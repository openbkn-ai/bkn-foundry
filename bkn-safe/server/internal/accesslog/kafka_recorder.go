package accesslog

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

// KafkaRecorder publishes existing Safe access facts without a local write or
// replay fallback. Callers log returned coverage gaps but never fail login or
// logout solely because the audit transport is unavailable.
type KafkaRecorder struct {
	publisher   KafkaPublisher
	environment string
	observe     func(string)
}

func NewKafkaRecorder(publisher KafkaPublisher, environment string, observe ...func(string)) *KafkaRecorder {
	recorder := &KafkaRecorder{publisher: publisher, environment: environment}
	if len(observe) > 0 {
		recorder.observe = observe[0]
	}
	return recorder
}

func (r *KafkaRecorder) Record(_ context.Context, entry Entry) error {
	if r == nil || r.publisher == nil {
		r.recordOutcome("dropped_unavailable")
		slog.Error("safe access coverage gap", "reason", "publisher_unavailable")
		return errors.New("safe access publisher is unavailable")
	}
	value, err := BuildKafkaRecord(entry, r.environment)
	if err == nil {
		disposition := r.publisher.TryPublish(value)
		if disposition != auditpublisher.Accepted {
			r.recordOutcome(string(disposition))
			err = fmt.Errorf("safe access audit disposition: %s", disposition)
		} else {
			r.recordOutcome("accepted")
		}
	} else {
		r.recordOutcome("dropped_invalid")
	}
	if err != nil {
		slog.Error("safe access coverage gap", "action", entry.Action, "outcome", entry.Outcome, "error", err)
	}
	return err
}

func (r *KafkaRecorder) recordOutcome(outcome string) {
	if r != nil && r.observe != nil {
		r.observe(outcome)
	}
}
