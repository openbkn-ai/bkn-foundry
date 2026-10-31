// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type fallbackProjectionFixture struct{ traces []evidencevo.NormalizedTrace }

func (f *fallbackProjectionFixture) LoadExecutionProjection(_ context.Context, _ iprojectionsource.Query) (iprojectionsource.Result, error) {
	return iprojectionsource.Result{Traces: f.traces}, nil
}

func TestTraceSummaryFallbackMariaDB(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_FALLBACK_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit isolated fallback database required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.DBName != "bkn_trace_2779_test" || !strings.HasPrefix(cfg.Addr, "127.0.0.1:") {
		t.Fatal("dedicated local bkn_trace_2779_test database required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	store := New(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("fb-%d", time.Now().UnixNano())
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	owner := sessionvo.Owner{EffectiveSubjectID: prefix + "-owner", EffectiveSubjectType: sessionvo.SubjectService, ApplicationPrincipalID: prefix + "-app"}
	foreign := sessionvo.Owner{EffectiveSubjectID: prefix + "-private", EffectiveSubjectType: sessionvo.SubjectService, ApplicationPrincipalID: prefix + "-private-app"}
	comparisonOwner := sessionvo.Owner{EffectiveSubjectID: prefix + "-comparison", EffectiveSubjectType: sessionvo.SubjectService, ApplicationPrincipalID: prefix + "-comparison-app"}
	var comparisonTraces []evidencevo.NormalizedTrace
	var comparisonIDs []string
	missing, healthy, older := prefix+"-missing", prefix+"-healthy", prefix+"-older"
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		for _, who := range []sessionvo.Owner{owner, foreign, comparisonOwner} {
			conv := who.EffectiveSubjectID + "-conv"
			interaction := conv + "-int"
			tx.SaveConversation(sessionvo.Conversation{ID: conv, Owner: who, ExternalConversationKey: conv, Generation: 1, Status: sessionvo.ConversationClosed, RowVersion: 1, CreatedAt: at, UpdatedAt: at})
			tx.SaveInteraction(sessionvo.Interaction{ID: interaction, ConversationID: conv, Ordinal: 1, ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete, StartIdempotencyKey: interaction, LeaseToken: interaction, LeaseExpiresAt: at.Add(24 * time.Hour), RowVersion: 1, CreatedAt: at, UpdatedAt: at})
		}
		save := func(id, trace, tool string, when time.Time, who sessionvo.Owner, failed bool) {
			conv := who.EffectiveSubjectID + "-conv"
			interaction := conv + "-int"
			status, rs := sessionvo.AttemptCompleted, sessionvo.ReceiptCompleted
			var failure *sessionvo.PayloadEnvelope
			if failed {
				status, rs = sessionvo.AttemptFailed, sessionvo.ReceiptFailed
				p, e := sessionvo.InlineJSONPayload([]byte(`{"message":"visible failure"}`))
				if e != nil {
					t.Fatal(e)
				}
				failure = &p
			}
			tx.SaveOperation(sessionvo.Operation{ID: id, ConversationID: conv, InteractionID: interaction, OperationKey: id, ToolName: tool, AttemptStatus: status, RowVersion: 1, CreatedAt: when, UpdatedAt: when})
			tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: id, Attempt: 1, ConversationID: conv, InteractionID: interaction, TraceID: trace, RequestID: trace, ToolName: tool, Protocol: sessionvo.ProtocolMCP, SourceModule: "fallback-service", StartedAt: when, Status: status, Error: failure})
			terminal := when.Add(time.Second)
			tx.SaveReceipt(sessionvo.Receipt{ID: id, SchemaVersion: "1", Attempt: 1, Owner: who, ConversationID: conv, InteractionID: interaction, OperationID: id, OperationKey: id, ToolName: tool, Status: rs, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: trace, TraceID: trace, RowVersion: 1, IssuedAt: when, TerminalAt: &terminal})
		}
		// A different subject's earlier failure/root must not contaminate this page.
		save(prefix+"-foreign", missing, "private-root", at.Add(-time.Hour), foreign, true)
		save(prefix+"-foreign-healthy", healthy, "private-root", at.Add(-time.Hour), foreign, true)
		for i := 0; i < 32; i++ {
			tool := "child"
			if i == 0 {
				tool = "visible-root"
			}
			save(fmt.Sprintf("%s-%02d", prefix, i), missing, tool, at.Add(time.Duration(i)*time.Second), owner, i == 31)
		}
		save(prefix+"-h", healthy, "healthy-root", at.Add(-time.Minute), owner, false)
		save(prefix+"-o", older, "older-root", at.Add(-2*time.Minute), owner, false)
		for caseID, statuses := range [][]bool{{true, false}, {false, true, false}, {true, false, true, false}, {false, false}, {false, true}} {
			traceID := fmt.Sprintf("%s-compare-%d", prefix, caseID)
			trace := evidencevo.NormalizedTrace{TraceID: traceID}
			for i, failed := range statuses {
				id := fmt.Sprintf("%s-%02d", traceID, i)
				when := at.Add(time.Duration(i) * time.Second)
				// Exercise receipt-ID tie ordering as well as issued-at ordering.
				if caseID == 0 {
					when = at
				}
				save(id, traceID, "root", when, comparisonOwner, failed)
				status := "completed"
				if failed {
					status = "failed"
				}
				trace.Events = append(trace.Events, evidencevo.EvidenceEvent{EventID: "receipt:" + id, EventType: "retrieval.completed", InteractionID: comparisonOwner.EffectiveSubjectID + "-conv-int", ObservedAt: when.Format(time.RFC3339Nano), EmittedAt: when.Add(time.Second).Format(time.RFC3339Nano), Payload: map[string]any{"status": status}})
			}
			comparisonTraces = append(comparisonTraces, trace)
			comparisonIDs = append(comparisonIDs, traceID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	comparisonRows, err := store.LoadTraceSummaryFallback(ctx, comparisonIDs, evidencevo.QueryScope{AccountID: comparisonOwner.EffectiveSubjectID, AccountType: "service"})
	if err != nil {
		t.Fatal(err)
	}
	_, expected := evidencevo.BuildExecutionSummaries(comparisonTraces, nil)
	byID := map[string]evidencevo.TraceSummary{}
	for _, row := range comparisonRows {
		byID[row.TraceID] = row
	}
	for _, want := range expected {
		got, ok := byID[want.TraceID]
		if !ok || got.Status != want.Status || got.StartedAt != want.StartedAt || got.CompletedAt != want.CompletedAt || got.DurationMS != want.DurationMS || got.InteractionID != want.InteractionID {
			t.Fatalf("receipt projection mismatch: got=%+v want=%+v", got, want)
		}
	}
	scope := evidencevo.QueryScope{AccountID: owner.EffectiveSubjectID, AccountType: "service"}
	projected := evidencevo.NormalizedTrace{TraceID: healthy, RequestID: healthy, AccountID: scope.AccountID, AccountType: scope.AccountType, ApplicationPrincipalID: owner.ApplicationPrincipalID, Events: []evidencevo.EvidenceEvent{{EventID: prefix + "-event", EventType: "retrieval.completed", ObservedAt: at.Add(-time.Minute).Format(time.RFC3339Nano)}}}
	source := &fallbackProjectionFixture{traces: []evidencevo.NormalizedTrace{projected}}
	svc := evidencesvc.New(evidencestore.New(), evidencesvc.WithSessionStore(store), evidencesvc.WithProjectionSource(source))
	var before, after int
	if err := db.QueryRow("SELECT COUNT(*) FROM bkn_trace_projection_outbox").Scan(&before); err != nil {
		t.Fatal(err)
	}
	page, err := svc.ListTraceExecutions(ctx, evidencevo.SummaryQueryOptions{Scope: scope, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[0].TraceID != missing || page.Entries[1].TraceID != healthy || page.Total != 3 || page.NextCursor == nil || page.Partial || page.Truncated {
		t.Fatalf("page=%+v", page)
	}
	row := page.Entries[0]
	if row.Status != "error" || row.RootOperation != "visible-root" || row.ErrorSummary != "visible failure" || row.DurationMS != 32000 || row.SpanCountStatus != "unavailable" || row.ApplicationPrincipalID != owner.ApplicationPrincipalID || row.ConversationID != owner.EffectiveSubjectID+"-conv" {
		t.Fatalf("full authorized lifetime not preserved: %+v", row)
	}
	next, err := svc.ListTraceExecutions(ctx, evidencevo.SummaryQueryOptions{Scope: scope, Limit: 2, Cursor: *page.NextCursor})
	if err != nil || len(next.Entries) != 1 || next.Entries[0].TraceID != older || next.Total != 3 || next.NextCursor != nil {
		t.Fatalf("next page=%+v err=%v", next, err)
	}
	// The window selects identities but does not change the root or lifetime aggregate.
	window, err := svc.ListTraceExecutions(ctx, evidencevo.SummaryQueryOptions{Scope: scope, Limit: 2, From: at.Add(30 * time.Second)})
	if err != nil || len(window.Entries) != 1 || window.Entries[0].RootOperation != "visible-root" || window.Entries[0].DurationMS != 32000 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	// No projection documents at all is still a complete canonical page.
	source.traces = nil
	empty, err := svc.ListTraceExecutions(ctx, evidencevo.SummaryQueryOptions{Scope: scope, Limit: 2})
	if err != nil || len(empty.Entries) != 2 || empty.Entries[1].Status != "completed" || empty.Partial {
		t.Fatalf("all missing=%+v err=%v", empty, err)
	}
	handler := httphandler.NewEvidenceHandlerWithSecurityConfig(svc, httphandler.EvidenceHandlerSecurityConfig{AllowUnauthenticatedQuery: true})
	request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces?limit=2", nil)
	request.Header.Set("x-account-id", scope.AccountID)
	request.Header.Set("x-account-type", scope.AccountType)
	response := httptest.NewRecorder()
	handler.ListTraceExecutions(response, request)
	var responsePage evidencevo.TraceSummaryPage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &responsePage) != nil || len(responsePage.Entries) != 2 || responsePage.Entries[0].TraceID != missing {
		t.Fatalf("HTTP fallback failed: %d %s", response.Code, response.Body.String())
	}
	rejected := httptest.NewRecorder()
	handler.ListTraceExecutions(rejected, httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces?limit=2", nil))
	if rejected.Code == http.StatusOK {
		t.Fatal("missing identity must not receive fallback data")
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM bkn_trace_projection_outbox").Scan(&after); err != nil || before != after {
		t.Fatalf("read changed outbox: before=%d after=%d err=%v", before, after, err)
	}
}
