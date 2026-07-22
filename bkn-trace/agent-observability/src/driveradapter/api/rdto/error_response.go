package rdto

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type TraceSearchByConversationResponse struct {
	ConversationID string `json:"conversation_id" example:"conv-1001"`
}
