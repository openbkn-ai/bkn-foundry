// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package connector is the socket the enterprise code line plugs data-source
// connectors into.
//
// One shape only: ExtraConnector, a connector type this binary has but the
// community binary does not. A community build has an empty registry, so the
// type is absent from the catalogue entirely rather than present-and-refusing.
//
// The socket sits where connectors are assembled — the factory — and never
// inside query, discovery or catalog logic. Few sockets is what makes two code
// lines affordable to maintain.
//
// # Registration is unconditional
//
// Nothing here consults the licence at registration time. The enterprise entry
// point registers everything it has and declares what each item costs via
// MinEdition; whether it may be used is decided per call, against the licence
// in force at that moment.
//
// The alternative — ee checks its own licence and registers only what it is
// entitled to — looks tidier and is wrong: a process that booted without a
// certificate would register nothing, and the socket freezes when the server
// starts. Installing the certificate afterwards could then do nothing until
// somebody restarted the process, which on a customer's site means an outage
// window to fix a licensing mistake.
//
// # What "not entitled" looks like
//
// It looks like the connector type does not exist. It is filtered out of the
// type catalogue, GetByType reports it missing, and creating a catalog against
// it fails the way an unknown type fails — a 404, not a 403. The community
// binary genuinely does not have the type, and an under-licensed enterprise
// binary has to be indistinguishable from it: an explicit "requires
// professional" would advertise the paid surface to anyone probing.
//
// # Metadata comes from the entry, not from the database
//
// Built-in connector types are seeded into t_connector_type by a migration. A
// paid connector must not be, for two reasons that are easy to discover the
// hard way:
//
//  1. A row with f_mode='local' whose implementation is not compiled in makes
//     RegisterAllConnectors fail, and the caller panics. Seeding the row in
//     core would therefore kill every community boot; seeding it from an ee
//     migration would kill the boot after a downgrade, when the customer swaps
//     back to the community image and the row stays behind.
//  2. A migration runs once; a licence changes at runtime. Metadata that lives
//     in the database cannot appear and disappear with the certificate, so the
//     type would keep showing up in the catalogue after a downgrade.
//
// So the entry carries its own metadata, and the type service merges the
// licensed ones into what it read from the database.
//
// Design: bkn-docs docs/shared/licensing/ee-design.md §5.
package connector

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement/socket"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
)

// ExtraConnector is one connector type contributed by the enterprise code line.
//
// It carries its own metadata rather than relying on a database row; see the
// package comment for why a migration cannot do this job.
type ExtraConnector struct {
	// Capability names the paid capability this connector belongs to. Several
	// connector types commonly share one — `connector_certified` covers every
	// certified commercial database — and the assembly registry records it once.
	Capability string
	// MinEdition is the lowest tier that may use it. Required: the zero value
	// would silently make a paid connector free.
	MinEdition licverify.Edition

	// Type is the connector type id, and the key in this socket. It must equal
	// the prototype's GetType(); Register enforces that, because the catalogue
	// is keyed by one and the implementation resolved by the other.
	Type string
	// Description is what the catalogue shows for this type. It is the one
	// field the Connector interface has no getter for.
	Description string
	// Tags is optional and passes straight through to the catalogue entry.
	Tags []string

	// New builds a fresh prototype. The factory holds one instance per type and
	// calls its New(cfg) per connection, mirroring how built-in connectors are
	// installed; this returns that held instance.
	//
	// Everything else the catalogue needs — mode, category, field config — is
	// read off the prototype, so an entry cannot describe itself as one thing
	// and behave as another.
	New func() interfaces.Connector
}

// The registry comes from comm-go: it carries the invariants every socket
// shares — registration only during assembly, a declared capability and minimum
// tier, no duplicate keys, stable ordering. What stays here is what is specific
// to connectors: the shape above, how metadata is derived, and what refusing
// looks like.
var extras = socket.New[ExtraConnector]("vega connector")

// Register adds a connector type to the socket. Call it from the enterprise
// entry point, between app.Boot and app.Run.
//
// It panics on a mismatch between Type and the prototype's GetType(), on a
// missing New, and — via the registry — on registration after assembly, a
// duplicate type, a missing capability or a zero MinEdition. Every one of those
// is a wiring mistake that would otherwise surface as a connector that silently
// never works, or as a paid connector that is silently free.
func Register(c ExtraConnector) {
	if c.New == nil {
		panic("vega connector: " + c.Type + " registered without a New func")
	}
	if got := c.New().GetType(); got != c.Type {
		panic("vega connector: entry Type " + c.Type + " disagrees with prototype GetType() " + got)
	}
	extras.Add(c.Type, c.Capability, c.MinEdition, c)
}

// All returns every registered entry regardless of licence.
//
// This is the assembly-time view, and the factory is its only caller: the
// implementation goes into the connector map unconditionally, so that a
// certificate installed later takes effect on the next request instead of
// waiting for a restart. Runtime callers want Licensed or Allowed.
func All() []ExtraConnector { return extras.All() }

// Licensed returns the entries the licence in force right now covers.
//
// Called per request, not cached: a certificate that arrives — or lapses —
// while the process runs changes the answer, and that is the whole point of
// deciding here rather than at registration.
func Licensed() []ExtraConnector {
	all := extras.All()
	out := make([]ExtraConnector, 0, len(all))
	for _, c := range all {
		if entitlement.AtLeast(c.MinEdition) {
			out = append(out, c)
		}
	}
	return out
}

// Allowed reports whether this connector type may be used right now.
//
// A type this socket never heard of is not this package's business, so the
// answer is false and the caller falls through to its own handling — a built-in
// type must not need the licence's permission to work.
func Allowed(connectorType string) bool {
	c, ok := extras.Get(connectorType)
	if !ok {
		return false
	}
	return entitlement.AtLeast(c.MinEdition)
}

// Registered reports whether the type came from this socket, licensed or not.
//
// Callers use it to tell "built-in type" from "paid type this build happens to
// carry"; it must never be used to decide whether to serve, because it is true
// under a community certificate as well.
func Registered(connectorType string) bool {
	_, ok := extras.Get(connectorType)
	return ok
}

// TypeOf renders an entry as the catalogue's own type record, so callers merge
// one shape instead of two.
//
// Mode, category and field config are read off the prototype rather than
// restated on the entry: the factory cross-checks a local connector's field
// config against the catalogue record and treats a mismatch as fatal, and two
// hand-written copies of the same map is exactly how that mismatch happens.
//
// Enabled is always true. The database flag exists so an operator can switch a
// seeded connector off; a paid connector has no row to switch, and the question
// it answers here — may this be used — is the licence's to answer.
func TypeOf(c ExtraConnector) *interfaces.ConnectorType {
	proto := c.New()
	return &interfaces.ConnectorType{
		Type:        proto.GetType(),
		Name:        proto.GetName(),
		Tags:        c.Tags,
		Description: c.Description,
		Mode:        proto.GetMode(),
		Category:    proto.GetCategory(),
		FieldConfig: proto.GetFieldConfig(),
		Enabled:     true,
	}
}

// LicensedTypes returns the catalogue records for the entries the current
// licence covers, ready to be merged into what the type service read from the
// database.
func LicensedTypes() []*interfaces.ConnectorType {
	licensed := Licensed()
	out := make([]*interfaces.ConnectorType, 0, len(licensed))
	for _, c := range licensed {
		out = append(out, TypeOf(c))
	}
	return out
}

// ResetForTest empties the socket and reinstalls g as the licence gate,
// unfreezing the assembly registry along the way. It is the opening line of a
// socket test: without it a test inherits the previous test's connectors.
//
// The socket is process-global by design — it models one binary's assembly,
// which happens once — so the enterprise repository's own tests need this
// helper too, which is why it is exported rather than kept in a _test.go file.
// Being exported means it is linked into the production binary, and it clears a
// registry that entitlement.MustBeAssembling panics to protect, so it refuses
// to run outside a test binary.
func ResetForTest(g entitlement.Gate) {
	if !testing.Testing() {
		panic("vega connector: ResetForTest is test-only and must never run in a production binary")
	}
	extras.ResetForTest()
	entitlement.SetGateForTest(g)
}
