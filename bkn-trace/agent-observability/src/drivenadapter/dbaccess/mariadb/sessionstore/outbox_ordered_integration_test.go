// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	v040 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v040"
)

func TestProjectionOutboxLeaseOrderedRefillMariaDB(t *testing.T) {
	if os.Getenv("BKN_TRACE_OUTBOX_PERF_ADMIN_DSN") == "" {
		t.Skip("BKN_TRACE_OUTBOX_PERF_ADMIN_DSN is not set")
	}
	for _, pattern := range []string{"clustered", "interleaved"} {
		t.Run(pattern, func(t *testing.T) {
			db, store := outboxRegressionDatabase(t)
			predicate := "seq<=400"
			if pattern == "interleaved" {
				predicate = "MOD(seq,2)=0"
			}
			// Two full candidate pages are locked. The status streams have
			// different ID distributions, exposing skipped-ID merge/refill bugs.
			_, err := db.Exec(fmt.Sprintf(`INSERT INTO bkn_trace_projection_outbox
				(outbox_id,aggregate_type,aggregate_id,event_type,event_id,payload,status,available_at,locked_until,lease_token,created_at)
				SELECT seq,'receipt',CONCAT('r-',seq),'receipt.completed',CONCAT('e-',seq),'{}',
				IF(%s,'pending','processing'),UTC_TIMESTAMP(6)-INTERVAL seq SECOND,
				UTC_TIMESTAMP(6)-INTERVAL 1 SECOND,'expired',UTC_TIMESTAMP(6)
				FROM seq_1_to_512`, predicate))
			if err != nil {
				t.Fatal(err)
			}
			// Match production READ COMMITTED: REPEATABLE READ range locks
			// would also lock the gap/next row (257), changing the expected set.
			tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			rows, err := tx.Query("SELECT outbox_id FROM bkn_trace_projection_outbox WHERE outbox_id<=256 FOR UPDATE")
			if err != nil {
				t.Fatal(err)
			}
			locked := 0
			for rows.Next() {
				locked++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil || locked != 256 {
				t.Fatalf("lock prefix: count=%d error=%v", locked, err)
			}
			items, elapsed := timedOutboxLease(t, store, 200)
			if len(items) != 200 {
				t.Fatalf("locked prefix must not produce a short/empty batch: %d", len(items))
			}
			for i, item := range items {
				if item.ID != uint64(257+i) || item.LeaseToken == "" || item.LeaseToken == "expired" || string(item.Payload) != "{}" {
					t.Fatalf("global ID order/fencing/payload at %d: %+v", i, item)
				}
			}
			t.Logf("locked_prefix=256 cross_status_batch=200 complete_lease=%s", elapsed)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			items, _ = timedOutboxLease(t, store, 1)
			if len(items) != 1 || items[0].ID != 1 {
				t.Fatalf("next call must revisit earlier unlocked IDs: %+v", items)
			}
		})
	}
}

func TestProjectionOutboxLeaseEligibilityMariaDB(t *testing.T) {
	db, store := outboxRegressionDatabase(t)
	_, err := db.Exec(`INSERT INTO bkn_trace_projection_outbox
		(outbox_id,aggregate_type,aggregate_id,event_type,event_id,payload,status,available_at,locked_until,created_at)
		SELECT seq,'receipt',CONCAT('r-',seq),'receipt.completed',CONCAT('e-',seq),'{}',
		IF(MOD(seq,2)=0,'processing','pending'),
		IF(seq<8193 AND MOD(seq,2)=1,UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,UTC_TIMESTAMP(6)-INTERVAL 1 SECOND),
		IF(MOD(seq,2)=0,UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,NULL),UTC_TIMESTAMP(6)
		FROM seq_1_to_8193`)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := timedOutboxLease(t, store, 1)
	if len(items) != 1 || items[0].ID != 8193 {
		t.Fatalf("must exclude delayed pending and live processing prefix: %+v", items)
	}
	if _, err := db.Exec("UPDATE bkn_trace_projection_outbox SET available_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE outbox_id=1"); err != nil {
		t.Fatal(err)
	}
	items, _ = timedOutboxLease(t, store, 1)
	if len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("newly eligible old ID must not be starved by a persisted cursor: %+v", items)
	}
}

func TestProjectionOutboxClaimIndexMigrationMariaDB(t *testing.T) {
	db, _ := outboxRegressionDatabase(t)
	if _, err := db.Exec("DROP INDEX idx_bkn_trace_projection_claim_order ON bkn_trace_projection_outbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bkn_trace_projection_outbox
		(aggregate_type,aggregate_id,event_type,event_id,payload,status,available_at,created_at)
		VALUES ('receipt','preserved','receipt.completed','preserved','{"value":42}','pending',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := db.Exec(v040.SchemaSQL()); err != nil {
			t.Fatalf("apply index migration iteration %d: %v", i, err)
		}
	}
	var payload, status string
	if err := db.QueryRow("SELECT payload,status FROM bkn_trace_projection_outbox WHERE event_id='preserved'").Scan(&payload, &status); err != nil {
		t.Fatal(err)
	}
	if payload != `{"value":42}` || status != "pending" {
		t.Fatalf("migration changed existing task: payload=%s status=%s", payload, status)
	}
	explainOrderedOutboxClaim(t, db)
}

func TestProjectionOutboxLeaseCandidateRecheckMariaDB(t *testing.T) {
	db, _ := outboxRegressionDatabase(t)
	if _, err := db.Exec(`INSERT INTO bkn_trace_projection_outbox
		(outbox_id,aggregate_type,aggregate_id,event_type,event_id,payload,status,available_at,created_at)
		SELECT seq,'receipt',CONCAT('r-',seq),'receipt.completed',CONCAT('e-',seq),'{}','pending',
		UTC_TIMESTAMP(6)-INTERVAL 1 SECOND,UTC_TIMESTAMP(6) FROM seq_1_to_4`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var asOf time.Time
	if err := tx.QueryRow("SELECT UTC_TIMESTAMP(6)").Scan(&asOf); err != nil {
		t.Fatal(err)
	}
	ids, err := projectionOutboxCandidateIDs(context.Background(), tx, 0, asOf)
	if err != nil || len(ids) != 4 {
		t.Fatalf("candidate IDs=%v error=%v", ids, err)
	}
	// If candidate discovery locks rows, this independent mutation times out.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `UPDATE bkn_trace_projection_outbox
		SET status=CASE outbox_id WHEN 1 THEN 'delivered' WHEN 3 THEN 'processing' ELSE status END,
		available_at=CASE outbox_id WHEN 2 THEN UTC_TIMESTAMP(6)+INTERVAL 1 HOUR ELSE available_at END,
		locked_until=CASE outbox_id WHEN 3 THEN UTC_TIMESTAMP(6)+INTERVAL 1 HOUR ELSE NULL END,
		lease_token=CASE outbox_id WHEN 3 THEN 'new-owner' ELSE NULL END
		WHERE outbox_id IN (1,2,3)`); err != nil {
		t.Fatalf("candidate discovery blocked another writer: %v", err)
	}
	// Exercise the exact production locking recheck, not a parallel test query.
	rows, err := tx.Query(leaseProjectionOutboxRowsSQL(4), ids[0], ids[1], ids[2], ids[3], asOf, asOf, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var id, version, size, attempts uint64
		var kind, aggregate, eventType, eventID string
		var createdAt time.Time
		if err := rows.Scan(&id, &kind, &aggregate, &version, &eventType, &eventID, &size, &attempts, &createdAt); err != nil {
			t.Fatal(err)
		}
		if id != 4 {
			t.Fatalf("stale candidate was not rechecked: id=%d", id)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != 1 {
		t.Fatalf("rechecked rows=%d error=%v", count, err)
	}
}
