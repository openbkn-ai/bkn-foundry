package bkntrace

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

type KafkaEmitter struct{ publisher *evidencepublisher.Publisher }

var (
	runtimeOnce  sync.Once
	runtimeValue *EvidencePublisherRuntime
	runtimeStop  chan struct{}
)

func NewKafkaEmitter(publisher *evidencepublisher.Publisher) *KafkaEmitter {
	return &KafkaEmitter{publisher: publisher}
}

func NewConfiguredKafkaEmitter() *KafkaEmitter {
	runtimeOnce.Do(func() {
		var err error
		runtimeValue, err = NewEvidencePublisherRuntime()
		if err != nil {
			return
		}
		runtimeStop = make(chan struct{})
		go func() {
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_ = runtimeValue.Publisher.Flush(ctx)
					cancel()
				case <-runtimeStop:
					return
				}
			}
		}()
	})
	if runtimeValue == nil {
		return NewKafkaEmitter(nil)
	}
	return NewKafkaEmitter(runtimeValue.Publisher)
}

func CloseEvidencePublisher(ctx context.Context) {
	if runtimeValue == nil {
		return
	}
	if runtimeStop != nil {
		close(runtimeStop)
		runtimeStop = nil
	}
	_ = runtimeValue.Publisher.Close(ctx)
	if runtimeValue.Producer != nil {
		_ = runtimeValue.Producer.Close()
	}
}

func (e *KafkaEmitter) Emit(_ context.Context, action Action, events []Event) error {
	if e == nil || e.publisher == nil {
		return evidencepublisherErr
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
