// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/logsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
)

type auditHandlerReader struct {
	page  auditsvc.Page
	query auditsvc.Query
	calls int
}

type auditLogDelegateStub struct {
	result observabilityvo.ListResult
	calls  int
	query  observabilityvo.LogQuery
}

func (delegate *auditLogDelegateStub) List(_ context.Context, _ evidencevo.AccessProfile, query observabilityvo.LogQuery) (observabilityvo.ListResult, error) {
	delegate.calls++
	delegate.query = query
	return delegate.result, nil
}

func TestLogHandlerDispatchesAuditCategoriesToTheCenterLedgerDelegate(t *testing.T) {
	delegate := &auditLogDelegateStub{result: observabilityvo.ListResult{
		Records:      []observabilityvo.LogRecord{{EventID: "evt-ledger", Category: observabilityvo.CategoryAuditAdmin, EventName: "execution_factory.operation.observed", EventTime: time.Date(2026, 9, 25, 9, 30, 0, 0, time.UTC), RecordedAt: time.Date(2026, 9, 25, 9, 31, 0, 0, time.UTC), SourceID: "execution-factory", BusinessModule: "execution_factory", ActorID: "user-1", TargetType: "toolbox", TargetID: "box-1", Action: "execute", Outcome: "success"}},
		SourceStatus: []observabilityvo.SourceStatus{{SourceID: "audit-ledger", Status: "healthy", Reliability: "best_effort", CollectionMethod: "kafka_audit", CountAccuracy: "exact"}}, Count: 1, CountExact: true, Page: 1, PageSize: 20,
	}}
	profile := evidencevo.AccessProfile{ActorID: "audit-1", EffectiveSubjectID: "audit-1", Roles: []string{"audit"}, AccountActive: true, Fingerprint: "sha256:audit"}
	authorizer := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{AllowUnauthenticatedQuery: true, AuthorizationScopeResolver: &fakeAccessScopeResolver{profile: profile}})
	handler := NewLogHandlerWithAuditDelegate(logsvc.New([]logsvc.Source{handlerLogSource{}}), authorizer, delegate)
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs?categories=audit.admin&time_from=2026-09-25T08:30:00Z&time_to=2026-09-25T10:30:00Z&limit=20", nil)
	request.Header.Set("x-account-id", "audit-1")
	request.Header.Set("x-account-type", "user")
	response := httptest.NewRecorder()

	handler.ListLogs(response, request)

	if response.Code != http.StatusOK || delegate.calls != 1 || len(delegate.query.Categories) != 1 || delegate.query.Categories[0] != observabilityvo.CategoryAuditAdmin || !strings.Contains(response.Body.String(), `"event_id":"evt-ledger"`) {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, delegate.calls, response.Body.String())
	}
}

func TestLogHandlerLeavesNonAuditCategoriesOnExistingSources(t *testing.T) {
	delegate := &auditLogDelegateStub{}
	profile := evidencevo.AccessProfile{ActorID: "admin-1", EffectiveSubjectID: "admin-1", Roles: []string{"admin"}, AccountActive: true, Fingerprint: "sha256:admin"}
	authorizer := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{AllowUnauthenticatedQuery: true, AuthorizationScopeResolver: &fakeAccessScopeResolver{profile: profile}})
	handler := NewLogHandlerWithAuditDelegate(logsvc.New([]logsvc.Source{handlerLogSource{records: []observabilityvo.LogRecord{{LogID: "runtime-1", EventID: "runtime-1", Category: observabilityvo.CategoryRuntimeBusiness, EventName: "service.started", EventTime: time.Now().UTC(), ObservedTimestamp: time.Now().UTC(), EffectiveSubjectID: "admin-1"}}}}), authorizer, delegate)
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs?categories=runtime.business", nil)
	request.Header.Set("x-account-id", "admin-1")
	request.Header.Set("x-account-type", "user")
	response := httptest.NewRecorder()

	handler.ListLogs(response, request)

	if response.Code != http.StatusOK || delegate.calls != 0 || !strings.Contains(response.Body.String(), `"source_id":"otel"`) {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, delegate.calls, response.Body.String())
	}
}

func (reader *auditHandlerReader) Query(_ context.Context, query auditsvc.Query) (auditsvc.Page, error) {
	reader.calls++
	reader.query = query
	return reader.page, nil
}

func TestAuditHandlerQueriesCenterLedgerForAuthorizedAuditRole(t *testing.T) {
	occurredAt := time.Date(2026, 9, 25, 9, 30, 0, 0, time.UTC)
	reader := &auditHandlerReader{page: auditsvc.Page{Records: []auditsvc.Record{{
		EventID: "evt-1", OccurredAt: occurredAt, SourceID: "execution-factory", Category: "audit.admin",
		EventName: "execution_factory.operation.observed", BusinessModule: "execution_factory", ActorID: "user-1",
		TargetType: "toolbox", TargetID: "box-1", Action: "execute", Outcome: "success",
	}}}}
	service, err := auditsvc.New(reader)
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestAuditHandler(t, evidencevo.AccessProfile{
		ActorID: "audit-1", EffectiveSubjectID: "audit-1", Roles: []string{"audit"}, AccountActive: true,
	}, service)
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/audit-events?categories=audit.admin&time_from=2026-09-25T08:30:00Z&time_to=2026-09-25T10:30:00Z&limit=20", nil)
	request.Header.Set("x-account-id", "audit-1")
	request.Header.Set("x-account-type", "user")
	response := httptest.NewRecorder()

	handler.List(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if reader.calls != 1 || len(reader.query.Categories) != 1 || reader.query.Categories[0] != "audit.admin" || reader.query.Limit != 20 {
		t.Fatalf("reader query=%#v calls=%d", reader.query, reader.calls)
	}
	var body struct {
		Data []auditsvc.Record `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].EventID != "evt-1" || body.Data[0].SourceID != "execution-factory" {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestAuditHandlerRejectsUnauthorizedCategoryBeforeReadingLedger(t *testing.T) {
	reader := &auditHandlerReader{}
	service, err := auditsvc.New(reader)
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestAuditHandler(t, evidencevo.AccessProfile{
		ActorID: "user-1", EffectiveSubjectID: "user-1", Roles: []string{"normal_user"}, AccountActive: true,
	}, service)
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/audit-events?categories=audit.admin&time_from=2026-09-25T08:30:00Z&time_to=2026-09-25T10:30:00Z", nil)
	request.Header.Set("x-account-id", "user-1")
	request.Header.Set("x-account-type", "user")
	response := httptest.NewRecorder()

	handler.List(response, request)

	if response.Code != http.StatusForbidden || !containsJSONCode(response.Body.Bytes(), "audit_query_unauthorized") || reader.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, reader.calls, response.Body.String())
	}
}

func TestAuditHandlerRejectsOverlongWindowBeforeReadingLedger(t *testing.T) {
	reader := &auditHandlerReader{}
	service, err := auditsvc.New(reader)
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestAuditHandler(t, evidencevo.AccessProfile{
		ActorID: "audit-1", EffectiveSubjectID: "audit-1", Roles: []string{"audit"}, AccountActive: true,
	}, service)
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/audit-events?categories=audit.admin&time_from=2026-08-01T00:00:00Z&time_to=2026-09-01T00:00:01Z", nil)
	request.Header.Set("x-account-id", "audit-1")
	request.Header.Set("x-account-type", "user")
	response := httptest.NewRecorder()

	handler.List(response, request)

	if response.Code != http.StatusBadRequest || !containsJSONCode(response.Body.Bytes(), "invalid_audit_filter") || reader.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, reader.calls, response.Body.String())
	}
}

func newTestAuditHandler(t *testing.T, profile evidencevo.AccessProfile, service *auditsvc.Service) *AuditHandler {
	t.Helper()
	authorizer := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedQuery:  true,
		AuthorizationScopeResolver: &fakeAccessScopeResolver{profile: profile},
	})
	return NewAuditHandler(service, authorizer)
}
