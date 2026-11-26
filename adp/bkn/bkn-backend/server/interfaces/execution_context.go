// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

type executionIDKey struct{}

// WithExecutionID is ctx for a request a cluster caller makes for an action execution.
func WithExecutionID(ctx context.Context, executionID string) context.Context {
	if executionID == "" {
		return ctx
	}
	return context.WithValue(ctx, executionIDKey{}, executionID)
}

// ExecutionIDOf is the action execution the request is made for, or "".
func ExecutionIDOf(ctx context.Context) string {
	id, _ := ctx.Value(executionIDKey{}).(string)
	return id
}
