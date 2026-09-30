// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturecontrollersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditvalidator"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type captureAuditFakeProducer struct{}

func (captureAuditFakeProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	return 0, 1, nil
}

func (captureAuditFakeProducer) Close() error { return nil }

func TestCaptureAuditSenderRetriesInitializationOnNextRecord(t *testing.T) {
	calls := 0
	sender := &captureAuditSender{
		brokers: []string{"broker:9092"}, config: sarama.NewConfig(),
		newProducer: func([]string, *sarama.Config) (captureAuditProducer, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("broker unavailable")
			}
			return captureAuditFakeProducer{}, nil
		},
	}
	record := auditpublisher.Record{Topic: auditpublisher.Topic}
	if err := sender.Send(context.Background(), record); err == nil {
		t.Fatal("first send must report broker initialization failure")
	}
	if err := sender.Send(context.Background(), record); err != nil {
		t.Fatalf("send after broker recovery: %v", err)
	}
	if calls != 2 {
		t.Fatalf("producer initialization attempts = %d, want 2", calls)
	}
	if err := sender.Close(); err != nil {
		t.Fatal(err)
	}
}

type captureAuditTestSender struct{ records chan auditpublisher.Record }

func (s captureAuditTestSender) Send(_ context.Context, record auditpublisher.Record) error {
	s.records <- record
	return nil
}

func TestCaptureRollbackFailureRespectsFrozenOperationFailedSchema(t *testing.T) {
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
		if event.EventName != "trace_evidence.operation_failed" || event.Facts.Action != "apply" {
			t.Fatalf("rollback failure audit shape: %#v", event)
		}
		validator, err := auditvalidator.New()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := validator.Validate(context.Background(), auditconsumer.Record{
			Topic: record.Topic, Key: record.Key, Value: record.Value,
			Headers:    []auditconsumer.Header{{Key: record.Headers[0].Key, Value: record.Headers[0].Value}},
			BrokerTime: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("rollback failure record rejected by frozen Audit schema: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rollback failure Audit record not delivered")
	}
}

func TestCaptureRequestedAuditMapsOAuthAppToServiceAccount(t *testing.T) {
	value, err := buildCaptureControlAudit(captureAuditInput{
		EventName: "trace_evidence.configuration_change_requested", Phase: "disabling",
		Action: "update", Outcome: "success", OperationID: "trace-op-app", PolicyRevision: 24,
		DesiredState: "disabled", EffectiveState: "enabled", ActorID: "client-app",
		ActorType: "app", Environment: "test", OccurredAt: time.Now().UTC(),
		BeforeHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AfterHash:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Actor struct {
			Type string `json:"type"`
		} `json:"actor"`
	}
	if err := json.Unmarshal(value, &event); err != nil {
		t.Fatal(err)
	}
	if event.Actor.Type != "service_account" {
		t.Fatalf("OAuth app audit actor type = %q, want service_account", event.Actor.Type)
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
