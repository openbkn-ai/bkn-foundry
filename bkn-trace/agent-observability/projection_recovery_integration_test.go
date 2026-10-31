// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/command/projectiondlq"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestProjectionRecoveryAcceptance(t *testing.T) {
	dsn, endpoint := os.Getenv("BKN_TRACE_RECOVERY_TEST_DSN"), os.Getenv("BKN_TRACE_RECOVERY_OPENSEARCH_URL")
	if dsn == "" || endpoint == "" {
		t.Skip("explicit isolated MariaDB and OpenSearch required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	target, urlErr := url.Parse(endpoint)
	if err != nil || cfg.DBName != "bkn_trace_2779_recovery" || !strings.HasPrefix(cfg.Addr, "127.0.0.1:") || urlErr != nil || target.Hostname() != "127.0.0.1" {
		t.Fatal("dedicated loopback recovery endpoints required")
	}
	adminConfig := *cfg
	adminConfig.DBName = ""
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("bkn_trace_2779_it_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanupCtx, "DROP DATABASE `"+name+"`"); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store := sessionstore.New(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BKN_TRACE_CORE_STORE", "mariadb")
	t.Setenv("BKN_TRACE_CORE_MARIADB_DSN", cfg.FormatDSN())
	t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", "false")
	index := fmt.Sprintf("bkn-2779-recovery-%d", time.Now().UnixNano())
	direct := opensearch.New(endpoint, opensearch.AuthConfig{}, 5*time.Second)
	if err := direct.EnsureIndex(ctx, index, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		req, _ := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, endpoint+"/"+index, nil)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Errorf("owned index cleanup status %d", response.StatusCode)
		}
	}()
	if _, err := direct.IndexDocumentVersion(ctx, index, "receipt:unrelated", 9, []byte(`{"revision":9}`)); err != nil {
		t.Fatal(err)
	}
	var failureStatus atomic.Int32
	proxy := httputil.NewSingleHostReverseProxy(target)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status := failureStatus.Load(); status != 0 {
			w.WriteHeader(int(status))
			_, _ = w.Write([]byte("private injected dependency body"))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	sink := opensearchprojection.New(opensearch.New(server.URL, opensearch.AuthConfig{}, 5*time.Second), index)
	worker := projectorsvc.NewWorker(store, sink, projectorsvc.WorkerOptions{BatchSize: 1, LeaseIOTimeout: 2 * time.Second, FullJitter: func(time.Duration) time.Duration { return time.Second }})
	seed := func(id string, age time.Duration) {
		t.Helper()
		if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			tx.AppendProjection(sessionvo.ProjectionMutation{EventID: id, AggregateType: "receipt", AggregateID: id, AggregateVersion: 1, EventType: "receipt.completed", Payload: []byte(`{"revision":1}`)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if age > 0 {
			if _, err := db.Exec("UPDATE bkn_trace_projection_outbox SET created_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL ? SECOND) WHERE event_id=?", int(age/time.Second), id); err != nil {
				t.Fatal(err)
			}
		}
	}
	due := func(id string) {
		t.Helper()
		if _, err := db.Exec("UPDATE bkn_trace_projection_outbox SET available_at=UTC_TIMESTAMP(6) WHERE event_id=? AND status='pending'", id); err != nil {
			t.Fatal(err)
		}
	}
	status := func(id string) string {
		t.Helper()
		var value string
		if err := db.QueryRow("SELECT status FROM bkn_trace_projection_outbox WHERE event_id=?", id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	dlqID := func(id string) uint64 {
		t.Helper()
		var value uint64
		if err := db.QueryRow("SELECT dlq_id FROM bkn_trace_dlq WHERE source_kind='projection' AND source_id=?", id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	auditCount := func(id string) int {
		t.Helper()
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM bkn_trace_dlq_replay_audit WHERE source_kind='projection' AND source_id=?", id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	assertDocument := func(id string, revision int) {
		t.Helper()
		doc, err := direct.GetDocument(ctx, index, "receipt:"+id)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Revision int `json:"revision"`
		}
		if json.Unmarshal(doc.Source, &body) != nil || body.Revision != revision {
			t.Fatalf("wrong document for %s: %s", id, doc.Source)
		}
	}
	seed("temporary", 0)
	failureStatus.Store(503)
	for attempt := 0; attempt < 12; attempt++ {
		due("temporary")
		result, err := worker.RunOnce(ctx)
		if err != nil || result.Retried != 1 || result.Dead != 0 {
			t.Fatalf("transient attempt %d: %+v err=%v", attempt+1, result, err)
		}
	}
	var attempts int
	if err := db.QueryRow("SELECT attempts FROM bkn_trace_projection_outbox WHERE event_id='temporary'").Scan(&attempts); err != nil || attempts != 12 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
	seed("new-healthy", 0)
	failureStatus.Store(0)
	due("temporary")
	if result, err := worker.Drain(ctx); err != nil || result.Delivered != 2 {
		t.Fatalf("recovery failed: %+v %v", result, err)
	}
	assertDocument("temporary", 1)
	assertDocument("new-healthy", 1)
	assertDocument("unrelated", 9)
	for _, id := range []string{"old-missing", "old-stale"} {
		t.Run(id, func(t *testing.T) {
			if id == "old-stale" {
				if _, err := direct.IndexDocumentVersion(ctx, index, "receipt:"+id, 3, []byte(`{"revision":3}`)); err != nil {
					t.Fatal(err)
				}
			}
			seed(id, 48*time.Hour)
			failureStatus.Store(503)
			result, err := worker.RunOnce(ctx)
			if err != nil || result.Dead != 1 {
				t.Fatalf("old unreplayed item not bounded: %+v %v", result, err)
			}
			key := dlqID(id)
			inspected, e := store.InspectProjectionDLQ(ctx, key)
			if e != nil || inspected.FailureClass != "retry_window_expired" || inspected.LastErrorCode != "opensearch_http_503" {
				t.Fatal("terminal policy and dependency status were not retained separately")
			}
			var created time.Time
			var original []byte
			if err := db.QueryRow("SELECT created_at,payload FROM bkn_trace_projection_outbox WHERE event_id=?", id).Scan(&created, &original); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := projectiondlq.Run([]string{"--id", fmt.Sprint(key)}, &out); err != nil || !strings.Contains(out.String(), `"action":"preview"`) || auditCount(id) != 0 || status(id) != "dead" {
				t.Fatalf("preview mutated state: %s %v", out.String(), err)
			}
			args := []string{"--id", fmt.Sprint(key), "--apply", "--operator", "acceptance-operator", "--reason", "dependency restored", "--repair-version", "2779-test"}
			out.Reset()
			if err := projectiondlq.Run(args, &out); err != nil || !strings.Contains(out.String(), "replay_queued") || auditCount(id) != 1 || status(id) != "pending" {
				t.Fatalf("audited replay failed: %s %v", out.String(), err)
			}
			var operator, reason, version string
			if err := db.QueryRow("SELECT replayed_by,reason,repair_version FROM bkn_trace_dlq_replay_audit WHERE source_id=?", id).Scan(&operator, &reason, &version); err != nil || operator != "acceptance-operator" || reason != "dependency restored" || version != "2779-test" {
				t.Fatal("audit fields lost")
			}
			out.Reset()
			if err := projectiondlq.Run(args, &out); err == nil || auditCount(id) != 1 {
				t.Fatal("duplicate replay accepted")
			}
			// The dependency is still down: first failure after replay gets a fresh,
			// bounded window rather than immediately dying from its immutable old age.
			result, err = worker.RunOnce(ctx)
			if err != nil || result.Retried != 1 || result.Dead != 0 {
				t.Fatalf("old replay has no recovery window: %+v %v", result, err)
			}
			var afterCreated time.Time
			var afterPayload []byte
			var afterVersion int
			if err := db.QueryRow("SELECT created_at,payload,aggregate_version FROM bkn_trace_projection_outbox WHERE event_id=?", id).Scan(&afterCreated, &afterPayload, &afterVersion); err != nil || !afterCreated.Equal(created) || !bytes.Equal(afterPayload, original) || afterVersion != 1 {
				t.Fatal("replay rewrote immutable event")
			}
			due(id)
			failureStatus.Store(0)
			result, err = worker.RunOnce(ctx)
			if err != nil || result.Delivered != 1 || status(id) != "delivered" {
				t.Fatalf("replay did not deliver: %+v %v", result, err)
			}
			expected := 1
			if id == "old-stale" {
				expected = 3
			}
			assertDocument(id, expected)
			assertDocument("unrelated", 9)
		})
	}
	seed("audit-rollback", 48*time.Hour)
	failureStatus.Store(503)
	if result, err := worker.RunOnce(ctx); err != nil || result.Dead != 1 {
		t.Fatalf("rollback fixture failed: %+v %v", result, err)
	}
	key := dlqID("audit-rollback")
	if _, err := db.Exec(`CREATE TRIGGER reject_test_replay_audit BEFORE INSERT ON bkn_trace_dlq_replay_audit FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected audit failure'`); err != nil {
		t.Fatal(err)
	}
	request := iprojectionoutbox.ReplayRequest{DLQID: key, Operator: "acceptance", Reason: "audit rollback test", RepairVersion: "test"}
	if err := store.ReplayProjectionDLQ(ctx, request); err == nil || status("audit-rollback") != "dead" || auditCount("audit-rollback") != 0 {
		t.Fatal("audit failure did not roll back the state transition")
	}
	preview, err := store.InspectProjectionDLQ(ctx, key)
	if err != nil || preview.ReplayedAt != nil {
		t.Fatal("failed audit marked entry replayed")
	}
	if _, err := db.Exec("DROP TRIGGER reject_test_replay_audit"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var successes atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if store.ReplayProjectionDLQ(ctx, request) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 || auditCount("audit-rollback") != 1 {
		t.Fatal("concurrent replay was not serialized")
	}
	failureStatus.Store(0)
	if result, err := worker.Drain(ctx); err != nil || result.Delivered != 1 {
		t.Fatalf("rollback recovery failed: %+v %v", result, err)
	}
	seed("permanent", 0)
	failureStatus.Store(400)
	if result, err := worker.RunOnce(ctx); err != nil || result.Dead != 1 || result.Retried != 0 {
		t.Fatalf("permanent failure was not terminal: %+v %v", result, err)
	}
	t.Log("12 transient failures recovered; preview is read-only; old replay window, audited atomicity/concurrency, stable versions and unrelated historical document preservation verified")
}
