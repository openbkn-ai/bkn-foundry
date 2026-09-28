package operationaudit

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type KafkaPublisher interface {
	TryPublish([]byte) auditpublisher.Disposition
}

// KafkaRecorder has no local database or replay fallback. The business
// middleware ignores its error, while the coverage gap is observable in logs.
type KafkaRecorder struct {
	publisher   KafkaPublisher
	environment string
}

func NewKafkaRecorder(publisher KafkaPublisher, environment string) *KafkaRecorder {
	return &KafkaRecorder{publisher: publisher, environment: environment}
}

func (recorder *KafkaRecorder) Record(_ context.Context, entry Entry) error {
	if recorder == nil || recorder.publisher == nil {
		log.Print("execution_factory_audit_coverage_gap reason=publisher_unavailable")
		return errors.New("execution factory Audit publisher is unavailable")
	}
	value, err := BuildKafkaRecord(entry, recorder.environment)
	if err != nil {
		log.Printf("execution_factory_audit_coverage_gap reason=invalid_record error_type=%T", err)
		return err
	}
	if disposition := recorder.publisher.TryPublish(value); disposition != auditpublisher.Accepted {
		log.Printf("execution_factory_audit_coverage_gap reason=%s", disposition)
		return fmt.Errorf("execution factory Audit disposition: %s", disposition)
	}
	return nil
}
