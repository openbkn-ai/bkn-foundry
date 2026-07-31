package ledgervo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

type Event struct {
	EventID           string                  `json:"event_id"`
	EventType         string                  `json:"event_type"`
	SchemaVersion     string                  `json:"schema_version"`
	PayloadHash       string                  `json:"payload_hash"`
	Owner             sessionvo.Owner         `json:"owner"`
	ConversationID    string                  `json:"conversation_id"`
	InteractionID     string                  `json:"interaction_id"`
	OperationID       string                  `json:"operation_id,omitempty"`
	Attempt           uint32                  `json:"attempt,omitempty"`
	RequestID         string                  `json:"request_id,omitempty"`
	TraceID           string                  `json:"trace_id,omitempty"`
	SpanID            string                  `json:"span_id,omitempty"`
	ProducerID        string                  `json:"producer_id"`
	ProducerStreamID  string                  `json:"producer_stream_id"`
	ProducerEpoch     uint64                  `json:"producer_epoch"`
	ProducerSequence  uint64                  `json:"producer_sequence"`
	CausationEventIDs []string                `json:"causation_event_ids,omitempty"`
	CausalityStatus   string                  `json:"causality_status,omitempty"`
	MissingCauseIDs   []string                `json:"missing_causation_event_ids,omitempty"`
	StartedAt         time.Time               `json:"started_at"`
	ObservedAt        time.Time               `json:"observed_at"`
	EmittedAt         time.Time               `json:"emitted_at"`
	Envelope          json.RawMessage         `json:"envelope"`
	BusinessRefs      []sessionvo.BusinessRef `json:"business_refs,omitempty"`
	ArtifactRefs      []string                `json:"artifact_refs,omitempty"`
}

type DurableAck struct {
	EventID        string    `json:"event_id"`
	Durable        bool      `json:"durable_ack"`
	Replayed       bool      `json:"replayed"`
	IngestSequence uint64    `json:"ingest_sequence"`
	IngestedAt     time.Time `json:"ingested_at"`
}

func CanonicalPayloadHash(payload json.RawMessage) string {
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err == nil {
		if canonical, err := json.Marshal(decoded); err == nil {
			payload = canonical
		}
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func ImmutableRecordHash(event Event) string {
	event.CausalityStatus = ""
	event.MissingCauseIDs = nil
	var decoded any
	if json.Unmarshal(event.Envelope, &decoded) == nil {
		event.Envelope, _ = json.Marshal(decoded)
	}
	data, _ := json.Marshal(event)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
