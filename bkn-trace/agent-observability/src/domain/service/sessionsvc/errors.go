// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

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

	// Cause names the specific check that failed. It never reaches the client.
	//
	// resource_not_disclosed deliberately answers "not found in the authorized
	// scope" to a missing resource and to one that exists but belongs to someone
	// else alike, because saying which would confirm the resource exists. That
	// posture is right for the caller and useless for whoever has to diagnose the
	// failure: a single code and a single sentence cover a missing conversation, an
	// owner mismatch, an interaction under a different conversation and several
	// more, and nothing on the server distinguishes them either. Carry the reason
	// out of the domain so the transport can log it while still returning the same
	// opaque answer.
	Cause string
}

// Causes for CodeResourceNotDisclosed. Server-side diagnostics only.
const (
	CauseConversationNotFound            = "conversation_not_found"
	CauseConversationOwnerMismatch       = "conversation_owner_mismatch"
	CauseInteractionNotFound             = "interaction_not_found"
	CauseInteractionNotInConversation    = "interaction_missing_or_not_in_conversation"
	CauseOperationNotFound               = "operation_not_found"
	CauseOperationNotInConversation      = "operation_missing_or_not_in_conversation"
	CauseParentOperationNotFound         = "parent_operation_not_found"
	CauseParentOperationOtherInteraction = "parent_operation_in_other_interaction"
	CauseOperationCallFactNotFound       = "operation_call_fact_not_found"
	CauseReceiptNotFound                 = "receipt_not_found"
	CauseReceiptNotInScope               = "receipt_not_in_scope"
)

// Causes for CodeOperationRequired. That code is the catch-all of this table: a
// malformed body, a missing protocol field, an unknown operation route, a
// non-positive attempt and a full interaction all answer with it and all map to
// required_action=ensure_operation, while every other code names an action the
// caller can actually take. Until that is split, the cause is what tells the
// operator which of them happened.
const (
	CauseOperationCapacityReached = "interaction_operation_capacity_reached"
)

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

func domainErrorWithCause(code ErrorCode, message, cause string) error {
	return &DomainError{Code: code, Message: message, Cause: cause}
}
