// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// ActionExecutionInspector runs the same admission guards as ExecuteAction,
// without creating an execution or invoking its source.
type ActionExecutionInspector interface {
	InspectActionExecution(context.Context, *ActionExecutionRequest) (*ActionExecutionInspection, error)
}

type ActionExecutionInspection struct {
	KNID                   string                 `json:"kn_id"`
	ActionTypeID           string                 `json:"action_type_id"`
	ObservedAt             int64                  `json:"observed_at"`
	Phase                  string                 `json:"phase"`
	Decision               string                 `json:"decision"`
	ErrorCode              string                 `json:"error_code,omitempty"`
	Operation              *ActionOperationConfig `json:"operation_config,omitempty"`
	Mode                   string                 `json:"execution_mode,omitempty"`
	TargetCount            int                    `json:"target_count,omitempty"`
	TotalCount             int                    `json:"total_count,omitempty"`
	Targets                []map[string]any       `json:"targets,omitempty"`
	Fingerprint            string                 `json:"fingerprint,omitempty"`
	DuplicateCheckEnabled  bool                   `json:"duplicate_check_enabled"`
	DuplicateWindowSeconds int                    `json:"duplicate_window_seconds"`
	DuplicateExecutionID   string                 `json:"duplicate_execution_id,omitempty"`
	PreconditionsChecked   int                    `json:"preconditions_checked"`
}

type ActionInvocationObservation struct {
	Authorization       string `json:"authorization"`
	SourceAuthorization string `json:"source_authorization"`
	ErrorCode           string `json:"error_code,omitempty"`
}
