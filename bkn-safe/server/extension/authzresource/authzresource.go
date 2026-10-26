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
//
// # Paid resource types
//
// RequireEdition is separate from Register: it puts a minimum edition on the
// resource type itself, so core's enforcer answers every authorization question
// on it as "not granted" while the licence in force is below that edition —
// super administrators included (#2512). This is the licence gate for an
// enterprise-only service that authorizes every request through bkn-safe, such
// as the knowledge base: it already trusts these answers for every data access,
// so gating the edition on them adds no trust surface (ee-design.md §2). A type
// with a provider but no RequireEdition is not gated; a gated type need not
// have a provider.
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

var editions = socket.New[licverify.Edition]("authzresource edition")

// RequireEdition makes resourceType a paid type: below min, core answers every
// check on it as not granted and refuses grants on it. Assembly-time only;
// capability names the paid capability the type belongs to.
func RequireEdition(resourceType, capability string, min licverify.Edition) {
	if strings.TrimSpace(resourceType) == "" {
		panic("authzresource: RequireEdition needs a resource type")
	}
	if min == "" {
		panic("authzresource: " + resourceType + " required without a minimum edition")
	}
	editions.Add(resourceType, capability, min, min)
}

// Entitled reports whether the licence in force reaches resourceType's
// required edition. Types nobody declared are always entitled, which is every
// type in a community binary.
func Entitled(resourceType string) bool {
	min, gated := editions.Get(resourceType)
	return !gated || entitlement.AtLeast(min)
}

// ResetForTest empties both registries. Test binaries only (socket refuses
// otherwise).
func ResetForTest() {
	registry.ResetForTest()
	editions.ResetForTest()
}
