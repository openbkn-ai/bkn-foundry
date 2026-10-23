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
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// This is an opt-in SQL capacity baseline, not a production load test. It creates
// and drops only its own randomly named database. No existing database is used.
func TestSummaryCapacityMariaDB(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_CAPACITY_ADMIN_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_CAPACITY_ADMIN_DSN is not set")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("bkn_trace_capacity_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Error(err)
		}
	}()
	cfg.DBName, cfg.ParseTime = name, true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	ctx := context.Background()
	store := New(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	owner := sessionvo.Owner{ApplicationPrincipalID: "capacity-app", EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: "capacity-owner"}
	// 2,100 matching traces exceed the old 2,000 projection-candidate window.
	// Every trace has an empty first module and a later descendant with a different
	// module. Equal timestamps force the operation-ID/attempt tie breakers.
	for batch := 0; batch < 2202; batch += 50 {
		if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			if batch == 0 {
				tx.SaveConversation(sessionvo.Conversation{ID: "capacity-c", Owner: owner, ExternalConversationKey: "capacity-c", Generation: 1, Status: sessionvo.ConversationClosed, RowVersion: 1, CreatedAt: now, UpdatedAt: now})
			}
			for i := batch; i < min(batch+50, 2202); i++ {
				id := fmt.Sprintf("capacity-%05d", i)
				at := now.Add(time.Duration(i/10) * time.Second)
				rowOwner := owner
				if i == 2200 {
					rowOwner.EffectiveSubjectID = "foreign-owner"
				}
				tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: "capacity-c", Ordinal: uint64(i + 1), ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete, StartIdempotencyKey: id, LeaseToken: id, LeaseExpiresAt: now.Add(time.Hour), RowVersion: 1, CreatedAt: at, UpdatedAt: at})
				module := "context-loader"
				if i >= 2100 && i < 2200 {
					module = "other"
				}
				if i == 2201 {
					module = ""
				}
				for j, source := range []string{"", module, "descendant"} {
					if i == 2201 {
						source = ""
					}
					op := fmt.Sprintf("%s-op-%d", id, j)
					tx.SaveOperation(sessionvo.Operation{ID: op, ConversationID: "capacity-c", InteractionID: id, OperationKey: op, ToolName: "run_sql", AttemptStatus: sessionvo.AttemptCompleted, RowVersion: 1, CreatedAt: at, UpdatedAt: at})
					tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: op, Attempt: 1, ConversationID: "capacity-c", InteractionID: id, ToolName: "run_sql", Protocol: sessionvo.ProtocolMCP, SourceModule: source, TraceID: id, RequestID: id, StartedAt: at, Status: sessionvo.AttemptCompleted})
					if j == 1 && i == 0 {
						tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: op, Attempt: 2, ConversationID: "capacity-c", InteractionID: id, ToolName: "run_sql", Protocol: sessionvo.ProtocolMCP, SourceModule: "retry-module", TraceID: id, RequestID: id, StartedAt: at, Status: sessionvo.AttemptCompleted})
					}
				}
				for attempt := 0; attempt < 1+boolInt(i == 0); attempt++ {
					tx.SaveReceipt(sessionvo.Receipt{ID: fmt.Sprintf("%s-r-%d", id, attempt), SchemaVersion: "1", Attempt: uint32(attempt + 1), Owner: rowOwner, ConversationID: "capacity-c", InteractionID: id, OperationID: id + "-op-1", OperationKey: id + "-op-1", ToolName: "run_sql", Status: sessionvo.ReceiptCompleted, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: id, TraceID: id, RowVersion: 1, IssuedAt: at})
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("seeded 2202 traces; testing canonical paging")
	query := isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{AccountID: owner.EffectiveSubjectID, AccountType: string(owner.EffectiveSubjectType)}, Service: "context-loader", Limit: 200}
	seen := map[string]bool{}
	for {
		page, err := store.ListTraceSummaryIdentities(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 2100 {
			t.Fatalf("service total=%d, want 2100", page.Total)
		}
		for _, entry := range page.Entries {
			if seen[entry.ID] {
				t.Fatalf("duplicate cursor entry %s", entry.ID)
			}
			seen[entry.ID] = true
		}
		if !page.HasMore {
			break
		}
		last := page.Entries[len(page.Entries)-1]
		query.AfterID, query.AfterStartedAt = last.ID, last.StartedAt
	}
	if len(seen) != 2100 {
		t.Fatalf("reachable=%d", len(seen))
	}
	query.AfterID, query.AfterStartedAt = "", ""
	for _, tc := range []struct {
		service, account string
		want             int
	}{{"", owner.EffectiveSubjectID, 2201}, {"other", owner.EffectiveSubjectID, 100}, {"descendant", owner.EffectiveSubjectID, 0}, {"retry-module", owner.EffectiveSubjectID, 0}, {"context-loader", "foreign-owner", 1}, {"context-loader", "unauthorized", 0}} {
		q := query
		q.Service = tc.service
		q.Scope.AccountID = tc.account
		page, err := store.ListTraceSummaryIdentities(ctx, q)
		if err != nil || page.Total != tc.want {
			t.Fatalf("%+v: total=%d err=%v", tc, page.Total, err)
		}
	}
	q := query
	q.From = now.Add(10 * time.Second)
	q.To = now.Add(19 * time.Second)
	page, err := store.ListTraceSummaryIdentities(ctx, q)
	if err != nil || page.Total != 100 {
		t.Fatalf("time range total=%d err=%v", page.Total, err)
	}
	q = query
	q.AfterID = "bad"
	q.AfterStartedAt = "invalid"
	if _, err := store.ListTraceSummaryIdentities(ctx, q); err == nil {
		t.Fatal("invalid cursor accepted")
	}

	report := map[string]any{"kind": "layered baseline: real SQL and operation HTTP; optional real OpenSearch trace HTTP; gateway/EE excluded; see per-layer scope", "traces": 2202, "matching_traces": 2100, "runs_per_case": 40}
	var version string
	if err := db.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	report["mariadb_version"] = version
	cases := map[string]any{}
	for _, service := range []string{"", "context-loader"} {
		for _, limit := range []int{1, 20} {
			q := query
			q.Service = service
			q.Limit = limit
			durations := []float64{}
			for run := 0; run < 43; run++ {
				start := time.Now()
				page, err := store.ListTraceSummaryIdentities(ctx, q)
				if err != nil || len(page.Entries) != limit {
					t.Fatalf("timed page=%+v err=%v", page, err)
				}
				if run >= 3 {
					durations = append(durations, float64(time.Since(start).Microseconds())/1000)
				}
			}
			sort.Float64s(durations)
			where, args := summaryOwnerWhere("r", q)
			where = append(where, usableSummaryReceiptPredicates("r")...)
			if service != "" {
				where = append(where, traceSummaryServicePredicate())
				args = append(args, service)
			}
			clause := strings.Join(where, " AND ")
			countSQL := "SELECT COUNT(DISTINCT r.trace_id) FROM bkn_trace_receipts r WHERE " + clause
			pageSQL := "SELECT r.trace_id, MIN(r.issued_at) FROM bkn_trace_receipts r WHERE " + clause + " GROUP BY r.trace_id ORDER BY MIN(r.issued_at) DESC, r.trace_id ASC LIMIT ? OFFSET ?"
			pageArgs := append(append([]any{}, args...), limit+1, 0)
			plan := map[string]string{}
			phases := map[string]any{}
			for key, sqlAndArgs := range map[string]struct {
				sql  string
				args []any
			}{"count": {countSQL, args}, "page": {pageSQL, pageArgs}} {
				var value string
				if err := db.QueryRowContext(ctx, "EXPLAIN FORMAT=JSON "+sqlAndArgs.sql, sqlAndArgs.args...).Scan(&value); err != nil {
					t.Fatal(err)
				}
				plan[key] = value
				samples := make([]float64, 0, 40)
				for run := 0; run < 40; run++ {
					start := time.Now()
					rows, err := db.QueryContext(ctx, sqlAndArgs.sql, sqlAndArgs.args...)
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					values := make([]any, len(columns))
					pointers := make([]any, len(columns))
					for i := range values {
						pointers[i] = &values[i]
					}
					for rows.Next() {
						if err := rows.Scan(pointers...); err != nil {
							t.Fatal(err)
						}
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					samples = append(samples, float64(time.Since(start).Microseconds())/1000)
				}
				sort.Float64s(samples)
				phases[key] = map[string]any{"p50_ms": samples[19], "p95_ms": samples[37]}

			}
			t.Logf("service=%q limit=%d p50=%.3f ms p95=%.3f ms", service, limit, durations[19], durations[37])
			cases[fmt.Sprintf("service=%s/limit=%d", service, limit)] = map[string]any{"p50_ms": durations[19], "p95_ms": durations[37], "samples_ms": durations, "explain": plan, "sql_phases": phases, "identity_sql_requests": 2 - boolInt(service != ""), "sql_phases_note": "independent legacy count/page reference; service production uses window query EXPLAIN in legacy_boundary_parity", "projection_requests": "not exercised in SQL-only measurement", "returned": limit}
		}
	}
	report["cases"] = cases
	report["legacy_boundary_parity"] = capacityCompareLegacyBoundaries(t, store, now)
	report["service_boundary"] = capacityServiceBoundary(t, store, now)
	report["operation_summaries"] = capacityOperationSummaries(t, store, now)
	report["http_operation_summaries"] = capacityHTTPSummaries(t, store)
	report["http_opensearch_trace_list"] = capacityOpenSearchHTTP(t, store, now)
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(raw))
	if path := os.Getenv("BKN_TRACE_CAPACITY_REPORT"); path != "" {
		if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func capacityOperationSummaries(t *testing.T, store *Store, now time.Time) map[string]any {
	t.Helper()
	ctx := context.Background()
	results := map[string]any{}
	// A 256 KiB input + output + error for each call makes full facts about 63/84
	// MiB. All four excluded columns are populated, including capability_profile.
	payload := sessionvo.PayloadEnvelope{Mode: "inline", MediaType: "application/json", Inline: json.RawMessage(`"` + strings.Repeat("x", 256*1024) + `"`)}
	for _, n := range []int{84, 112} {
		id := fmt.Sprintf("capacity-large-%d", n)
		if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: "capacity-c", Ordinal: uint64(10000 + n), ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete, StartIdempotencyKey: id, LeaseToken: id, LeaseExpiresAt: now.Add(time.Hour), RowVersion: 1, CreatedAt: now, UpdatedAt: now})
			for i := 0; i < n; i++ {
				op := fmt.Sprintf("%s-%03d", id, i)
				tx.SaveOperation(sessionvo.Operation{ID: op, ConversationID: "capacity-c", InteractionID: id, OperationKey: op, ToolName: "run_sql", AttemptStatus: sessionvo.AttemptCompleted, RowVersion: 1, CreatedAt: now, UpdatedAt: now})
				tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: op, Attempt: 1, ConversationID: "capacity-c", InteractionID: id, ToolName: "run_sql", Protocol: sessionvo.ProtocolMCP, SourceModule: "context-loader", StartedAt: now, Status: sessionvo.AttemptCompleted, Input: payload, Output: &payload, Error: &payload, CapabilityProfile: &sessionvo.CapabilityProfile{ManifestID: "synthetic-profile"}})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var summaries []sessionvo.OperationCallSummary
		start := time.Now()
		if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { summaries = tx.ListOperationCallSummaries(id); return nil }); err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		raw, err := json.Marshal(map[string]any{"entries": summaries, "total": len(summaries)})
		if err != nil {
			t.Fatal(err)
		}
		if len(summaries) != n || len(raw) >= 1024*1024 {
			t.Fatalf("n=%d entries=%d bytes=%d", n, len(summaries), len(raw))
		}
		for _, field := range []string{`"input"`, `"output"`, `"error"`, `"capability_profile"`} {
			if strings.Contains(string(raw), field) {
				t.Fatalf("summary includes %s", field)
			}
		}
		for _, column := range []string{"input_payload", "output_payload", "error_payload", "capability_profile"} {
			if strings.Contains(operationCallSummarySelect, column) {
				t.Fatalf("summary SELECT reads large column %s", column)
			}
		}
		var full []sessionvo.OperationCallFact
		if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { full = tx.ListOperationCallFacts(id); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(full) != n || len(full[0].Input.Inline) != len(payload.Inline) || full[0].CapabilityProfile == nil {
			t.Fatal("full payload compatibility lost")
		}
		results[fmt.Sprint(n)] = map[string]any{"summary_bytes": len(raw), "summary_ms": float64(elapsed.Microseconds()) / 1000, "payload_bytes_per_call": 3 * len(payload.Inline), "full_facts_preserved": true}
	}
	return results
}

// The projection port below is deliberately synthetic. This check verifies the
// selected-page boundary, not OpenSearch performance or network request counts.
type capacityProjection struct {
	now     time.Time
	queries []iprojectionsource.Query
}

func (p *capacityProjection) LoadExecutionProjection(_ context.Context, query iprojectionsource.Query) (iprojectionsource.Result, error) {
	p.queries = append(p.queries, query)
	result := iprojectionsource.Result{}
	for _, id := range query.TraceIDs {
		var index int
		if _, err := fmt.Sscanf(id, "capacity-%05d", &index); err != nil {
			return result, err
		}
		at := p.now.Add(time.Duration(index/10) * time.Second).Format(time.RFC3339Nano)
		result.Traces = append(result.Traces, evidencevo.NormalizedTrace{TraceID: id, RequestID: id, ConversationID: "capacity-c", AccountID: "capacity-owner", AccountType: "service", SchemaVersion: evidencevo.ArtifactContractVersion, Events: []evidencevo.EvidenceEvent{{EventID: "event-" + id, EventType: "agent.interaction.started", TraceID: id, RequestID: id, ObservedAt: at, EmittedAt: at, OperationName: "agent.run", Payload: map[string]any{"agent_id": "capacity-agent"}}}})
	}
	return result, nil
}
func capacityServiceBoundary(t *testing.T, store *Store, now time.Time) map[string]any {
	t.Helper()
	result := map[string]any{}
	for _, service := range []string{"", "context-loader"} {
		for _, limit := range []int{1, 20} {
			projection := &capacityProjection{now: now}
			svc := evidencesvc.New(evidencestore.New(), evidencesvc.WithSessionStore(store), evidencesvc.WithProjectionSource(projection))
			page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: evidencevo.QueryScope{AccountID: "capacity-owner", AccountType: "service"}, Service: service, Limit: limit})
			want := 2201
			if service != "" {
				want = 2100
			}
			if err != nil || page.Total != want || len(page.Entries) != limit {
				t.Fatalf("service boundary service=%q limit=%d page=%+v err=%v", service, limit, page, err)
			}
			if len(projection.queries) != 1 || len(projection.queries[0].TraceIDs) != limit {
				t.Fatalf("projection did not restrict to selected page: %+v", projection.queries)
			}
			result[fmt.Sprintf("service=%s/limit=%d", service, limit)] = map[string]any{"total": page.Total, "returned": len(page.Entries), "projection_port_calls": len(projection.queries), "projection_trace_ids": len(projection.queries[0].TraceIDs), "real_opensearch_requests": "not measured"}
		}
	}
	return result
}
