// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestAuditLedgerSourceStatusReturnsDropWindowAsAnUnknownPair(t *testing.T) {
	payload, err := json.Marshal(NewAuditLedgerSource(nil).Metadata())
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatal(err)
	}
	if _, ok := status["dropped_records"]; !ok || status["dropped_records"] != nil {
		t.Fatalf("dropped_records must be present and null while producer coverage is unknown: %s", payload)
	}
	if _, ok := status["dropped_records_since"]; !ok || status["dropped_records_since"] != nil {
		t.Fatalf("dropped_records_since must be present and null while producer coverage is unknown: %s", payload)
	}
}

func TestAuditQueryCategoriesUsesAuthorizedDefaultAndIntersection(t *testing.T) {
	authorized := []string{"runtime.system", "audit.admin", "audit.security"}
	if got := auditQueryCategories(observabilityvo.LogQuery{AuthorizedCategories: authorized}); !reflect.DeepEqual(got, []string{"audit.admin", "audit.security"}) {
		t.Fatalf("default categories=%v", got)
	}
	got := auditQueryCategories(observabilityvo.LogQuery{
		Categories: []string{"audit.admin", "runtime.business"}, AuthorizedCategories: authorized,
	})
	if !reflect.DeepEqual(got, []string{"audit.admin"}) {
		t.Fatalf("intersected categories=%v", got)
	}
}

func TestAuditLogRecordUsesCanonicalIdentityAndSafeDisplayFallbacks(t *testing.T) {
	when := time.Date(2026, 9, 25, 9, 30, 0, 0, time.UTC)
	brokerReceivedAt := when.Add(2 * time.Second)
	recordedAt := when.Add(3 * time.Second)
	record := auditLogRecord(auditsvc.Record{
		EventID: "evt-1", OccurredAt: when, BrokerReceivedAt: brokerReceivedAt, RecordedAt: recordedAt,
		SourceID: "execution-factory",
		Category: "audit.admin", EventName: "execution_factory.operation.observed",
		BusinessModule: "execution_factory", ActorID: "user-1", TargetType: "toolbox",
		TargetID: "box-1", Action: "execute", Outcome: "success", Environment: "test",
		EffectiveSubjectID: "user-1", ActorType: "user", AuthMethod: "oauth",
		SourceChannel: "studio", Summary: "executed toolbox", ApplicationID: "app-1",
		KnowledgeNetworkIDs: []string{"kn-1"}, FailureCode: "SAFE_FAILURE", HTTPStatus: 202,
		Transport: "http", Method: "POST", ClientIP: "192.0.2.1", RequestID: "req-1",
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", OperationID: "op-1",
	})

	if record.LogID != "evt-1" || record.SourceLogID != "evt-1" {
		t.Fatalf("unexpected canonical ids: log=%q source=%q", record.LogID, record.SourceLogID)
	}
	if record.ActorNameSnapshot != "user-1" || record.TargetNameSnapshot != "box-1" {
		t.Fatalf("safe display fallbacks were not applied: actor=%q target=%q", record.ActorNameSnapshot, record.TargetNameSnapshot)
	}
	if record.ResourceRef == nil || record.ResourceRef.ResourceType != "toolbox" || record.ResourceRef.ResourceID != "box-1" {
		t.Fatalf("target resource ref=%#v", record.ResourceRef)
	}
	if !record.ObservedTimestamp.Equal(brokerReceivedAt) || !record.RecordedAt.Equal(recordedAt) {
		t.Fatalf("timestamps: observed=%s recorded=%s", record.ObservedTimestamp, record.RecordedAt)
	}
	if record.ApplicationID != "app-1" || len(record.KnowledgeNetworkIDs) != 1 || record.KnowledgeNetworkIDs[0] != "kn-1" {
		t.Fatalf("scope: app=%q networks=%v", record.ApplicationID, record.KnowledgeNetworkIDs)
	}
	if record.FailureCode != "SAFE_FAILURE" || record.RequestID != "req-1" || record.TraceID == "" || record.OperationID != "op-1" {
		t.Fatalf("audit correlation fields were not mapped: %#v", record)
	}
	if record.Attributes["method"] != "POST" || record.Attributes["status_code"] != 202 || record.Attributes["client_ip"] != "192.0.2.1" || record.Attributes["transport"] != "http" {
		t.Fatalf("safe attributes=%#v", record.Attributes)
	}
}

func TestClientIPIsOnlyExposedOnAuthorizedSecurityDetail(t *testing.T) {
	record := observabilityvo.LogRecord{
		Category:   observabilityvo.CategoryAuditAdmin,
		Attributes: map[string]any{"client_ip": "192.0.2.1", "method": "POST"},
	}
	listRecord, listRedacted := projectClientIP(record, false, true)
	if !listRedacted || listRecord.Attributes["client_ip"] != nil || listRecord.Attributes["method"] != "POST" {
		t.Fatalf("list projection=%#v redacted=%t", listRecord.Attributes, listRedacted)
	}
	adminDetail, adminRedacted := projectClientIP(record, true, true)
	if !adminRedacted || adminDetail.Attributes["client_ip"] != nil {
		t.Fatalf("admin detail projection=%#v redacted=%t", adminDetail.Attributes, adminRedacted)
	}
	record.Category = observabilityvo.CategoryAuditSecurity
	securityDetail, securityRedacted := projectClientIP(record, true, true)
	if securityRedacted || securityDetail.Attributes["client_ip"] != "192.0.2.1" {
		t.Fatalf("security detail projection=%#v redacted=%t", securityDetail.Attributes, securityRedacted)
	}
	unauthorizedDetail, unauthorizedRedacted := projectClientIP(record, true, false)
	if !unauthorizedRedacted || unauthorizedDetail.Attributes["client_ip"] != nil {
		t.Fatalf("unauthorized detail projection=%#v redacted=%t", unauthorizedDetail.Attributes, unauthorizedRedacted)
	}
}
