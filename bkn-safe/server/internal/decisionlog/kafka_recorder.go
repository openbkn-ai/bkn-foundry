package decisionlog

import (
	"log/slog"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type Recorder interface {
	Record(Entry)
}

type KafkaPublisher interface {
	TryPublish([]byte) auditpublisher.Disposition
}

// KafkaRecorder uses the existing bounded Safe Audit publisher; authorization
// never waits for Kafka and does not fall back to writing the historical table.
type KafkaRecorder struct {
	publisher   KafkaPublisher
	environment string
	observe     func(string)
}

func NewKafkaRecorder(publisher KafkaPublisher, environment string, observe func(string)) *KafkaRecorder {
	return &KafkaRecorder{publisher: publisher, environment: environment, observe: observe}
}

func (r *KafkaRecorder) Record(entry Entry) {
	if r == nil || r.publisher == nil {
		r.recordOutcome("dropped_unavailable")
		slog.Error("safe security coverage gap", "reason", "publisher_unavailable")
		return
	}
	value, err := BuildKafkaRecord(entry, r.environment)
	if err != nil {
		r.recordOutcome("dropped_invalid")
		slog.Error("safe security coverage gap", "reason", "record_invalid", "error", err)
		return
	}
	disposition := r.publisher.TryPublish(value)
	r.recordOutcome(string(disposition))
	if disposition != auditpublisher.Accepted {
		slog.Error("safe security coverage gap", "reason", disposition)
	}
}

func (r *KafkaRecorder) recordOutcome(outcome string) {
	if r != nil && r.observe != nil {
		r.observe(outcome)
	}
}
