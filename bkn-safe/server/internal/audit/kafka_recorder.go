package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type KafkaPublisher interface {
	TryPublish([]byte) auditpublisher.Disposition
}

// KafkaRecorder publishes Safe admin facts without a local write or replay
// fallback. Delivery is bounded by auditpublisher and business calls fail open.
type KafkaRecorder struct {
	publisher   KafkaPublisher
	environment string
	telemetry   *PublishTelemetry
}

func NewKafkaRecorder(publisher KafkaPublisher, environment string, telemetry ...*PublishTelemetry) *KafkaRecorder {
	recorder := &KafkaRecorder{publisher: publisher, environment: environment}
	if len(telemetry) > 0 {
		recorder.telemetry = telemetry[0]
	}
	return recorder
}

func (r *KafkaRecorder) Record(ctx context.Context, entry Entry) error {
	return r.RecordBatch(ctx, []Entry{entry})
}

func (r *KafkaRecorder) RecordBatch(_ context.Context, entries []Entry) error {
	if r == nil || r.publisher == nil {
		if r != nil {
			r.telemetry.Observe("dropped_unavailable")
		}
		slog.Error("safe audit coverage gap", "reason", "publisher_unavailable")
		return errors.New("safe audit publisher is unavailable")
	}
	var first error
	for _, entry := range entries {
		value, err := BuildKafkaAdminRecord(entry, r.environment)
		if err == nil {
			if disposition := r.publisher.TryPublish(value); disposition != auditpublisher.Accepted {
				r.telemetry.Observe(string(disposition))
				err = fmt.Errorf("safe audit disposition: %s", disposition)
			} else {
				r.telemetry.Observe("accepted")
			}
		} else {
			r.telemetry.Observe("dropped_invalid")
		}
		if err != nil {
			slog.Error("safe audit coverage gap", "request_id", entry.RequestID, "resource", entry.Resource, "action", entry.Action, "error", err)
			if first == nil {
				first = err
			}
		}
	}
	return first
}
