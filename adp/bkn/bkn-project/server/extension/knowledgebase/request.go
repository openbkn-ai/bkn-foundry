// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import "context"

type operatorAuthorizationKey struct{}

// A function-valued entry keeps Go-syntax context formatting from traversing
// the credential; the token is exposed only by the explicit accessor below.
type operatorAuthorization func() string

func (operatorAuthorization) String() string   { return "[redacted]" }
func (operatorAuthorization) GoString() string { return "[redacted]" }

// WithOperatorAuthorization forwards an authenticated K1/K2 interaction only.
// Never persist this context or copy credentials into task inputs, decisions or git.
// Background K4 identity is deliberately not defined by this helper.
func WithOperatorAuthorization(ctx context.Context, authorization string) context.Context {
	return context.WithValue(ctx, operatorAuthorizationKey{}, operatorAuthorization(func() string { return authorization }))
}

// OperatorAuthorization is consumed only by the source adapter's HTTP request.
func OperatorAuthorization(ctx context.Context) string {
	value, _ := ctx.Value(operatorAuthorizationKey{}).(operatorAuthorization)
	if value == nil {
		return ""
	}
	return value()
}

// SourceError preserves upstream HTTP semantics without exposing upstream bodies,
// request URLs or credentials to logs or clients.
type SourceError struct{ StatusCode int }

func (e *SourceError) Error() string { return "knowledge base source request failed" }
