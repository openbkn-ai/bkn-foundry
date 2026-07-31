package sessionvo

import "time"

type InteractionStatus string

const (
	InteractionActive    InteractionStatus = "active"
	InteractionCompleted InteractionStatus = "completed"
	InteractionFailed    InteractionStatus = "failed"
	InteractionCanceled  InteractionStatus = "canceled"
	InteractionHandedOff InteractionStatus = "handed_off"
	InteractionAbandoned InteractionStatus = "abandoned"
)

type EvidenceStatus string

const (
	EvidenceNotApplicable EvidenceStatus = "not_applicable"
	EvidenceAssembling    EvidenceStatus = "assembling"
	EvidenceComplete      EvidenceStatus = "complete"
	EvidencePartial       EvidenceStatus = "partial"
	EvidenceFailed        EvidenceStatus = "failed"
)

type ClosureManifest struct {
	Version              string              `json:"completion_manifest_version" binding:"required"`
	AnswerArtifactRef    string              `json:"answer_artifact_ref,omitempty"`
	Claims               []string            `json:"claims,omitempty"`
	ExpectedOperations   []ExpectedOperation `json:"expected_operations,omitempty"`
	ExpectedReceipts     []ExpectedReceipt   `json:"expected_receipts,omitempty"`
	AssemblerDeadline    *time.Time          `json:"assembler_deadline,omitempty"`
	CompletionReason     string              `json:"completion_reason" binding:"required"`
	SystemPartialReasons []string            `json:"system_partial_reasons,omitempty"`
}

type ExpectedOperation struct {
	OperationID string `json:"operation_id" binding:"required"`
	Required    bool   `json:"required" binding:"required"`
}

type ExpectedReceipt struct {
	ReceiptID string `json:"receipt_id" binding:"required"`
	Required  bool   `json:"required" binding:"required"`
}

type Interaction struct {
	ID                     string            `json:"interaction_id" binding:"required"`
	ConversationID         string            `json:"conversation_id" binding:"required"`
	Ordinal                uint64            `json:"ordinal" binding:"required"`
	ExecutionStatus        InteractionStatus `json:"execution_status" binding:"required"`
	EvidenceStatus         EvidenceStatus    `json:"evidence_status" binding:"required"`
	StartIdempotencyKey    string            `json:"-"`
	TerminalIdempotencyKey string            `json:"-"`
	TerminalPayloadHash    string            `json:"-"`
	ClosureManifest        *ClosureManifest  `json:"closure_manifest,omitempty"`
	LeaseToken             string            `json:"lease_token" binding:"required"`
	LeaseEpoch             uint64            `json:"lease_epoch" binding:"required"`
	LeaseVersion           uint64            `json:"lease_version" binding:"required"`
	LeaseExpiresAt         time.Time         `json:"lease_expires_at" binding:"required"`
	RowVersion             uint64            `json:"row_version" binding:"required"`
	CreatedAt              time.Time         `json:"created_at" binding:"required"`
	UpdatedAt              time.Time         `json:"updated_at" binding:"required"`
	TerminalAt             *time.Time        `json:"terminal_at,omitempty"`
}

func (i Interaction) IsTerminal() bool {
	return i.ExecutionStatus != InteractionActive
}
