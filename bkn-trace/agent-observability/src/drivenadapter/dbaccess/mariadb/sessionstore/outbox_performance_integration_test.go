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
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

// The dedicated admin DSN is opt-in: fixtures and migrations only ever touch a
// uniquely named database created here, never an existing Trace database.
func outboxRegressionDatabase(t *testing.T) (*sql.DB, *Store) {
	t.Helper()
	dsn := os.Getenv("BKN_TRACE_OUTBOX_PERF_ADMIN_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_OUTBOX_PERF_ADMIN_DSN is not set")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	cfg.MultiStatements = true
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 2 * time.Minute // Fixture loading; each claim still has its own 2s context.
	cfg.WriteTimeout = 30 * time.Second
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("trace_outbox_regression_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE " + name); err != nil {
			t.Errorf("drop fixture database: %v", err)
		}
	})
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(8)
	store := New(db)
	// Use the production Core migrations, excluding the unrelated shared
	// bkn_audit schema (v031), as in the durable-answer integration fixtures.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, migration := range Migrations() {
		if migration.Version == "031" {
			continue
		}
		statements, err := splitSQLStatements(migration.SQL)
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range statements {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("isolated Core migration %s: %v", migration.Version, err)
			}
		}
	}
	var version string
	var bufferPool int64
	if err := db.QueryRow("SELECT VERSION(), @@innodb_buffer_pool_size").Scan(&version, &bufferPool); err != nil {
		t.Fatal(err)
	}
	t.Logf("MariaDB=%s buffer_pool=%d database=%s", version, bufferPool, name)
	return db, store
}

func explainOutboxClaim(t *testing.T, db *sql.DB, query string, parameters ...any) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN "+query, parameters...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	key := ""
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		args := make([]any, len(columns))
		for i := range values {
			args[i] = &values[i]
		}
		if err := rows.Scan(args...); err != nil {
			t.Fatal(err)
		}
		var fields []string
		table, rowKey, extra := "", "", ""
		for i, column := range columns {
			fields = append(fields, column+"="+values[i].String)
			if column == "key" {
				rowKey = values[i].String
			}
		}
		for i, column := range columns {
			if column == "table" {
				table = values[i].String
			}
			if column == "Extra" {
				extra = values[i].String
			}
		}
		if table == "bkn_trace_projection_outbox" {
			key = rowKey
			if strings.Contains(extra, "filesort") {
				t.Errorf("unbounded base-table filesort: %s", extra)
			}
			if strings.Contains(query, "UNION ALL") && (rowKey != "idx_bkn_trace_projection_claim_order" || !strings.Contains(extra, "Using index")) {
				t.Errorf("candidate branch must use covering claim-order index: key=%s extra=%s", rowKey, extra)
			}
		}
		t.Log("EXPLAIN outbox access: " + strings.Join(fields, " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return key
}

func timedOutboxLease(t *testing.T, store *Store, limit int) ([]iprojectionoutbox.Item, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	items, err := store.Lease(ctx, limit, 30*time.Second)
	elapsed := time.Since(started)
	if err != nil || elapsed >= 2*time.Second {
		t.Fatalf("complete Lease(limit=%d) took %s, error=%v (budget=2s)", limit, elapsed, err)
	}
	return items, elapsed
}

func TestProjectionOutboxLeaseHistoricalPerformanceMariaDB(t *testing.T) {
	db, store := outboxRegressionDatabase(t)
	// Match the incident's shape: ~190k terminal rows before the first ready
	// ID, ~14k pending, and a table larger than a 128 MiB buffer pool. MariaDB's
	// sequence tables generate fixtures without a client-side row insertion loop.
	const history = 190148
	const pending = 14213
	for _, fixture := range []struct {
		count, offset int
		status        string
	}{{190047, 0, "delivered"}, {101, 190047, "dead"}, {pending, history, "pending"}} {
		_, err := db.Exec(fmt.Sprintf(`INSERT INTO bkn_trace_projection_outbox
			(outbox_id, aggregate_type, aggregate_id, event_type, event_id, payload, status, available_at, created_at)
			SELECT seq+?, 'receipt', CONCAT('receipt-',seq+?), 'receipt.completed', CONCAT('event-',seq+?),
			CONCAT('{"padding":"',REPEAT('x',1152),'"}'), ?, UTC_TIMESTAMP(6)-INTERVAL 2 DAY,
			UTC_TIMESTAMP(6)-INTERVAL 2 DAY FROM seq_1_to_%d`, fixture.count),
			fixture.offset, fixture.offset, fixture.offset, fixture.status)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("ANALYZE TABLE bkn_trace_projection_outbox"); err != nil {
		t.Fatal(err)
	}
	var dataBytes, indexBytes int64
	if err := db.QueryRow(`SELECT DATA_LENGTH, INDEX_LENGTH FROM information_schema.TABLES
		WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='bkn_trace_projection_outbox'`).Scan(&dataBytes, &indexBytes); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture: terminal=%d pending=%d first_pending=%d data_bytes=%d index_bytes=%d", history, pending, history+1, dataBytes, indexBytes)
	explainOrderedOutboxClaim(t, db)
	var durations []time.Duration
	// Includes the first claim after loading history, repeated production-sized
	// claims, payload reads, fencing updates and COMMIT. ACKs are outside timing.
	for i := 0; i < 20; i++ {
		items, elapsed := timedOutboxLease(t, store, 1)
		durations = append(durations, elapsed)
		if len(items) != 1 || items[0].ID != uint64(history+i+1) {
			t.Fatalf("claim %d: wrong ID/order: %+v", i, items)
		}
		item := items[0]
		if !bytes.Equal(item.Payload, []byte(`{"padding":"`+strings.Repeat("x", 1152)+`"}`)) || item.LeaseToken == "" {
			t.Fatalf("claim %d: missing payload or token", i)
		}
		var status, token string
		var live bool
		if err := db.QueryRow(`SELECT status, lease_token, locked_until>UTC_TIMESTAMP(6)
			FROM bkn_trace_projection_outbox WHERE outbox_id=?`, item.ID).Scan(&status, &token, &live); err != nil {
			t.Fatal(err)
		}
		if status != "processing" || token != item.LeaseToken || !live {
			t.Fatalf("claim not committed: status=%s token=%s live=%v", status, token, live)
		}
		if err := store.MarkDelivered(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	first := durations[0]
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("complete Lease limit=1 n=%d first=%s p50=%s p95=%s max=%s", len(durations), first, durations[10], durations[18], durations[19])
	items, elapsed := timedOutboxLease(t, store, 100)
	if len(items) != 100 || items[0].ID != history+21 || items[99].ID != history+120 {
		t.Fatalf("batch claim order/count: %+v", items)
	}
	t.Logf("complete Lease limit=100: %s", elapsed)
	// Empty active set must not regress to scanning the terminal history either.
	if _, err := db.Exec("UPDATE bkn_trace_projection_outbox SET status='delivered' WHERE outbox_id>?", history); err != nil {
		t.Fatal(err)
	}
	items, elapsed = timedOutboxLease(t, store, 1)
	if len(items) != 0 {
		t.Fatalf("empty queue returned %d items", len(items))
	}
	t.Logf("complete Lease empty queue: %s", elapsed)

}

func TestProjectionOutboxLeaseConcurrencyMariaDB(t *testing.T) {
	db, store := outboxRegressionDatabase(t)
	if _, err := db.Exec(`INSERT INTO bkn_trace_projection_outbox
		(outbox_id, aggregate_type, aggregate_id, event_type, event_id, payload, status, available_at, locked_until, lease_token, created_at)
		VALUES
		(1,'receipt','1','receipt.completed','1','{}','pending',UTC_TIMESTAMP(6),NULL,NULL,UTC_TIMESTAMP(6)),
		(2,'receipt','2','receipt.completed','2','{}','pending',UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,NULL,NULL,UTC_TIMESTAMP(6)),
		(3,'receipt','3','receipt.completed','3','{}','processing',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,'live',UTC_TIMESTAMP(6)),
		(4,'receipt','4','receipt.completed','4','{}','processing',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)-INTERVAL 1 SECOND,'expired',UTC_TIMESTAMP(6)),
		(5,'receipt','5','receipt.completed','5','{}','pending',UTC_TIMESTAMP(6)-INTERVAL 1 HOUR,NULL,NULL,UTC_TIMESTAMP(6)),
		(6,'receipt','6','receipt.completed','6','{}','pending',UTC_TIMESTAMP(6)-INTERVAL 2 HOUR,NULL,NULL,UTC_TIMESTAMP(6))`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var lockedID int
	if err := tx.QueryRow("SELECT outbox_id FROM bkn_trace_projection_outbox WHERE outbox_id=1 FOR UPDATE").Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	items, _ := timedOutboxLease(t, store, 1)
	if len(items) != 1 || items[0].ID != 4 {
		t.Fatalf("must skip row lock, future availability and live lease; reclaim earliest expired ID: %+v", items)
	}
	if err := store.MarkDelivered(context.Background(), iprojectionoutbox.Item{ID: 4, LeaseToken: "expired"}); !errors.Is(err, iprojectionoutbox.ErrLeaseLost) {
		t.Fatalf("stale ACK accepted: %v", err)
	}
	if err := store.MarkDelivered(context.Background(), items[0]); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	items, _ = timedOutboxLease(t, store, 1)
	if len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("must claim earlier ID after row unlock: %+v", items)
	}
	stale := items[0]
	if _, err := db.Exec("UPDATE bkn_trace_projection_outbox SET locked_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE outbox_id=1"); err != nil {
		t.Fatal(err)
	}
	items, _ = timedOutboxLease(t, store, 1)
	if len(items) != 1 || items[0].ID != 1 || items[0].LeaseToken == stale.LeaseToken {
		t.Fatalf("expired claim must acquire a fresh token: %+v", items)
	}
	for _, err := range []error{
		store.MarkDelivered(context.Background(), stale),
		store.MarkRetry(context.Background(), stale, "stale", time.Now()),
		store.MoveToDLQ(context.Background(), stale, "stale"),
	} {
		if !errors.Is(err, iprojectionoutbox.ErrLeaseLost) {
			t.Fatalf("stale worker changed reclaimed row: %v", err)
		}
	}
	// Two simultaneous workers must get disjoint remaining IDs. Lock duration
	// keeps their committed leases ineligible even after SQL row locks release.
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([][]iprojectionoutbox.Item, 2)
	errorsByWorker := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			// SKIP LOCKED may legitimately return an empty batch while the other
			// claim is sorting/locking candidates. Retry after its commit, just
			// as the worker polls again; both must make progress within 2s.
			for len(results[i]) == 0 && errorsByWorker[i] == nil {
				results[i], errorsByWorker[i] = store.Lease(ctx, 1, 30*time.Second)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	seen := map[uint64]bool{}
	for i, result := range results {
		if errorsByWorker[i] != nil || len(result) != 1 {
			t.Fatalf("worker %d: items=%+v error=%v", i, result, errorsByWorker[i])
		}
		seen[result[0].ID] = true
	}
	if len(seen) != 2 || !seen[5] || !seen[6] {
		t.Fatalf("workers did not claim disjoint eligible rows: %v", seen)
	}
}

func explainOrderedOutboxClaim(t *testing.T, db *sql.DB) {
	t.Helper()
	asOf := time.Now().UTC()
	key := explainOutboxClaim(t, db, leaseProjectionOutboxCandidatesSQL,
		uint64(0), asOf, asOf, projectionOutboxCandidatePageSize,
		uint64(0), asOf, asOf, projectionOutboxCandidatePageSize,
		projectionOutboxCandidatePageSize)
	if key != "idx_bkn_trace_projection_claim_order" {
		t.Errorf("candidate index=%q", key)
	}
	key = explainOutboxClaim(t, db, leaseProjectionOutboxRowsSQL(2), uint64(190149), uint64(190150), asOf, asOf, 1)
	if key != "PRIMARY" {
		t.Errorf("bounded locking index=%q", key)
	}
}
