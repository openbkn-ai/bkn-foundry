// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// ActionExecutionCheckRequest contains schedule inputs that affect the trusted
// action dependency check. The execution subject is always read from context.
type ActionExecutionCheckRequest struct {
	KNID          string
	ActionTypeID  string
	DynamicParams map[string]any
}

type ActionExecutionAccess interface {
	CheckActionExecution(ctx context.Context, request ActionExecutionCheckRequest) error
}
