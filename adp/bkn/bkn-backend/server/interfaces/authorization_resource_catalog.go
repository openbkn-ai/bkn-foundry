// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

type authorizationResourceCatalogContextKey struct{}

// WithAuthorizationResourceCatalog marks an internal request that enumerates resource identities
// for authorization configuration. It must only be used by protected internal endpoints.
func WithAuthorizationResourceCatalog(ctx context.Context) context.Context {
	return context.WithValue(ctx, authorizationResourceCatalogContextKey{}, true)
}

// IsAuthorizationResourceCatalog reports whether a request is the internal resource catalog flow.
func IsAuthorizationResourceCatalog(ctx context.Context) bool {
	value, _ := ctx.Value(authorizationResourceCatalogContextKey{}).(bool)
	return value
}
