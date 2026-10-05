package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type KafkaPublisher interface {
	TryPublish([]byte) auditpublisher.Disposition
}

type coverageGapLoggedError struct{ err error }

func (e *coverageGapLoggedError) Error() string { return e.err.Error() }
func (e *coverageGapLoggedError) Unwrap() error { return e.err }

// CoverageGapWasLogged reports whether the component returning err already
// emitted the structured service log for this failed audit fact. Callers use
// it to preserve observability without producing duplicate outage logs.
func CoverageGapWasLogged(err error) bool {
	var logged *coverageGapLoggedError
	return errors.As(err, &logged)
}

func markCoverageGapLogged(err error) error {
	if err == nil || CoverageGapWasLogged(err) {
		return err
	}
	return &coverageGapLoggedError{err: err}
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
		for _, entry := range entries {
			logKafkaCoverageGap(entry, "publish", "publisher_unavailable", errors.New("safe audit publisher is unavailable"))
		}
		return markCoverageGapLogged(errors.New("safe audit publisher is unavailable"))
	}
	var first error
	for _, entry := range entries {
		value, err := BuildKafkaAdminRecord(entry, r.environment)
		reason, stage := "", ""
		if err == nil {
			if disposition := r.publisher.TryPublish(value); disposition != auditpublisher.Accepted {
				r.telemetry.Observe(string(disposition))
				err = fmt.Errorf("safe audit disposition: %s", disposition)
				reason, stage = "publish_rejected", "publish"
			} else {
				r.telemetry.Observe("accepted")
			}
		} else {
			r.telemetry.Observe("dropped_invalid")
			reason, stage = kafkaBuildFailureReason(err), "build"
		}
		if err != nil {
			logKafkaCoverageGap(entry, stage, reason, err)
			if first == nil {
				first = err
			}
		}
	}
	return markCoverageGapLogged(first)
}

func kafkaBuildFailureReason(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "actor snapshot"):
		return "actor_snapshot_unavailable"
	case strings.Contains(message, "target snapshot"):
		return "target_snapshot_unavailable"
	case strings.Contains(message, "json"), strings.Contains(message, "canonical"), strings.Contains(message, "bytes"):
		return "serialization_failed"
	default:
		return "fact_validation_failed"
	}
}

func logKafkaCoverageGap(entry Entry, stage, reason string, err error) {
	attributes := []any{
		"request_id", entry.RequestID,
		"resource", entry.Resource,
		"action", entry.Action,
		"stage", stage,
		"reason", reason,
	}
	if entry.TargetID != "" {
		attributes = append(attributes, "target_id", entry.TargetID)
	}
	if err != nil {
		attributes = append(attributes, "error", err)
	}
	slog.Error("safe audit coverage gap", attributes...)
}
