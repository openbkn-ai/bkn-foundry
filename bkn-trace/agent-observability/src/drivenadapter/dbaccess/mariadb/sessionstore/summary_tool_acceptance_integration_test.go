// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSummaryToolAcceptance(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_CAPACITY_ADMIN_DSN")
	if dsn == "" {
		t.Skip("explicit isolated DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:13374" {
		t.Fatal("dedicated test endpoint required")
	}
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("bkn_trace_tool_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Error(err)
		}
	}()
	cfg.DBName = name
	cfg.ParseTime = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	store := New(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, text := range append(strings.Split(rootToolWhitespace, ""), "\u180e", "\u200b", "\ufeff", "\u00a0Run_SQL\u3000", " x y ", "工具", " ") {
		for _, input := range []string{text, text + "\u4e2d" + text, "", "\x00", "\n\t\u3000"} {
			var actual string
			var nonblank bool
			if err := db.QueryRow("SELECT "+normalizedRootToolSQL("rt")+", "+nonBlankRootToolSQL("rt")+" FROM (SELECT CONVERT(? USING utf8mb4) AS tool_name) rt", input).Scan(&actual, &nonblank); err != nil {
				t.Fatal(err)
			}
			if actual != strings.TrimSpace(input) {
				t.Fatalf("Unicode difference input=%q SQL=%q Go=%q", input, actual, strings.TrimSpace(input))
			}
			if nonblank != (strings.TrimSpace(input) != "") {
				t.Fatalf("Unicode blank predicate differs for %q", input)
			}
		}
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	owner := sessionvo.Owner{ApplicationPrincipalID: "tool-app", EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: "tool-owner"}
	scope := evidencevo.QueryScope{AccountID: owner.EffectiveSubjectID, AccountType: string(owner.EffectiveSubjectType)}
	receipts := []sessionvo.Receipt{}
	save := func(tx isessionstore.Transaction, id, trace, tool string, at time.Time, who sessionvo.Owner) {
		tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: "tool-c", Ordinal: uint64(len(receipts) + 1), ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete, StartIdempotencyKey: id, LeaseToken: id, LeaseExpiresAt: now.Add(time.Hour), RowVersion: 1, CreatedAt: at, UpdatedAt: at})
		tx.SaveOperation(sessionvo.Operation{ID: id, ConversationID: "tool-c", InteractionID: id, OperationKey: id, ToolName: tool, AttemptStatus: sessionvo.AttemptCompleted, RowVersion: 1, CreatedAt: at, UpdatedAt: at})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: id, Attempt: 1, ConversationID: "tool-c", InteractionID: id, ToolName: tool, Protocol: sessionvo.ProtocolMCP, SourceModule: "root-service", TraceID: trace, RequestID: id, StartedAt: at, Status: sessionvo.AttemptCompleted})
		r := sessionvo.Receipt{ID: id, SchemaVersion: "1", Attempt: 1, Owner: who, ConversationID: "tool-c", InteractionID: id, OperationID: id, OperationKey: id, ToolName: tool, Status: sessionvo.ReceiptCompleted, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: id, TraceID: trace, RowVersion: 1, IssuedAt: at}
		tx.SaveReceipt(r)
		receipts = append(receipts, r)
	}
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "tool-c", Owner: owner, ExternalConversationKey: "tool-c", Generation: 1, Status: sessionvo.ConversationClosed, RowVersion: 1, CreatedAt: now, UpdatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for batch := 0; batch < 2202; batch += 50 {
		if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			for i := batch; i < min(batch+50, 2202); i++ {
				id := fmt.Sprintf("tool-%05d", i)
				save(tx, id, id, "run_sql", now.Add(time.Duration(i)*time.Second), owner)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		// The true root lies outside both the selected time window and latest20 projection.
		save(tx, "tool-old-root", "tool-00000", "\u00a0run_sql\u3000", now.Add(-time.Minute), owner)
		for i := 0; i < 25; i++ {
			save(tx, fmt.Sprintf("tool-child-%02d", i), "tool-00000", "child", now.Add(time.Duration(i)*time.Second), owner)
		}
		foreign := owner
		foreign.EffectiveSubjectID = "foreign-owner"
		save(tx, "tool-foreign-root", "tool-00000", "foreign", now.Add(-2*time.Minute), foreign)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	q := isessionstore.SummaryPageQuery{Scope: scope, Tool: "run_sql", Limit: 200}
	seen := map[string]bool{}
	for {
		p, err := store.ListTraceSummaryIdentities(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 2202 {
			t.Fatalf("tool total=%d", p.Total)
		}
		for _, e := range p.Entries {
			if seen[e.ID] {
				t.Fatal("duplicate cursor")
			}
			seen[e.ID] = true
		}
		if !p.HasMore {
			break
		}
		last := p.Entries[len(p.Entries)-1]
		q.AfterID, q.AfterStartedAt = last.ID, last.StartedAt
	}
	if len(seen) != 2202 {
		t.Fatalf("reachable=%d", len(seen))
	}
	for _, tool := range []string{"child", "Run_sql", "run_sql ", "foreign"} {
		p, err := store.ListTraceSummaryIdentities(ctx, isessionstore.SummaryPageQuery{Scope: scope, Tool: tool, Limit: 20})
		if err != nil || p.Total != 0 {
			t.Fatalf("child/binary/owner filter %q total=%d err=%v", tool, p.Total, err)
		}
	}
	p, err := store.ListTraceSummaryIdentities(ctx, isessionstore.SummaryPageQuery{Scope: scope, Tool: "run_sql", Service: "root-service", Limit: 20, Offset: 99999})
	if err != nil || p.Total != 2202 || len(p.Entries) != 0 {
		t.Fatalf("empty page %+v err=%v", p, err)
	}
	q = isessionstore.SummaryPageQuery{Scope: scope, Tool: "run_sql", Limit: 20, From: now, To: now.Add(25 * time.Second)}
	p, err = store.ListTraceSummaryIdentities(ctx, q)
	if err != nil || p.Total != 26 {
		t.Fatalf("whole lifetime root/time window total=%d err=%v", p.Total, err)
	}
	var roots map[string]string
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		roots = map[string]string{}
		for id, root := range tx.ListCanonicalTraceRootsByTraceIDs([]string{"tool-00000"}, scope) {
			if root.HasTool {
				roots[id] = root.ToolName
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if roots["tool-00000"] != "run_sql" {
		t.Fatalf("root=%v", roots)
	}
	endpoint := os.Getenv("BKN_TRACE_CAPACITY_OPENSEARCH_URL")
	if endpoint != "http://127.0.0.1:19274" {
		t.Fatal("dedicated OS endpoint required")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	send := func(method, path string, body []byte) []byte {
		req, err := http.NewRequest(method, endpoint+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode >= 300 {
			t.Fatalf("OS %s status=%d err=%v", method, resp.StatusCode, err)
		}
		return raw
	}
	index := fmt.Sprintf("bkn-trace-tool-%d", time.Now().UnixNano())
	osclient := opensearch.NewWithHTTPClient(endpoint, opensearch.AuthConfig{}, client)
	if err := opensearchprojection.New(osclient, index).EnsureBootstrap(ctx, index+"-v1"); err != nil {
		t.Fatal(err)
	}
	defer send("DELETE", "/"+index+"-v1", nil)
	var bulk bytes.Buffer
	for _, r := range receipts {
		meta, err := json.Marshal(map[string]any{"index": map[string]string{"_index": index, "_id": r.ID}})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{"receipt_id": r.ID, "schema_version": "1", "owner": r.Owner, "conversation_id": r.ConversationID, "interaction_id": r.InteractionID, "operation_id": r.OperationID, "operation_key": r.OperationKey, "tool_name": r.ToolName, "receipt_status": "completed", "evidence_durability": "durable", "request_id": r.RequestID, "trace_id": r.TraceID, "issued_at": r.IssuedAt.Format(time.RFC3339Nano), "terminal_at": r.IssuedAt.Format(time.RFC3339Nano)})
		if err != nil {
			t.Fatal(err)
		}
		bulk.Write(meta)
		bulk.WriteByte('\n')
		bulk.Write(raw)
		bulk.WriteByte('\n')
	}
	var response struct {
		Errors bool `json:"errors"`
	}
	if err := json.Unmarshal(send("POST", "/_bulk?refresh=true", bulk.Bytes()), &response); err != nil || response.Errors {
		t.Fatalf("bulk error=%v", err)
	}
	svc := evidencesvc.New(evidencestore.New(), evidencesvc.WithSessionStore(store), evidencesvc.WithProjectionSource(opensearchcoreprojection.New(osclient, index, nil)))
	for _, tool := range []string{"", "run_sql"} {
		page, err := svc.ListTraceExecutions(ctx, evidencevo.SummaryQueryOptions{Scope: scope, Tool: tool, From: now, To: now, Limit: 20})
		if err != nil || page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].RootOperation != "run_sql" {
			t.Fatalf("toggle=%q page=%+v err=%v", tool, page, err)
		}
	}
	// Absolute cost of the new correct identity query; the capped old result
	// is not a valid performance comparator. Exclude three warmups per case.
	toolIdentitySamples := map[string][]float64{}
	for _, service := range []string{"", "root-service"} {
		key := "tool=run_sql/service=" + service
		for i := -3; i < 10; i++ {
			start := time.Now()
			page, err := store.ListTraceSummaryIdentities(ctx, isessionstore.SummaryPageQuery{Scope: scope, Tool: "run_sql", Service: service, Limit: 20})
			elapsed := float64(time.Since(start).Microseconds()) / 1000
			if err != nil || page.Total != 2202 || len(page.Entries) != 20 {
				t.Fatalf("timed identity page=%+v err=%v", page, err)
			}
			if i >= 0 {
				toolIdentitySamples[key] = append(toolIdentitySamples[key], elapsed)
			}
		}
	}

	// Validate empty-root presence separately from a missing receipt, and the
	// actual DATETIME(6) roundtrip used by canonical SQL ordering.
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		save(tx, "blank-root", "blank-trace", "\u3000\u0085", now.Add(-time.Hour), owner)
		save(tx, "z-nano-early", "nano-trace", "nano-early", now.Add(-time.Hour).Add(100*time.Nanosecond), owner)
		save(tx, "a-nano-later", "nano-trace", "nano-later", now.Add(-time.Hour).Add(900*time.Nanosecond), owner)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var early, later time.Time
	if err := db.QueryRow("SELECT issued_at FROM bkn_trace_receipts WHERE receipt_id=?", "z-nano-early").Scan(&early); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT issued_at FROM bkn_trace_receipts WHERE receipt_id=?", "a-nano-later").Scan(&later); err != nil {
		t.Fatal(err)
	}
	if !early.Equal(later) {
		t.Fatalf("expected DATETIME(6) tie: %v vs %v", early, later)
	}
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		roots := tx.ListCanonicalTraceRootsByTraceIDs([]string{"blank-trace", "missing-trace", "nano-trace"}, scope)
		if !roots["blank-trace"].HasTool || roots["blank-trace"].ToolName != "" || roots["missing-trace"].HasTool || roots["nano-trace"].ToolName != "nano-later" {
			t.Fatalf("empty/missing/persisted-tie roots=%+v", roots)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate the exact v036 predecessor in this owned test DB, then exercise
	// registered037 upgrade, idempotency and data preservation. Never run on live data.
	checksum := func() int64 {
		var table string
		var value int64
		if e := db.QueryRow("CHECKSUM TABLE bkn_trace_receipts EXTENDED").Scan(&table, &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	beforeChecksum := checksum()
	qplan := isessionstore.SummaryPageQuery{Scope: scope, Tool: "run_sql"}
	where, planArgs := summaryOwnerWhere("r", qplan)
	where = append(where, usableSummaryReceiptPredicates("r")...)
	predicate, rootArgs := rootToolPredicate(qplan)
	where = append(where, predicate)
	planArgs = append(planArgs, rootArgs...)
	planSQL := `SELECT trace_id,started_at,total FROM (SELECT r.trace_id,MIN(r.issued_at) AS started_at,COUNT(*) OVER() AS total FROM bkn_trace_receipts r WHERE ` + strings.Join(where, " AND ") + ` GROUP BY r.trace_id) counted ORDER BY started_at DESC,trace_id ASC LIMIT ? OFFSET ?`
	planArgs = append(planArgs, 21, 0)
	var withIndex, withoutIndex string
	if e := db.QueryRow("EXPLAIN FORMAT=JSON "+planSQL, planArgs...).Scan(&withIndex); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec("DROP INDEX idx_bkn_trace_receipt_trace_root ON bkn_trace_receipts"); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec("DELETE FROM bkn_trace_schema_migrations WHERE migration_version='037'"); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow("EXPLAIN FORMAT=JSON "+planSQL, planArgs...).Scan(&withoutIndex); e != nil {
		t.Fatal(e)
	}
	if e := store.EnsureSchema(ctx, false); e == nil || !strings.Contains(e.Error(), "missing migration 037") {
		t.Fatalf("036 image guard=%v", e)
	}
	if e := store.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e := store.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if checksum() != beforeChecksum {
		t.Fatal("index migration changed receipt data")
	}
	applied := map[string]string{}
	for _, m := range Migrations() {
		applied[m.Version] = m.Checksum
	}
	if _, e := migrationPlan(Migrations()[:len(Migrations())-1], applied); e == nil {
		t.Fatal("old manifest must reject037 ledger")
	}
	planReport, reportErr := json.MarshalIndent(map[string]any{"new_tool_identity_latency_ms": toolIdentitySamples, "with037": json.RawMessage(withIndex), "without037": json.RawMessage(withoutIndex), "receipt_checksum_preserved": true, "upgrade036_equivalent_to037": true, "idempotent": true, "old_manifest_rejected": true}, "", "  ")
	if reportErr != nil {
		t.Fatal(reportErr)
	}
	if path := os.Getenv("BKN_TRACE_TOOL_PLAN_REPORT"); path != "" {
		if e := os.WriteFile(path, planReport, 0600); e != nil {
			t.Fatal(e)
		}
	}

	t.Log("PASS real MariaDB+OS: 2202 tool matches/cursor traversal; root outside latest20/time; owner isolation/binary/Unicode; filter-toggle canonical root parity; candidate037 migration")
}
