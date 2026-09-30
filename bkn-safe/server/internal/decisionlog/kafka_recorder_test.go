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

func TestKafkaRecorderDoesNotPublishAuthorizationDecisions(t *testing.T) {
	entry := Entry{VerifiedActorID: "admin-1", ResourceType: "safe_admin", ResourceID: "console", Decision: DecisionDeny, Method: "GET", Source: "admin"}
	publisher := &testPublisher{disposition: auditpublisher.Accepted}
	var outcomes []string
	recorder := NewKafkaRecorder(publisher, "test", func(result string) { outcomes = append(outcomes, result) })
	recorder.Record(entry)
	if publisher.value != nil || len(outcomes) != 0 {
		t.Fatalf("authorization decisions must not enter the business log stream: value=%q outcomes=%v", publisher.value, outcomes)
	}
}
