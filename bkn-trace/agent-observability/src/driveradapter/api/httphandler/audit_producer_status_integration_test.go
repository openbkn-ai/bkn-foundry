// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package httphandler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditproducersvc"
	sqlstore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/auditpublisherstatus"
)

// The HTTP server provides the Safe wire contract; the monitor and gap storage
// are real implementations. This is not a live Kafka fault-injection test.
func TestAuditPublisherObservationPersistsGapAcrossRecoveryAndRestart(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("fresh isolated writable MariaDB required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sqlstore.New(db)
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	const sourceID = "audit-ledger"
	const gapID = "bkn-safe-publisher-gap"
	if _, found, err := store.Get(ctx, sourceID, gapID); err != nil || found {
		t.Fatalf("test requires a fresh isolated gap scope: found=%v err=%v", found, err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM bkn_trace_log_source_coverage WHERE source_id=? AND deployment_id=?", sourceID, gapID)
	})
	clock := time.Now().UTC()
	snapshot := auditproducersvc.Snapshot{SchemaVersion: "1.0", InstanceID: "safe-instance-a", ObservedAt: clock, Status: "unknown", Reason: "delivery_not_observed"}
	var mu sync.Mutex
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/internal/audit/publish-status" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		copy := snapshot
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(copy)
	}))
	defer server.Close()
	reader, err := auditpublisherstatus.New(server.URL + "/internal/audit/publish-status")
	if err != nil {
		t.Fatal(err)
	}
	newMonitor := func() *auditproducersvc.Service {
		return auditproducersvc.New(store, reader, auditproducersvc.Options{Now: func() time.Time { return clock }})
	}
	monitor := newMonitor()
	if err = monitor.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	source := NewAuditLedgerSource(nil, monitor)
	if status := source.Metadata(); status.StatusScope != "query" || status.Status != "healthy" || status.CollectionStatus.Status != "unknown" || status.CollectionStatus.HistoricalGap != nil {
		t.Fatalf("idle must not imply failure or completeness: %+v", status)
	}
	clock = clock.Add(time.Second)
	failureAt := clock
	mu.Lock()
	snapshot.ObservedAt = clock
	snapshot.Status = "degraded"
	snapshot.Reason = "publisher_unavailable"
	snapshot.DroppedRecords = 3
	snapshot.LastFailureAt = &failureAt
	mu.Unlock()
	if err = monitor.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	degraded := source.Metadata().CollectionStatus
	if degraded.Status != "degraded" || degraded.HistoricalGap == nil || !*degraded.HistoricalGap || degraded.DroppedRecords == nil || *degraded.DroppedRecords != 3 {
		t.Fatalf("known producer loss missing: %+v", degraded)
	}
	before := calls.Load()
	for i := 0; i < 5; i++ {
		_ = source.Metadata()
	}
	if calls.Load() != before {
		t.Fatal("query metadata performed extra producer HTTP reads")
	}
	clock = clock.Add(time.Second)
	deliveredAt := clock
	mu.Lock()
	snapshot.ObservedAt = clock
	snapshot.Status = "healthy"
	snapshot.Reason = "delivery_observed"
	snapshot.LastDeliveredAt = &deliveredAt
	mu.Unlock()
	if err = monitor.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	recovered := source.Metadata().CollectionStatus
	if recovered.Status != "healthy" || recovered.HistoricalGap == nil || !*recovered.HistoricalGap {
		t.Fatalf("recovery erased history: %+v", recovered)
	}
	// Both AO and Safe restart. The new process's zero counter must stay zero;
	// the durable historical-gap proof must survive independently.
	clock = clock.Add(time.Second)
	newDeliveredAt := clock
	mu.Lock()
	snapshot.InstanceID = "safe-instance-b"
	snapshot.ObservedAt = clock
	snapshot.DroppedRecords = 0
	snapshot.LastFailureAt = nil
	snapshot.LastDeliveredAt = &newDeliveredAt
	mu.Unlock()
	monitor = newMonitor()
	if err = monitor.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := NewAuditLedgerSource(nil, monitor).Metadata().CollectionStatus
	if restarted.Status != "healthy" || restarted.DroppedRecords == nil || *restarted.DroppedRecords != 0 || restarted.HistoricalGap == nil || !*restarted.HistoricalGap || restarted.HistoricalGapSince == nil {
		t.Fatalf("restart mixed instance count with durable loss: %+v", restarted)
	}
	clock = clock.Add(91 * time.Second)
	stale := monitor.Status()
	if stale.Status != "unknown" || stale.DroppedRecords != nil || stale.HistoricalGap == nil || !*stale.HistoricalGap {
		t.Fatalf("stale observation certified health or erased gap: %+v", stale)
	}
	t.Logf("four bounded producer reads; metadata added none; recovery/restarts retained history; actual reads=%d", calls.Load())
}
