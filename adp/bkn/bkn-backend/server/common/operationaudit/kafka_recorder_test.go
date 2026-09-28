// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type captureKafkaAuditPublisher struct {
	values      [][]byte
	disposition auditpublisher.Disposition
}

func (p *captureKafkaAuditPublisher) TryPublish(value []byte) auditpublisher.Disposition {
	p.values = append(p.values, append([]byte(nil), value...))
	return p.disposition
}

func TestKafkaRecorderPublishesOneBoundedRecord(t *testing.T) {
	publisher := &captureKafkaAuditPublisher{disposition: auditpublisher.Accepted}
	recorder := NewKafkaRecorder(publisher, "test")
	entry := Entry{EventID: "evt-1", EventTime: time.Now().UTC(), KnowledgeNetworkID: "kn-1",
		ActorID: "user-a", ActorName: "User A", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-1", SourceChannel: "api", Method: "PUT", HTTPStatus: 200, Action: "update",
		TargetType: "knowledge_network", TargetID: "kn-1", Outcome: "success",
		ChangeSummary: map[string]any{"changed_fields": []string{"name"}},
	}
	if err := recorder.Record(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if len(publisher.values) != 1 {
		t.Fatalf("published %d records, want one", len(publisher.values))
	}
	if _, err := auditpublisher.BuildRecord(publisher.values[0]); err != nil {
		t.Fatalf("publisher received invalid Kafka record: %v", err)
	}
}

func TestKafkaRecorderReportsQueueDropWithoutBlockingBusiness(t *testing.T) {
	publisher := &captureKafkaAuditPublisher{disposition: auditpublisher.DroppedQueueFull}
	recorder := NewKafkaRecorder(publisher, "test")
	entry := Entry{EventID: "evt-1", EventTime: time.Now().UTC(), KnowledgeNetworkID: "kn-1",
		ActorID: "user-a", ActorName: "User A", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-1", SourceChannel: "api", Method: "PUT", HTTPStatus: 200, Action: "update",
		TargetType: "knowledge_network", TargetID: "kn-1", Outcome: "success",
		ChangeSummary: map[string]any{"changed_fields": []string{}},
	}
	if err := recorder.Record(context.Background(), entry); err == nil {
		t.Fatal("queue drop must be reported to middleware coverage-gap logging")
	}
}
