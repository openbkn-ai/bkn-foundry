package observabilityvo

import "context"

type sourceAuthorizationContextKey struct{}
type sourceAccessScopeContextKey struct{}

type SourceAccessScope struct {
	TenantID       string
	BusinessDomain string
}

func WithSourceAuthorization(ctx context.Context, authorization string) context.Context {
	return context.WithValue(ctx, sourceAuthorizationContextKey{}, authorization)
}

func SourceAuthorization(ctx context.Context) string {
	value, _ := ctx.Value(sourceAuthorizationContextKey{}).(string)
	return value
}

func WithSourceAccessScope(ctx context.Context, scope SourceAccessScope) context.Context {
	return context.WithValue(ctx, sourceAccessScopeContextKey{}, scope)
}

func SourceAccessScopeFromContext(ctx context.Context) SourceAccessScope {
	scope, _ := ctx.Value(sourceAccessScopeContextKey{}).(SourceAccessScope)
	return scope
}
