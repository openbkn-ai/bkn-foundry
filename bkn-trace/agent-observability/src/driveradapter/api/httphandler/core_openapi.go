package httphandler

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"

type conversationPage struct {
	Entries    []sessionvo.Conversation `json:"entries"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

type requestPage struct {
	Entries    []sessionvo.RequestSummary `json:"entries"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

// @Summary Create or return the current managed Conversation
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param request body ensureConversationRequest true "Managed Conversation request"
// @Success 201 {object} sessionvo.Conversation
// @Failure 400,401,403,409,500 {object} lifecycleErrorEnvelope
// @Router /conversations [post]
func documentCreateConversation() {}

// @Summary List managed Conversations in the authorized owner scope
// @Tags lifecycle
// @Produce json
// @Param limit query int false "Page size, 1..100"
// @Success 200 {object} conversationPage
// @Failure 401,403,500 {object} lifecycleErrorEnvelope
// @Router /conversations [get]
func documentListConversations() {}

// @Summary Ensure the current managed Conversation
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param request body ensureConversationRequest true "Managed Conversation request"
// @Success 201 {object} sessionvo.Conversation
// @Failure 400,401,403,409,500 {object} lifecycleErrorEnvelope
// @Router /conversations:ensure-current [post]
func documentEnsureCurrentConversation() {}

// @Summary Create a new managed Conversation generation
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param request body ensureConversationRequest true "New generation request"
// @Success 201 {object} sessionvo.Conversation
// @Failure 400,401,403,409,500 {object} lifecycleErrorEnvelope
// @Router /conversations:create-new-generation [post]
func documentCreateConversationGeneration() {}

// @Summary Resume an active managed Conversation by Core ID
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param request body resumeConversationRequest true "Conversation resume request"
// @Success 200 {object} sessionvo.Conversation
// @Failure 400,401,403,404,409,500 {object} lifecycleErrorEnvelope
// @Router /conversations:resume-by-id [post]
func documentResumeConversation() {}

// @Summary Get one managed Conversation
// @Tags lifecycle
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Success 200 {object} sessionvo.Conversation
// @Failure 401,403,404,500 {object} lifecycleErrorEnvelope
// @Router /conversations/{conversation_id} [get]
func documentGetConversation() {}

// @Summary Close one managed Conversation
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Param request body closeConversationRequest true "Idempotent close request"
// @Success 200 {object} sessionvo.Conversation
// @Failure 400,401,403,404,409,500 {object} lifecycleErrorEnvelope
// @Router /conversations/{conversation_id}/close [post]
func documentCloseConversation() {}

// @Summary Start one managed Interaction
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Param request body startInteractionRequest true "Interaction start request"
// @Success 201 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,500 {object} lifecycleErrorEnvelope
// @Router /conversations/{conversation_id}/interactions [post]
func documentStartInteraction() {}

// @Summary Ensure an idempotent Operation intent and durable Receipt
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param conversation_id path string true "Conversation ID"
// @Param interaction_id path string true "Interaction ID"
// @Param request body ensureOperationRequest true "Operation intent"
// @Success 201 {object} operationResult
// @Failure 400,401,403,404,409,500 {object} lifecycleErrorEnvelope
// @Router /conversations/{conversation_id}/interactions/{interaction_id}/operations:ensure [post]
func documentEnsureOperation() {}

// @Summary Get one managed Interaction
// @Tags lifecycle
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Success 200 {object} sessionvo.Interaction
// @Failure 401,403,404,500 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id} [get]
func documentGetInteraction() {}

// @Summary Complete one managed Interaction
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Param request body terminalInteractionRequest true "Terminal closure manifest"
// @Success 200 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,422,500 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id}/complete [post]
func documentCompleteInteraction() {}

// @Summary Fail one managed Interaction
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Param request body terminalInteractionRequest true "Terminal closure manifest"
// @Success 200 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,422,500 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id}/fail [post]
func documentFailInteraction() {}

// @Summary Cancel one managed Interaction
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Param request body terminalInteractionRequest true "Terminal closure manifest"
// @Success 200 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,422,500 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id}/cancel [post]
func documentCancelInteraction() {}

// @Summary Hand off one managed Interaction
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param interaction_id path string true "Interaction ID"
// @Param request body terminalInteractionRequest true "Terminal closure manifest"
// @Success 200 {object} sessionvo.Interaction
// @Failure 400,401,403,404,409,422,500 {object} lifecycleErrorEnvelope
// @Router /interactions/{interaction_id}/handoff [post]
func documentHandoffInteraction() {}

// @Summary Get one Operation
// @Tags lifecycle
// @Produce json
// @Param operation_id path string true "Operation ID"
// @Success 200 {object} sessionvo.Operation
// @Failure 401,403,404,500 {object} lifecycleErrorEnvelope
// @Router /operations/{operation_id} [get]
func documentGetOperation() {}

// @Summary Start the next retry attempt for an Operation
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param operation_id path string true "Operation ID"
// @Param request body interactionLeaseRequest true "Current Interaction lease"
// @Success 201 {object} operationResult
// @Failure 400,401,403,404,409,500 {object} lifecycleErrorEnvelope
// @Router /operations/{operation_id}/attempts [post]
func documentStartOperationAttempt() {}

// @Summary Complete one Operation attempt
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param operation_id path string true "Operation ID"
// @Param attempt path int true "Attempt number"
// @Param request body finishAttemptRequest true "Durable operation result"
// @Success 200 {object} operationResult
// @Failure 400,401,403,404,409,500 {object} lifecycleErrorEnvelope
// @Router /operations/{operation_id}/attempts/{attempt}:complete [post]
func documentCompleteOperationAttempt() {}

// @Summary Fail one Operation attempt
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param operation_id path string true "Operation ID"
// @Param attempt path int true "Attempt number"
// @Param request body finishAttemptRequest true "Durable operation failure"
// @Success 200 {object} operationResult
// @Failure 400,401,403,404,409,500 {object} lifecycleErrorEnvelope
// @Router /operations/{operation_id}/attempts/{attempt}:fail [post]
func documentFailOperationAttempt() {}

// @Summary Get one durable Receipt
// @Tags lifecycle
// @Produce json
// @Param receipt_id path string true "Receipt ID"
// @Success 200 {object} sessionvo.Receipt
// @Failure 401,403,404,409,500 {object} lifecycleErrorEnvelope
// @Router /receipts/{receipt_id} [get]
func documentGetReceipt() {}

// @Summary List request projections from durable Receipts
// @Tags lifecycle
// @Produce json
// @Param limit query int false "Page size, 1..100"
// @Success 200 {object} requestPage
// @Failure 401,403,500 {object} lifecycleErrorEnvelope
// @Router /requests [get]
func documentListCoreRequests() {}

// @Summary Get one request projection from durable Receipts
// @Tags lifecycle
// @Produce json
// @Param request_id path string true "Request ID"
// @Success 200 {object} sessionvo.RequestSummary
// @Failure 401,403,404,500 {object} lifecycleErrorEnvelope
// @Router /requests/{request_id} [get]
func documentGetCoreRequest() {}

type resumeConversationRequest struct {
	ConversationID string `json:"conversation_id"`
}

type closeConversationRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}
