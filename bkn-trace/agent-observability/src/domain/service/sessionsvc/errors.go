package sessionsvc

import "errors"

type ErrorCode string

const (
	CodeConversationRequired      ErrorCode = "conversation_required"
	CodeConversationNotFound      ErrorCode = "conversation_not_found"
	CodeConversationClosed        ErrorCode = "conversation_closed"
	CodeConversationExpired       ErrorCode = "conversation_expired"
	CodeConversationOwnerMismatch ErrorCode = "conversation_owner_mismatch"
	CodeInteractionInProgress     ErrorCode = "interaction_in_progress"
	CodeInteractionRequired       ErrorCode = "interaction_required"
	CodeInteractionTerminal       ErrorCode = "interaction_terminal"
	CodeAgentNameConflict         ErrorCode = "agent_name_conflict"
	CodeAgentNameInvalid          ErrorCode = "agent_name_invalid"
	CodeOperationRequired         ErrorCode = "operation_required"
	CodeIdempotencyConflict       ErrorCode = "idempotency_conflict"
	CodeReceiptPending            ErrorCode = "receipt_pending"
	CodeTerminalConflict          ErrorCode = "terminal_conflict"
	CodeClosureManifestInvalid    ErrorCode = "closure_manifest_invalid"
	CodeResourceNotDisclosed      ErrorCode = "resource_not_disclosed"
)

type DomainError struct {
	Code                 ErrorCode
	Message              string
	CurrentStatus        string
	CurrentInteractionID string
}

func (e *DomainError) Error() string {
	return e.Message
}

func IsCode(err error, code ErrorCode) bool {
	var domainErr *DomainError
	return errors.As(err, &domainErr) && domainErr.Code == code
}

func domainError(code ErrorCode, message string) error {
	return &DomainError{Code: code, Message: message}
}
