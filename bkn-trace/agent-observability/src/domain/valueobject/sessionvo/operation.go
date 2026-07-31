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
	ID                  string        `json:"operation_id" binding:"required"`
	ConversationID      string        `json:"conversation_id" binding:"required"`
	InteractionID       string        `json:"interaction_id" binding:"required"`
	OperationKey        string        `json:"operation_key" binding:"required"`
	ToolName            string        `json:"tool_name" binding:"required"`
	NormalizedInputHash string        `json:"normalized_input_hash" binding:"required"`
	ParentOperationID   string        `json:"parent_operation_id,omitempty"`
	CausationEventIDs   []string      `json:"causation_event_ids,omitempty"`
	Attempt             uint32        `json:"attempt" binding:"required"`
	AttemptStatus       AttemptStatus `json:"attempt_status" binding:"required"`
	Retryable           bool          `json:"retryable" binding:"required"`
	RowVersion          uint64        `json:"row_version" binding:"required"`
	CreatedAt           time.Time     `json:"created_at" binding:"required"`
	UpdatedAt           time.Time     `json:"updated_at" binding:"required"`
}

type Receipt struct {
	ID                   string             `json:"receipt_id" binding:"required"`
	SchemaVersion        string             `json:"schema_version" binding:"required"`
	Owner                Owner              `json:"owner" binding:"required"`
	ConversationID       string             `json:"conversation_id" binding:"required"`
	InteractionID        string             `json:"interaction_id" binding:"required"`
	OperationID          string             `json:"operation_id" binding:"required"`
	Attempt              uint32             `json:"attempt" binding:"required"`
	OperationKey         string             `json:"operation_key" binding:"required"`
	ToolName             string             `json:"tool_name" binding:"required"`
	NormalizedInputHash  string             `json:"normalized_input_hash" binding:"required"`
	Status               ReceiptStatus      `json:"receipt_status" binding:"required"`
	EvidenceDurability   EvidenceDurability `json:"evidence_durability" binding:"required"`
	Required             bool               `json:"required" binding:"required"`
	RequestID            string             `json:"request_id" binding:"required"`
	TraceID              string             `json:"trace_id" binding:"required"`
	CausationEventIDs    []string           `json:"causation_event_ids" binding:"required"`
	ObservedEvidenceRefs []string           `json:"observed_evidence_refs" binding:"required"`
	BusinessRefs         []BusinessRef      `json:"business_refs" binding:"required"`
	ArtifactRefs         []string           `json:"artifact_refs" binding:"required"`
	PartialReasons       []string           `json:"partial_reasons" binding:"required"`
	RowVersion           uint64             `json:"row_version" binding:"required"`
	IssuedAt             time.Time          `json:"issued_at" binding:"required"`
	TerminalAt           *time.Time         `json:"terminal_at,omitempty"`
	PayloadHash          string             `json:"payload_hash" binding:"required"`
}

type BusinessRef struct {
	RefType          string     `json:"ref_type" binding:"required"`
	RefID            string     `json:"ref_id" binding:"required"`
	BusinessDomainID string     `json:"business_domain_id" binding:"required"`
	Version          string     `json:"version" binding:"required"`
	AsOf             *time.Time `json:"as_of,omitempty"`
	DisplayHint      string     `json:"display_hint,omitempty"`
}
