// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"encoding/json"
)

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

// ActionTrialAccess runs a recognition action once, without recording anything: the objects it
// selects now. It runs as the account in ctx.
type ActionTrialAccess interface {
	TrialEventAction(ctx context.Context, branch string, actionType *ActionType) (*ActionEventTrial, error)
}

// EventHandlingOutcome is what came of executing an event's handling action. Exactly one of its
// fields says it: the execution started, the action type is governed and only a person can start
// it, the execution was refused for the reason given, or ontology-query could not be asked and it
// is tried again later.
type EventHandlingOutcome struct {
	ExecutionID string
	// Running is set when an execution of the action on the object was already running.
	Running     bool
	NeedsPerson string
	Refused     string
	Unavailable error
}

// EventHandlingAccess executes the handling action of an event on the event's object, as the
// account in ctx: the execution subject of the event's class.
type EventHandlingAccess interface {
	ExecuteForEvent(ctx context.Context, knID, actionTypeID string, identity, inputs json.RawMessage) EventHandlingOutcome
}
