package bkntrace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

var (
	evidencePublisherMu sync.RWMutex
	evidencePublisher   EvidencePublisher
)

type EvidencePublisher interface {
	TryPublish(evidencepublisher.Event) evidencepublisher.PublishResult
}

func SetEvidencePublisher(publisher EvidencePublisher) {
	evidencePublisherMu.Lock()
	defer evidencePublisherMu.Unlock()
	evidencePublisher = publisher
}
func currentEvidencePublisher() EvidencePublisher {
	evidencePublisherMu.RLock()
	defer evidencePublisherMu.RUnlock()
	return evidencePublisher
}

func publishEvidenceEvent(event Event, ec eventContext) evidencepublisher.PublishResult {
	return publishEvidenceEventWithPrevious(event, ec, nil)
}

func publishEvidenceEventWithPrevious(event Event, ec eventContext, previous *evidencepublisher.PublishResult) evidencepublisher.PublishResult {
	publisher := currentEvidencePublisher()
	if publisher == nil {
		return evidencepublisher.PublishResult{Disposition: evidencepublisher.Dropped, Reason: evidencepublisher.ReasonPublisherUnavailable}
	}
	// The Kafka Ledger requires the owner inside the envelope. Derive it from
	// trusted request context rather than accepting identity from event payload.
	// Size the copy by len(event) only: the "+1" for owner is not worth an overflowing size hint,
	// and the map grows for the extra key on its own.
	envelopeEvent := make(Event, len(event))
	for key, value := range event {
		envelopeEvent[key] = value
	}
	envelopeEvent["owner"] = map[string]string{
		"application_principal_id": ec.applicationID,
		"effective_subject_type":   ec.subjectType,
		"effective_subject_id":     ec.accountID,
	}
	envelope, err := json.Marshal(envelopeEvent)
	if err != nil {
		return evidencepublisher.PublishResult{Disposition: evidencepublisher.Dropped, Reason: evidencepublisher.ReasonSerialization}
	}
	get := func(key string) string { value, _ := event[key].(string); return value }
	observedAt := get("observed_at")
	if observedAt == "" {
		observedAt = ec.observedAt
	}
	startedAt := get("started_at")
	if startedAt == "" {
		startedAt = observedAt
	}
	emittedAt := get("emitted_at")
	if emittedAt == "" {
		emittedAt = observedAt
	}
	conversationID := get("conversation_id")
	if conversationID == "" {
		conversationID = ec.conversationID
	}
	attempt, _ := event["attempt"].(int)
	refs := trace30BusinessRefs(event, ec.businessRefs)
	var refsJSON, edgesJSON json.RawMessage
	if len(refs) > 0 {
		refsJSON, err = json.Marshal(refs)
		if err != nil {
			return evidencepublisher.PublishResult{Disposition: evidencepublisher.Dropped, Reason: evidencepublisher.ReasonSerialization}
		}
		// Interaction artifacts have no Operation. Do not manufacture an edge
		// with an empty operation identity for those events.
		if operationID := get("operation_id"); operationID != "" {
			edges := make([]trace30OperationEdge, 0, len(refs))
			for _, ref := range refs {
				edges = append(edges, trace30OperationEdge{OperationID: operationID, BusinessRef: ref, Role: "read", ObservedAt: observedAt})
			}
			edgesJSON, err = json.Marshal(edges)
			if err != nil {
				return evidencepublisher.PublishResult{Disposition: evidencepublisher.Dropped, Reason: evidencepublisher.ReasonSerialization}
			}
		}
	}
	var causes []string
	if cause := get("causation_event_id"); cause != "" {
		causes = []string{cause}
	}
	return publisher.TryPublish(evidencepublisher.Event{
		EventID: get("event_id"), EventType: get("event_type"), ConversationID: conversationID,
		InteractionID: get("interaction_id"), OperationID: get("operation_id"), Attempt: attempt,
		RequestID: get("bkn.request.id"), TraceID: get("trace_id"), SpanID: get("span_id"),
		StartedAt: startedAt, ObservedAt: observedAt, EmittedAt: emittedAt, Envelope: envelope,
		BusinessRefs: refsJSON, OperationBusinessEdges: edgesJSON, CausationEventIDs: causes,
		PreviousResult: previous,
	})
}

func FlushEvidencePublisher(ctx context.Context) evidencepublisher.DrainResult {
	if publisher, ok := currentEvidencePublisher().(interface {
		Flush(context.Context) evidencepublisher.DrainResult
	}); ok {
		return publisher.Flush(ctx)
	}
	return evidencepublisher.DrainResult{}
}

// CaptureDisabled reads the runtime's verified local policy. Legacy publishers
// without policy state retain their existing behavior.
func CaptureDisabled() bool {
	publisher, ok := currentEvidencePublisher().(interface{ CaptureDisabled() bool })
	return ok && publisher.CaptureDisabled()
}

func IsCaptureDisabledError(apiErr *APIError) bool {
	return apiErr != nil && apiErr.Code == "capture_disabled"
}

func IsCaptureDisabledEvidenceError(err error) bool {
	var coreErr *CoreHTTPError
	return errors.As(err, &coreErr) && strings.EqualFold(coreErr.Code, "capture_disabled")
}
