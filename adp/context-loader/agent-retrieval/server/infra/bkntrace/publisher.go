package bkntrace

import (
	"context"
	"encoding/json"
	"sync"

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

func publishEvidenceEvent(event Event) evidencepublisher.PublishResult {
	publisher := currentEvidencePublisher()
	if publisher == nil {
		return evidencepublisher.PublishResult{Disposition: evidencepublisher.Dropped, Reason: evidencepublisher.ReasonPublisherClosing}
	}
	envelope, err := json.Marshal(event)
	if err != nil {
		return evidencepublisher.PublishResult{Disposition: evidencepublisher.Dropped, Reason: evidencepublisher.ReasonSerialization}
	}
	get := func(key string) string { value, _ := event[key].(string); return value }
	attempt, _ := event["attempt"].(int)
	return publisher.TryPublish(evidencepublisher.Event{EventID: get("event_id"), EventType: get("event_type"), ConversationID: get("conversation_id"), InteractionID: get("interaction_id"), OperationID: get("operation_id"), Attempt: attempt, RequestID: get("bkn.request.id"), TraceID: get("trace_id"), SpanID: get("span_id"), ObservedAt: get("observed_at"), EmittedAt: get("emitted_at"), Envelope: envelope})
}

func FlushEvidencePublisher(ctx context.Context) evidencepublisher.DrainResult {
	if publisher := currentEvidencePublisher(); publisher != nil {
		return publisher.Flush(ctx)
	}
	return evidencepublisher.DrainResult{}
}
func CloseEvidencePublisher(ctx context.Context) evidencepublisher.DrainResult {
	if publisher := currentEvidencePublisher(); publisher != nil {
		return publisher.Close(ctx)
	}
	return evidencepublisher.DrainResult{}
}
