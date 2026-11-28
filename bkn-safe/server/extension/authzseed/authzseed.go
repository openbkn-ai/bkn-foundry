// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package authzseed lets an edition build contribute the authorization
// vocabulary of resource types that core bkn-safe does not know.
//
// Core seeds its own resource types, operations and built-in role grants from
// embedded files on every start. A resource type owned by a paid service
// (Execution Factory's tool_box, function, mcp and skill) is declared here
// instead, so a Community binary neither seeds nor recognises it. An Extension
// carries four things:
//
//   - Catalog: resource types and operations, in the shape of core's
//     authorization registry file;
//   - Grants: built-in role grants on those types, in the shape of core's
//     grants file;
//   - BundleOperations: the operations the access bundle may grant on a type;
//   - ProxyOperations: the operations a proxy-grant source may name on a type.
//
// # Registration happens before Boot
//
// Boot seeds the catalog and the role grants, so an Extension has to be
// registered before app.Boot; Boot then seals the registry. Registration is
// unconditional and licence-blind, like every socket: whether the types may be
// used is a separate per-call decision (authzresource.RequireEdition), made
// against the licence in force.
package authzseed

import (
	"strings"
	"sync"
	"testing"
)

// Extension is one edition build's contribution. Every field is optional.
type Extension struct {
	// Catalog is a JSON document with a "resource_types" array, in the shape of
	// core's embedded authorization registry.
	Catalog []byte
	// Grants is a JSON document with a "grants" array, in the shape of core's
	// embedded built-in role grants.
	Grants []byte
	// BundleOperations maps a resource type to the operations the Community
	// access bundle may grant on it.
	BundleOperations map[string][]string
	// ProxyOperations maps a resource type to the operations a proxy-grant
	// source may name on it.
	ProxyOperations map[string][]string
}

var (
	mu         sync.RWMutex
	sealed     bool
	extensions []Extension
)

// Register adds an extension. It panics once Boot has sealed the registry:
// the seed has already run, so a later registration would silently do nothing.
func Register(ext Extension) {
	mu.Lock()
	defer mu.Unlock()
	if sealed {
		panic("authzseed: Register after app.Boot; register before Boot, which seeds the catalog")
	}
	for _, ops := range []map[string][]string{ext.BundleOperations, ext.ProxyOperations} {
		for resourceType := range ops {
			if strings.TrimSpace(resourceType) == "" {
				panic("authzseed: an operation list needs a resource type")
			}
		}
	}
	extensions = append(extensions, ext)
}

// Seal closes the registry. Boot calls it before seeding.
func Seal() {
	mu.Lock()
	defer mu.Unlock()
	sealed = true
}

// Registered returns the registered extensions in registration order.
func Registered() []Extension {
	mu.RLock()
	defer mu.RUnlock()
	return append([]Extension(nil), extensions...)
}

// BundleOperations returns the extension-declared bundle operations for one
// resource type.
func BundleOperations(resourceType string) ([]string, bool) {
	return lookup(resourceType, func(e Extension) map[string][]string { return e.BundleOperations })
}

// ProxyOperations returns the extension-declared proxy-grant operations for one
// resource type.
func ProxyOperations(resourceType string) ([]string, bool) {
	return lookup(resourceType, func(e Extension) map[string][]string { return e.ProxyOperations })
}

func lookup(resourceType string, pick func(Extension) map[string][]string) ([]string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	for _, ext := range extensions {
		if ops, ok := pick(ext)[resourceType]; ok {
			return append([]string(nil), ops...), true
		}
	}
	return nil, false
}

// ResetForTest empties and reopens the registry. Test binaries only.
func ResetForTest() {
	if !testing.Testing() {
		panic("authzseed: ResetForTest outside a test binary")
	}
	mu.Lock()
	defer mu.Unlock()
	sealed = false
	extensions = nil
}
