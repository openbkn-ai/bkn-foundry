package rdto

type ErrorResponse struct {
	ErrorCode string `json:"error_code"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	TraceID   string `json:"trace_id"`
	Details   any    `json:"details,omitempty"`
}

type TraceSearchByConversationResponse struct {
	ConversationID string `json:"conversation_id" example:"conv-1001"`
}
