package evidencepublisher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	Topic = "openbkn.evidence.v1"

	Accepted Disposition = "accepted"
	Dropped  Disposition = "dropped"

	ReasonInvalidEvent         = "invalid_event"
	ReasonSerialization        = "serialization_failed"
	ReasonMessageTooLarge      = "message_too_large"
	ReasonQueueFull            = "queue_full"
	ReasonPublisherClosing     = "publisher_closing"
	ReasonPublisherUnavailable = "publisher_unavailable"
	ReasonRetryExhausted       = "retry_exhausted"
	ReasonShutdownTimeout      = "shutdown_timeout"
)

type Disposition string

type Header struct {
	Key   string
	Value string
}

type Record struct {
	Key     string
	Value   []byte
	Headers []Header
}

func (r Record) Header(key string) string {
	for _, header := range r.Headers {
		if header.Key == key {
			return header.Value
		}
	}
	return ""
}

type Event struct {
	EventID        string
	EventType      string
	SchemaVersion  string
	ConversationID string
	InteractionID  string
	OperationID    string
	Attempt        int
	RequestID      string
	TraceID        string
	SpanID         string
	StartedAt      string
	ObservedAt     string
	EmittedAt      string
	Envelope       json.RawMessage
}

type PublishResult struct {
	EventID     string
	Disposition Disposition
	Reason      string
}

type DrainResult struct {
	ProducerInstanceID    string
	CapturePolicyRevision string
	LastAcceptedSequence  uint64
	Published             uint64
	Dropped               uint64
	QueueEmpty            bool
}

func canonicalPayloadHash(payload json.RawMessage) (string, error) {
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
