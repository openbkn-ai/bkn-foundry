package decisionlog

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type testPublisher struct {
	value       []byte
	disposition auditpublisher.Disposition
}

func (p *testPublisher) TryPublish(value []byte) auditpublisher.Disposition {
	p.value = value
	return p.disposition
}

func TestKafkaRecorderPublishesOnceAndReportsGap(t *testing.T) {
	entry := Entry{VerifiedActorID: "admin-1", ResourceType: "safe_admin", ResourceID: "console", Decision: DecisionDeny, Method: "GET", Source: "admin"}
	publisher := &testPublisher{disposition: auditpublisher.Accepted}
	var outcomes []string
	recorder := NewKafkaRecorder(publisher, "test", func(result string) { outcomes = append(outcomes, result) })
	recorder.Record(entry)
	if len(publisher.value) == 0 || len(outcomes) != 1 || outcomes[0] != "accepted" {
		t.Fatalf("publish = %d bytes, outcomes = %v", len(publisher.value), outcomes)
	}
	publisher.disposition = auditpublisher.DroppedQueueFull
	recorder.Record(entry)
	if len(outcomes) != 2 || outcomes[1] != string(auditpublisher.DroppedQueueFull) {
		t.Fatalf("gap not counted: %v", outcomes)
	}
}

func TestKafkaRecorderSkipsDecisionWithoutVerifiedActor(t *testing.T) {
	publisher := &testPublisher{disposition: auditpublisher.Accepted}
	var outcomes []string
	recorder := NewKafkaRecorder(publisher, "test", func(result string) { outcomes = append(outcomes, result) })
	recorder.Record(Entry{
		AccessorID: "evaluated-user", ResourceType: "safe_admin", ResourceID: "console",
		Decision: DecisionAllow, Source: "check", Method: "POST",
	})
	if publisher.value != nil {
		t.Fatalf("decision without a verified actor must not be published: %s", publisher.value)
	}
	if len(outcomes) != 0 {
		t.Fatalf("decision without a verified actor must not emit a publish outcome: %v", outcomes)
	}
}

func TestKafkaRecorderSkipsUnsafeVerifiedActor(t *testing.T) {
	publisher := &testPublisher{disposition: auditpublisher.Accepted}
	recorder := NewKafkaRecorder(publisher, "test", nil)
	recorder.Record(Entry{
		VerifiedActorID: "Bearer abcdefghijklmnop", AccessorID: "Bearer abcdefghijklmnop",
		ResourceType: "safe_admin", ResourceID: "console", Decision: DecisionAllow, Source: "check", Method: "POST",
	})
	if publisher.value != nil {
		t.Fatalf("unsafe actor must not enter the audit stream: %s", publisher.value)
	}
}
