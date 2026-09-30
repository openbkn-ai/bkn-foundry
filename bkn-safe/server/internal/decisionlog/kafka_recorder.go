package decisionlog

import (
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type Recorder interface {
	Record(Entry)
}

type KafkaPublisher interface {
	TryPublish([]byte) auditpublisher.Disposition
}

// KafkaRecorder is retained as a compatibility boundary for authorization
// callers. Authorization checks are internal reads, not business operations,
// so they never enter the Audit stream.
type KafkaRecorder struct {
}

func NewKafkaRecorder(publisher KafkaPublisher, environment string, observe func(string)) *KafkaRecorder {
	// Keep the constructor signature while callers migrate away from the former
	// decision publisher. Authorization checks never belong to the business log.
	_ = publisher
	_ = environment
	_ = observe
	return &KafkaRecorder{}
}

func (*KafkaRecorder) Record(Entry) {}
