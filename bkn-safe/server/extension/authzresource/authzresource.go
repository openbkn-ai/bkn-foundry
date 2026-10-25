// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package authzresource lets an edition build serve authorization resources
// that core bkn-safe has no upstream for.
//
// Core lists and checks the resources of the services it ships with (bkn
// backend, bkn agent, execution factory, vega backend) through fixed upstreams.
// A resource type owned by a paid service — knowledge_base, served by the
// enterprise kb-api — registers a Provider here instead, keyed by its resource
// type. Core consults the socket only for types it does not handle itself:
//
//   - the authorization workbench lists and resolves the type's resources
//     through Provider.List;
//   - permission requests check the requested resource through
//     Provider.Exists.
//
// # Registration is unconditional
//
// As with every socket, the enterprise entry point registers what it has and
// declares the lowest edition that may use it; whether it may be used is
// decided per call against the licence in force. A type that is not registered,
// or not licensed now, behaves exactly like a type core does not know.
package authzresource

import (
	"context"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement/socket"
	"github.com/openbkn-ai/licverify"
)

// Resource is one authorizable resource as the workbench shows it.
type Resource struct {
	ID   string
	Name string
}

// Page is one page of a resource listing.
type Page struct {
	Entries []Resource
	Total   int
}

// Query is a page request. Name filters by display name; Sort is always
// "name" and Direction "asc" or "desc", as core validates them.
type Query struct {
	Name      string
	Sort      string
	Direction string
	Offset    int
	Limit     int
}

// CallTimeout bounds every call core makes to a Provider, as core's own
// upstream clients are bounded: permission-request listings check each pending
// row in turn, so one stuck provider would otherwise hang the whole request.
// Providers should still bound their own I/O.
const CallTimeout = 5 * time.Second

// Provider serves one resource type. Both calls must fail closed: an error is
// reported as "unavailable", never as "missing".
type Provider interface {
	List(ctx context.Context, query Query) (Page, error)
	// Exists reports whether the resource still exists. false is reserved for
	// a resource the owning service says is gone.
	Exists(ctx context.Context, id string) (bool, error)
}

type entry struct {
	min      licverify.Edition
	provider Provider
}

var registry = socket.New[entry]("authzresource")

// Register serves resourceType through provider for editions at or above min.
// Assembly-time only; capability names the paid capability the type belongs to.
func Register(resourceType, capability string, min licverify.Edition, provider Provider) {
	if strings.TrimSpace(resourceType) == "" || provider == nil {
		panic("authzresource: Register needs a resource type and a provider")
	}
	if min == "" {
		panic("authzresource: " + resourceType + " registered without a minimum edition")
	}
	registry.Add(resourceType, capability, min, entry{min: min, provider: provider})
}

// Lookup returns the provider for resourceType when one is registered and the
// current licence reaches its edition.
func Lookup(resourceType string) (Provider, bool) {
	item, ok := registry.Get(resourceType)
	if !ok || !entitlement.AtLeast(item.min) {
		return nil, false
	}
	return item.provider, true
}

// ResetForTest empties the registry. Test binaries only (socket refuses
// otherwise).
func ResetForTest() { registry.ResetForTest() }
