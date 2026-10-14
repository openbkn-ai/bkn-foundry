// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestAuditLedgerSourceReportsIntegratedSource(t *testing.T) {
	status := NewAuditLedgerSource(nil).Metadata()
	if status.Status != observabilityvo.SourceCoverageHealthy || status.Reason != "" {
		t.Fatalf("audit ledger status=%+v, want integrated source without coverage inference", status)
	}
	if status.CollectionMethod != "kafka_audit" || status.CountAccuracy != "partial" {
		t.Fatalf("audit ledger integration metadata=%+v", status)
	}
	if !reflect.DeepEqual(status.CoveredModules, []string{"system_management"}) {
		t.Fatalf("audit ledger covered modules=%v", status.CoveredModules)
	}
}

func TestAuditLedgerSourceDeclaresSourceIDPushdown(t *testing.T) {
	if !NewAuditLedgerSource(nil).SupportsSourceIDFilter() {
		t.Fatal("audit ledger must preserve exact counts by declaring its source_id pushdown")
	}
}

func TestAuditQueryCategoriesUsesAuthorizedDefaultAndIntersection(t *testing.T) {
	authorized := []string{"access.user", "audit.admin", "audit.security"}
	if got := auditQueryCategories(observabilityvo.LogQuery{AuthorizedCategories: authorized}); !reflect.DeepEqual(got, []string{"access.user", "audit.admin", "audit.security"}) {
		t.Fatalf("default categories=%v", got)
	}
	got := auditQueryCategories(observabilityvo.LogQuery{
		Categories: []string{"access.user", "audit.admin", "runtime.business"}, AuthorizedCategories: authorized,
	})
	if !reflect.DeepEqual(got, []string{"access.user", "audit.admin"}) {
		t.Fatalf("intersected categories=%v", got)
	}
	metadata := NewAuditLedgerSource(nil).Metadata()
	if !reflect.DeepEqual(metadata.Categories, []string{"access.user", "audit.admin", "audit.security"}) {
		t.Fatalf("ledger categories=%v", metadata.Categories)
	}
}

func TestAuditLogRecordDoesNotInventDisplayNamesForIncompleteProducerFacts(t *testing.T) {
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
	if record.ActorNameSnapshot != "" || record.TargetNameSnapshot != "" {
		t.Fatalf("consumer must not turn opaque identifiers into business display names: actor=%q target=%q", record.ActorNameSnapshot, record.TargetNameSnapshot)
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

func TestAuditLedgerMixedSearchPreservesRawContinuation(t *testing.T) {
	for _, count := range []int{0, 20, 21} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			reader, err := auditstore.NewReader(db)
			if err != nil {
				t.Fatal(err)
			}
			when := time.Date(2026, 9, 25, 9, 30, 0, 0, time.UTC)
			from, to := when.Add(-time.Hour), when.Add(time.Hour)
			rows := sqlmock.NewRows([]string{"event_id", "source_id", "payload", "occurred_at", "broker_received_at", "recorded_at"})
			for i := 0; i < count; i++ {
				eventID := fmt.Sprintf("evt-%03d", i)
				occurredAt := when.Add(-time.Duration(i) * time.Second)
				payload := fmt.Sprintf(`{"event_id":%q,"source_id":"bkn-safe","category":"audit.admin","event_name":"user.created","occurred_at":%q,"actor":{"id":"admin"},"target":{"type":"user","id":"user-a"},"scope":{"business_module":"system_management"},"outcome":"success"}`, eventID, occurredAt.Format(time.RFC3339Nano))
				rows.AddRow(eventID, "bkn-safe", []byte(payload), occurredAt, when, when)
			}
			mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, source_id, payload, occurred_at, broker_received_at, recorded_at FROM bkn_audit.audit_event_202609")).WithArgs(from, to, "audit.admin", 21).WillReturnRows(rows)
			page, err := NewAuditLedgerSource(reader).Search(context.Background(), observabilityvo.LogQuery{TimeFrom: &from, TimeTo: &to, Limit: 20, Categories: []string{"audit.admin"}, AuthorizedCategories: []string{"audit.admin"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != min(count, 20) || (page.NextCursor != "") != (count > 20) {
				t.Fatalf("count=%d page=%+v", count, page)
			}
			if count > 0 && page.LastPosition.LogID != fmt.Sprintf("evt-%03d", min(count, 20)-1) {
				t.Fatal("last consumed boundary must exclude lookahead")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
