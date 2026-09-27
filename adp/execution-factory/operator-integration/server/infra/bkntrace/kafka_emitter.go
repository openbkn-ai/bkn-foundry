package bkntrace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

type actionEvidencePublisher interface {
	TryPublish(evidencepublisher.Event) evidencepublisher.PublishResult
}

type KafkaEmitter struct{ publisher actionEvidencePublisher }

var (
	runtimeOnce   sync.Once
	runtimeValue  *EvidencePublisherRuntime
	runtimeStop   chan struct{}
	runtimeDone   chan struct{}
	runtimeCancel context.CancelFunc
	runtimeClose  sync.Once
)

func NewKafkaEmitter(publisher actionEvidencePublisher) *KafkaEmitter {
	return &KafkaEmitter{publisher: publisher}
}

type EvidenceDropReporter func(evidencepublisher.DrainResult)

func NewConfiguredKafkaEmitter(logger interfaces.Logger) *KafkaEmitter {
	runtimeOnce.Do(func() {
		var err error
		runtimeValue, err = NewEvidencePublisherRuntime()
		if err != nil {
			return
		}
		runtimeStop = make(chan struct{})
		runtimeDone = make(chan struct{})
		policyCtx, cancel := context.WithCancel(context.Background())
		runtimeCancel = cancel
		go func() { _ = runtimeValue.Runtime.Run(policyCtx) }()
		go runEvidenceFlushLoop(runtimeStop, runtimeDone, 250*time.Millisecond, runtimeValue.Runtime.Flush, evidenceDropReporter(logger))
	})
	if runtimeValue == nil {
		return NewKafkaEmitter(nil)
	}
	return NewKafkaEmitter(runtimeValue.Runtime)
}

func CloseEvidencePublisher(ctx context.Context) error {
	if runtimeValue == nil {
		return nil
	}
	var closeErr error
	runtimeClose.Do(func() {
		if runtimeCancel != nil {
			runtimeCancel()
		}
		if runtimeStop != nil {
			close(runtimeStop)
			<-runtimeDone
		}
		_, closeErr = runtimeValue.Runtime.Close(ctx)
		if runtimeValue.Producer != nil {
			_ = runtimeValue.Producer.Close()
		}
	})
	return closeErr
}

func runEvidenceFlushLoop(stop <-chan struct{}, done chan<- struct{}, interval time.Duration, flush func(context.Context) evidencepublisher.DrainResult, report EvidenceDropReporter) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			result := flush(ctx)
			cancel()
			reportEvidenceDrops(result, report)
		case <-stop:
			return
		}
	}
}

func reportEvidenceDrops(result evidencepublisher.DrainResult, report EvidenceDropReporter) {
	if result.Dropped > 0 && report != nil {
		report(result)
	}
}

func evidenceDropReporter(logger interfaces.Logger) EvidenceDropReporter {
	if logger == nil {
		return nil
	}
	return func(result evidencepublisher.DrainResult) {
		logger.Errorf("bkn_trace_coverage_gap workload_identity=%q producer_id=%q producer_instance_id=%q dropped=%d last_accepted_sequence=%d capture_policy_revision=%q",
			os.Getenv("BKN_TRACE_WORKLOAD_IDENTITY"), os.Getenv("BKN_TRACE_PRODUCER_ID"), result.ProducerInstanceID,
			result.Dropped, result.LastAcceptedSequence, result.CapturePolicyRevision)
	}
}

func (e *KafkaEmitter) Emit(_ context.Context, action Action, events []Event) error {
	if e == nil || e.publisher == nil {
		return evidencepublisherErr
	}
	if action.conversationID == "" {
		return errors.New("bkn trace evidence admission dropped: missing trusted conversation_id")
	}
	var publishErr error
	for _, event := range events {
		envelope, err := json.Marshal(map[string]any{
			"bkn.trace.schema.version": event.SchemaVersion,
			"trace":                    map[string]any{"trace_id": action.traceID, "traceparent": action.traceparent, "bkn.request.id": action.requestID, "bkn.account.id": action.accountID, "bkn.account.type": action.accountType},
			"event":                    event,
		})
		if err != nil {
			if publishErr == nil {
				publishErr = evidencepublisherErr
			}
			continue
		}
		observed := event.ObservedAt
		if _, err := time.Parse(time.RFC3339Nano, observed); err != nil {
			observed = ""
		}
		started := action.observedAt
		if _, err := time.Parse(time.RFC3339Nano, started); err != nil {
			started = ""
		}
		result := e.publisher.TryPublish(evidencepublisher.Event{
			EventID: event.EventID, EventType: event.EventType, SchemaVersion: event.SchemaVersion,
			ConversationID: action.conversationID, InteractionID: event.InteractionID,
			OperationID: event.OperationID, Attempt: event.Attempt, RequestID: event.RequestID,
			TraceID: event.TraceID, SpanID: event.SpanID, StartedAt: started,
			ObservedAt: observed, EmittedAt: event.EmittedAt, Envelope: envelope,
		})
		if result.Disposition == evidencepublisher.Dropped && publishErr == nil {
			publishErr = errors.New("bkn trace evidence admission dropped: " + result.Reason)
		}
	}
	return publishErr
}

var evidencepublisherErr = errors.New("bkn trace evidence publisher unavailable")
