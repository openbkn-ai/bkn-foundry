// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturecontrollersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditvalidator"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type captureAuditTestSender struct{ records chan auditpublisher.Record }

func (s captureAuditTestSender) Send(_ context.Context, record auditpublisher.Record) error {
	s.records <- record
	return nil
}

func TestCaptureRollbackFailureUsesRollbackAction(t *testing.T) {
	records := make(chan auditpublisher.Record, 1)
	publisher, err := auditpublisher.New(captureAuditTestSender{records: records}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	sink := &captureAuditSink{publisher: publisher, environment: "test"}
	sink.terminal(capturecontrollersvc.TerminalEvent{
		OperationID: "trace-op-rollback", PolicyRevision: 23,
		DesiredState: "enabled", EffectiveState: "disabled", Phase: "rollback_failed",
		FailureCode: "TRACE_EVIDENCE_ROLLBACK_FAILED", At: time.Now().UTC(),
	})
	select {
	case record := <-records:
		var event struct {
			EventName string `json:"event_name"`
			Facts     struct {
				Action string `json:"action"`
			} `json:"facts"`
		}
		if err := json.Unmarshal(record.Value, &event); err != nil {
			t.Fatal(err)
		}
		if event.EventName != "trace_evidence.operation_failed" || event.Facts.Action != "rollback" {
			t.Fatalf("rollback failure audit shape: %#v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rollback failure Audit record not delivered")
	}
}

func TestCaptureControlAuditRecordsAdmitAllFrozenEvents(t *testing.T) {
	validator, err := auditvalidator.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, phase, action, outcome string
	}{
		{"trace_evidence.configuration_change_requested", "disabling", "update", "success"},
		{"trace_evidence.operation_succeeded", "succeeded", "apply", "success"},
		{"trace_evidence.operation_failed", "failed", "apply", "failure"},
		{"trace_evidence.rollback_completed", "rollback_completed", "rollback", "success"},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
			value, err := buildCaptureControlAudit(captureAuditInput{
				EventName: test.name, Phase: test.phase, Action: test.action, Outcome: test.outcome,
				OperationID: "trace-op-123", PolicyRevision: 20, DesiredState: "disabled", EffectiveState: "enabled",
				ActorID: "user-1", ActorType: "user", Environment: "test", OccurredAt: now,
				BeforeHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				AfterHash:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			})
			if err != nil {
				t.Fatal(err)
			}
			record, err := auditpublisher.BuildRecord(value)
			if err != nil {
				t.Fatal(err)
			}
			_, err = validator.Validate(context.Background(), auditconsumer.Record{
				Topic: record.Topic, Key: record.Key, Value: record.Value,
				Headers:    []auditconsumer.Header{{Key: record.Headers[0].Key, Value: record.Headers[0].Value}},
				BrokerTime: now,
			})
			if err != nil {
				t.Fatalf("%s rejected by Audit Consumer: %v", test.name, err)
			}
		})
	}
}
