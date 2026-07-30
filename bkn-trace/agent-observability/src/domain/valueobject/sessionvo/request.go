package sessionvo

import "time"

type RequestSummary struct {
	RequestID      string    `json:"request_id"`
	ConversationID string    `json:"conversation_id"`
	InteractionID  string    `json:"interaction_id"`
	OperationCount int       `json:"operation_count"`
	ReceiptCount   int       `json:"receipt_count"`
	TraceIDs       []string  `json:"trace_ids,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}
