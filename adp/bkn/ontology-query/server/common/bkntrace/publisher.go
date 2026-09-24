package bkntrace

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

var (
	evidencePublisherMu sync.RWMutex
	evidencePublisher   *evidencepublisher.Publisher
)

func SetEvidencePublisher(publisher *evidencepublisher.Publisher) {
	evidencePublisherMu.Lock()
	defer evidencePublisherMu.Unlock()
	evidencePublisher = publisher
}

func currentEvidencePublisher() *evidencepublisher.Publisher {
	evidencePublisherMu.RLock()
	defer evidencePublisherMu.RUnlock()
	return evidencePublisher
}

func EvidenceEnabled() bool { return currentEvidencePublisher() != nil }

func publishEvidenceEvent(_ context.Context, event Event) evidencepublisher.PublishResult {
	data, err := json.Marshal(event)
	if err != nil {
		return evidencepublisher.PublishResult{Disposition: evidencepublisher.Dropped, Reason: evidencepublisher.ReasonSerialization}
	}
	publisher := currentEvidencePublisher()
	if publisher == nil {
		return evidencepublisher.PublishResult{Disposition: evidencepublisher.Dropped, Reason: evidencepublisher.ReasonPublisherClosing}
	}
	get := func(key string) string { value, _ := event[key].(string); return value }
	attempt, _ := event["attempt"].(int)
	if attempt == 0 {
		if value, ok := event["attempt"].(uint32); ok {
			attempt = int(value)
		}
	}
	parseTime := func(key string) string {
		value := get(key)
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			return ""
		}
		return value
	}
	envelope := data
	if raw, ok := event["envelope"].(json.RawMessage); ok {
		envelope = raw
	}
	return publisher.TryPublish(evidencepublisher.Event{EventID: get("event_id"), EventType: get("event_type"), ConversationID: get("conversation_id"), InteractionID: get("interaction_id"),
		OperationID: get("operation_id"), Attempt: attempt, RequestID: get("request_id"), TraceID: get("trace_id"), SpanID: get("span_id"),
		StartedAt: parseTime("started_at"), ObservedAt: parseTime("observed_at"), EmittedAt: parseTime("emitted_at"), Envelope: envelope})
}
