// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

type deferredExportEnrichmentContextKey struct{}

// WithDeferredExportEnrichment marks a request whose caller will finish cross-resource
// enrichment after all export sections have been loaded. This lets the caller reuse one object
// map, one account-name batch, and the already-authorized relation and action lists.
func WithDeferredExportEnrichment(ctx context.Context) context.Context {
	return context.WithValue(ctx, deferredExportEnrichmentContextKey{}, true)
}

// IsAccountNameEnrichmentDeferred reports whether account-name lookup is owned by the caller.
func IsAccountNameEnrichmentDeferred(ctx context.Context) bool {
	return isExportEnrichmentDeferred(ctx)
}

// IsObjectReferenceEnrichmentDeferred reports whether simple object references are caller-owned.
func IsObjectReferenceEnrichmentDeferred(ctx context.Context) bool {
	return isExportEnrichmentDeferred(ctx)
}

// IsConceptGroupMemberScopeDeferred reports whether group membership and statistics are caller-owned.
func IsConceptGroupMemberScopeDeferred(ctx context.Context) bool {
	return isExportEnrichmentDeferred(ctx)
}

func isExportEnrichmentDeferred(ctx context.Context) bool {
	value, _ := ctx.Value(deferredExportEnrichmentContextKey{}).(bool)
	return value
}
