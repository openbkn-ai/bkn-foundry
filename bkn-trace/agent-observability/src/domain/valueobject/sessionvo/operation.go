package sessionvo

import "time"

type AttemptStatus string

const (
	AttemptPending   AttemptStatus = "pending"
	AttemptCompleted AttemptStatus = "completed"
	AttemptFailed    AttemptStatus = "failed"
)

type ReceiptStatus string
type EvidenceDurability string

const (
	ReceiptPending   ReceiptStatus = "pending"
	ReceiptCompleted ReceiptStatus = "completed"
	ReceiptFailed    ReceiptStatus = "failed"

	DurabilityPending EvidenceDurability = "pending"
	DurabilityDurable EvidenceDurability = "durable"
	DurabilityFailed  EvidenceDurability = "failed"
)

type Operation struct {
	ID                  string        `json:"operation_id"`
	ConversationID      string        `json:"conversation_id"`
	InteractionID       string        `json:"interaction_id"`
	OperationKey        string        `json:"operation_key"`
	ToolName            string        `json:"tool_name"`
	NormalizedInputHash string        `json:"normalized_input_hash"`
	ParentOperationID   string        `json:"parent_operation_id,omitempty"`
	CausationEventIDs   []string      `json:"causation_event_ids,omitempty"`
	Attempt             uint32        `json:"attempt"`
	AttemptStatus       AttemptStatus `json:"attempt_status"`
	Retryable           bool          `json:"retryable"`
	RowVersion          uint64        `json:"row_version"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

type Receipt struct {
	ID                   string             `json:"receipt_id"`
	SchemaVersion        string             `json:"schema_version"`
	Owner                Owner              `json:"owner"`
	ConversationID       string             `json:"conversation_id"`
	InteractionID        string             `json:"interaction_id"`
	OperationID          string             `json:"operation_id"`
	Attempt              uint32             `json:"attempt"`
	OperationKey         string             `json:"operation_key"`
	ToolName             string             `json:"tool_name"`
	NormalizedInputHash  string             `json:"normalized_input_hash"`
	Status               ReceiptStatus      `json:"receipt_status"`
	EvidenceDurability   EvidenceDurability `json:"evidence_durability"`
	Required             bool               `json:"required"`
	RequestID            string             `json:"request_id"`
	TraceID              string             `json:"trace_id"`
	CausationEventIDs    []string           `json:"causation_event_ids"`
	ObservedEvidenceRefs []string           `json:"observed_evidence_refs"`
	BusinessRefs         []BusinessRef      `json:"business_refs"`
	ArtifactRefs         []string           `json:"artifact_refs"`
	PartialReasons       []string           `json:"partial_reasons"`
	RowVersion           uint64             `json:"row_version"`
	IssuedAt             time.Time          `json:"issued_at"`
	TerminalAt           *time.Time         `json:"terminal_at,omitempty"`
	PayloadHash          string             `json:"payload_hash"`
}

type BusinessRef struct {
	RefType          string     `json:"ref_type"`
	RefID            string     `json:"ref_id"`
	BusinessDomainID string     `json:"business_domain_id"`
	Version          string     `json:"version"`
	AsOf             *time.Time `json:"as_of,omitempty"`
	DisplayHint      string     `json:"display_hint,omitempty"`
}
