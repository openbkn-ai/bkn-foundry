// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditvalidator"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

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
