package operationaudit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type auditPublisherCapture struct {
	values      [][]byte
	disposition auditpublisher.Disposition
}

func (capture *auditPublisherCapture) TryPublish(value []byte) auditpublisher.Disposition {
	capture.values = append(capture.values, value)
	return capture.disposition
}

func TestKafkaRecorderUsesIndependentAuditPublisher(t *testing.T) {
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		EventID: id.String(), EventTime: time.Now().UTC(), ActorID: "user-1", ActorName: "Operator",
		ActorType: "user", AuthMethod: "oauth", RequestID: "req-1", Method: "POST",
		HTTPStatus: 201, Action: "create", TargetType: "operator", TargetID: "op-1",
		Outcome: "success",
	}
	publisher := &auditPublisherCapture{disposition: auditpublisher.Accepted}
	recorder := NewKafkaRecorder(publisher, "test")
	if err := recorder.Record(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if len(publisher.values) != 1 {
		t.Fatalf("published %d records", len(publisher.values))
	}
	publisher.disposition = auditpublisher.DroppedQueueFull
	if err := recorder.Record(context.Background(), entry); err == nil {
		t.Fatal("dropped Audit should report a coverage gap")
	}
}
