// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

type verifiedDependencySourcesContextKey struct{}
type dependencyBindingScopeContextKey struct{}

type dependencyBindingScope struct {
	KNID        string
	BindingType string
	BindingID   string
}

// WithVerifiedDependencySources makes a server-resolved proxy preflight result
// available to strict dependency validation in the same publication request.
func WithVerifiedDependencySources(ctx context.Context, sources []ProxyGrantResolvedSource) context.Context {
	if len(sources) == 0 {
		return ctx
	}
	copyOfSources := append([]ProxyGrantResolvedSource(nil), sources...)
	return context.WithValue(ctx, verifiedDependencySourcesContextKey{}, copyOfSources)
}

// WithDependencyBindingScope identifies the concrete server-built KN binding
// whose downstream dependency is about to be read.
func WithDependencyBindingScope(ctx context.Context, knID, bindingType, bindingID string) context.Context {
	return context.WithValue(ctx, dependencyBindingScopeContextKey{}, dependencyBindingScope{
		KNID: knID, BindingType: bindingType, BindingID: bindingID,
	})
}

// HasVerifiedDependencySources reports whether proxy preflight established a
// controlled dependency lookup for this publication request.
func HasVerifiedDependencySources(ctx context.Context) bool {
	sources, ok := ctx.Value(verifiedDependencySourcesContextKey{}).([]ProxyGrantResolvedSource)
	return ok && len(sources) > 0
}

// VerifiedDependencyAccount returns the effective delegator for one exact
// target operation. The target list comes from the server-built candidate
// model and bkn-safe preflight, never from a client assertion.
func VerifiedDependencyAccount(ctx context.Context, resourceType, resourceID, operation string) (AccountInfo, bool) {
	sources, _ := ctx.Value(verifiedDependencySourcesContextKey{}).([]ProxyGrantResolvedSource)
	scope, scoped := ctx.Value(dependencyBindingScopeContextKey{}).(dependencyBindingScope)
	if len(sources) > 0 && !scoped {
		return AccountInfo{}, false
	}
	for _, source := range sources {
		if source.ResourceType == resourceType && source.ResourceID == resourceID &&
			source.Operation == operation && source.KNID == scope.KNID &&
			source.BindingType == scope.BindingType && source.BindingID == scope.BindingID &&
			source.GrantedBy != "" {
			return AccountInfo{ID: source.GrantedBy, Type: ACCESSOR_TYPE_USER}, true
		}
	}
	return AccountInfo{}, false
}
