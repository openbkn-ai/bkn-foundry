// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"

type conversationPage struct {
	Entries []sessionvo.Conversation `json:"entries"`
}

type requestPage struct {
	Entries    []sessionvo.RequestSummary `json:"entries"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

// @Summary Create or return the current managed Conversation
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body ensureConversationRequest true "Managed Conversation request"
// @Success 201 {object} sessionvo.Conversation
// @Failure 400,401,403,409,500,503 {object} lifecycleErrorEnvelope
// @Router /conversations [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentCreateConversation() {}

// @Summary List managed Conversations in the authorized owner scope
// @Tags lifecycle
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Page size, 1..100"
// @Success 200 {object} conversationPage
// @Failure 401,403,500,503 {object} lifecycleErrorEnvelope
// @Router /conversations [get]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentListConversations() {}

// @Summary Ensure the current managed Conversation
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body ensureConversationRequest true "Managed Conversation request"
// @Success 201 {object} sessionvo.Conversation
// @Failure 400,401,403,409,500,503 {object} lifecycleErrorEnvelope
// @Router /conversations:ensure-current [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentEnsureCurrentConversation() {}

// @Summary Create a new managed Conversation generation
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body ensureConversationRequest true "New generation request"
// @Success 201 {object} sessionvo.Conversation
// @Failure 400,401,403,409,500,503 {object} lifecycleErrorEnvelope
// @Router /conversations:create-new-generation [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentCreateConversationGeneration() {}

// @Summary Resume an active managed Conversation by Core ID
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body resumeConversationRequest true "Conversation resume request"
// @Success 200 {object} sessionvo.Conversation
// @Failure 400,401,403,404,409,500,503 {object} lifecycleErrorEnvelope
// @Router /conversations:resume-by-id [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentResumeConversation() {}

// @Summary Get one managed Conversation
// @Tags lifecycle
// @Security BearerAuth
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Success 200 {object} sessionvo.Conversation
// @Failure 401,403,404,500,503 {object} lifecycleErrorEnvelope
// @Router /conversations/{conversation_id} [get]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentGetConversation() {}

// @Summary Close one managed Conversation
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Param request body closeConversationRequest true "Idempotent close request"
// @Success 200 {object} sessionvo.Conversation
// @Failure 400,401,403,404,409,500,503 {object} lifecycleErrorEnvelope
// @Router /conversations/{conversation_id}/close [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentCloseConversation() {}

// @Summary Start one managed Interaction
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Param request body startInteractionRequest true "Interaction start request"
// @Success 201 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,500,503 {object} lifecycleErrorEnvelope
// @Router /conversations/{conversation_id}/interactions [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentStartInteraction() {}

// @Summary Ensure an idempotent Operation intent and durable Receipt
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Param interaction_id path string true "Interaction ID"
// @Param request body ensureOperationRequest true "Operation intent"
// @Success 201 {object} operationResult
// @Failure 400,401,403,404,409,500,503 {object} lifecycleErrorEnvelope
// @Router /conversations/{conversation_id}/interactions/{interaction_id}/operations:ensure [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentEnsureOperation() {}

// @Summary Get one managed Interaction
// @Tags lifecycle
// @Security BearerAuth
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Success 200 {object} sessionvo.Interaction
// @Failure 401,403,404,500,503 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id} [get]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentGetInteraction() {}

// @Summary List raw Operation call facts for one Interaction
// @Tags lifecycle
// @Security BearerAuth
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Success 200 {object} operationCallFactsResponse
// @Failure 401,403,404,500,503 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id}/operations [get]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentListInteractionOperations() {}

// @Summary Finish one Interaction through the Agent-facing facade
// @Description Derives the current lease and closure manifest from authoritative Operations and Receipts. Ordinary Agents do not submit concurrency or closure internals.
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Param request body managedFinishInteractionRequest true "Managed finish request"
// @Success 200 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,422,500,503 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id}/finish [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentManagedFinishInteraction() {}

// @Summary Complete one managed Interaction
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Param request body terminalInteractionRequest true "Terminal closure manifest"
// @Success 200 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,422,500,503 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id}/complete [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentCompleteInteraction() {}

// @Summary Fail one managed Interaction
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Param request body terminalInteractionRequest true "Terminal closure manifest"
// @Success 200 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,422,500,503 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id}/fail [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentFailInteraction() {}

// @Summary Cancel one managed Interaction
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Param request body terminalInteractionRequest true "Terminal closure manifest"
// @Success 200 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,422,500,503 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id}/cancel [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentCancelInteraction() {}

// @Summary Hand off one managed Interaction
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Param request body terminalInteractionRequest true "Terminal closure manifest"
// @Success 200 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,422,500,503 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id}/handoff [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentHandoffInteraction() {}

// @Summary Get one Operation
// @Tags lifecycle
// @Security BearerAuth
// @Produce json
// @Param operation_id path string true "Operation ID"
// @Success 200 {object} sessionvo.Operation
// @Failure 401,403,404,500,503 {object} lifecycleErrorEnvelope
// @Router /operations/{operation_id} [get]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentGetOperation() {}

// @Summary Get one authorized Operation attempt call fact
// @Tags lifecycle
// @Security BearerAuth
// @Produce json
// @Param operation_id path string true "Operation ID"
// @Param attempt path int true "Attempt number"
// @Success 200 {object} sessionvo.OperationCallFact
// @Failure 400,401,403,404,500,503 {object} lifecycleErrorEnvelope
// @Router /operations/{operation_id}/attempts/{attempt} [get]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentGetOperationAttemptFact() {}

// @Summary Start the next retry attempt for an Operation
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param operation_id path string true "Operation ID"
// @Param request body interactionLeaseRequest true "Current Interaction lease"
// @Success 201 {object} operationResult
// @Failure 400,401,403,404,409,500,503 {object} lifecycleErrorEnvelope
// @Router /operations/{operation_id}/attempts [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentStartOperationAttempt() {}

// @Summary Complete one Operation attempt
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param operation_id path string true "Operation ID"
// @Param attempt path int true "Attempt number"
// @Param request body finishAttemptRequest true "Durable operation result"
// @Success 200 {object} operationResult
// @Failure 400,401,403,404,409,500,503 {object} lifecycleErrorEnvelope
// @Router /operations/{operation_id}/attempts/{attempt}:complete [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentCompleteOperationAttempt() {}

// @Summary Fail one Operation attempt
// @Tags lifecycle
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param operation_id path string true "Operation ID"
// @Param attempt path int true "Attempt number"
// @Param request body finishAttemptRequest true "Durable operation failure"
// @Success 200 {object} operationResult
// @Failure 400,401,403,404,409,500,503 {object} lifecycleErrorEnvelope
// @Router /operations/{operation_id}/attempts/{attempt}:fail [post]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentFailOperationAttempt() {}

// @Summary Get one durable Receipt
// @Tags lifecycle
// @Security BearerAuth
// @Produce json
// @Param receipt_id path string true "Receipt ID"
// @Success 200 {object} sessionvo.Receipt
// @Failure 401,403,404,409,500,503 {object} lifecycleErrorEnvelope
// @Router /receipts/{receipt_id} [get]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentGetReceipt() {}

// @Summary List request projections from durable Receipts
// @Tags lifecycle
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Page size, 1..100"
// @Success 200 {object} requestPage
// @Failure 401,403,500,503 {object} lifecycleErrorEnvelope
// @Router /requests [get]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentListCoreRequests() {}

// @Summary Get one request projection from durable Receipts
// @Tags lifecycle
// @Security BearerAuth
// @Produce json
// @Param request_id path string true "Request ID"
// @Success 200 {object} sessionvo.RequestSummary
// @Failure 401,403,404,500,503 {object} lifecycleErrorEnvelope
// @Router /requests/{request_id} [get]
//
//nolint:unused // Swag parses this annotation carrier during documentation generation.
func documentGetCoreRequest() {}

//nolint:unused // Swag resolves this request schema from annotation references.
type resumeConversationRequest struct {
	ConversationID string `json:"conversation_id" binding:"required"`
}

//nolint:unused // Swag resolves this request schema from annotation references.
type closeConversationRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}
