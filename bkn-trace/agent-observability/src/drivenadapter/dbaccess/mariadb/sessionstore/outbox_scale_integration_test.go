// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

// Opt-in capacity experiment: kept separate from the small CI regression. A
// failed 2s assertion is reported only after all phases have logged results.
func TestProjectionOutboxScaleMariaDB(t *testing.T) {
	if os.Getenv("BKN_TRACE_OUTBOX_SCALE") != "1" {
		t.Skip("BKN_TRACE_OUTBOX_SCALE=1 is required")
	}
	workerDuration := 30 * time.Second
	if value := os.Getenv("BKN_TRACE_OUTBOX_SCALE_WORKER_DURATION"); value != "" {
		var err error
		workerDuration, err = time.ParseDuration(value)
		if err != nil || workerDuration < 10*time.Second {
			t.Fatal("BKN_TRACE_OUTBOX_SCALE_WORKER_DURATION must be at least 10s")
		}
	}
	endpoint := os.Getenv("BKN_TRACE_OUTBOX_SCALE_OPENSEARCH_URL")
	if endpoint == "" {
		t.Fatal("BKN_TRACE_OUTBOX_SCALE_OPENSEARCH_URL is required for real-sink throughput")
	}
	sizes := []int{100000, 500000}
	if value := os.Getenv("BKN_TRACE_OUTBOX_SCALE_SIZES"); value != "" {
		sizes = nil
		for _, part := range strings.Split(value, ",") {
			size, err := strconv.Atoi(part)
			if err != nil || size < 1 || size > 5000000 {
				t.Fatal("scale sizes must be integers between 1 and 5000000")
			}
			sizes = append(sizes, size)
		}
	}
	for _, pending := range sizes {
		t.Run(fmt.Sprintf("pending_%d", pending), func(t *testing.T) {
			db, store := outboxRegressionDatabase(t)
			seedOutboxScale(t, db, pending)
			explainOrderedOutboxClaim(t, db)
			for _, batch := range []int{1, 100} {
				var samples outboxScaleTiming
				iterations := 20
				if batch == 100 {
					iterations = 10
				}
				for i := 0; i < iterations; i++ {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					started := time.Now()
					items, err := store.Lease(ctx, batch, 30*time.Second)
					samples.record(time.Since(started), err)
					cancel()
					if err != nil {
						t.Logf("Lease limit=%d sample=%d failed: %v", batch, i, err)
						continue
					}
					if len(items) != batch {
						t.Fatalf("sample %d: leased %d, want %d", i, len(items), batch)
					}
					for j, item := range items {
						if len(item.Payload) == 0 || item.LeaseToken == "" || j > 0 && items[j-1].ID >= item.ID {
							t.Fatal("claim lost payload, lease token, or ID ordering")
						}
						if err := store.MarkDelivered(context.Background(), item); err != nil {
							t.Fatal(err)
						}
					}
				}
				t.Logf("LEASE_SCALE pending=%d batch=%d %s", pending, batch, samples.summary())
				if samples.failures != 0 || samples.overBudget != 0 {
					t.Errorf("pending=%d batch=%d did not consistently meet complete-transaction 2s budget", pending, batch)
				}
			}
			resetOutboxScaleQueue(t, db, pending)
			measureOutboxScaleWorker(t, db, store, "noop", outboxScaleNoopSink{}, workerDuration)
			client := opensearch.New(endpoint, opensearch.AuthConfig{}, 10*time.Second)
			alias := fmt.Sprintf("trace-outbox-scale-%d", time.Now().UnixNano())
			index := alias + "-v1"
			sink := opensearchprojection.New(client, alias)
			if err := sink.EnsureBootstrap(context.Background(), index); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				request, err := http.NewRequest(http.MethodDelete, endpoint+"/"+index, nil)
				if err != nil {
					t.Error(err)
					return
				}
				response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
				if err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = response.Body.Close() }()
				_, _ = io.Copy(io.Discard, response.Body)
				if response.StatusCode != http.StatusOK {
					t.Errorf("delete isolated index: status=%d", response.StatusCode)
				}
			})
			resetOutboxScaleQueue(t, db, pending)
			delivered := measureOutboxScaleWorker(t, db, store, "opensearch", sink, workerDuration)
			if err := client.RefreshIndex(context.Background(), alias); err != nil {
				t.Fatal(err)
			}
			count, err := client.CountDocuments(context.Background(), alias)
			if err != nil || count != uint64(delivered) {
				t.Fatalf("OpenSearch documents=%d worker delivered=%d error=%v", count, delivered, err)
			}
			t.Logf("OPENSEARCH_VERIFIED indexed_documents=%d", count)
		})
	}
}

// Reuse only synthetic fixture rows. Each sink starts at the exact requested
// backlog; resetting acknowledged samples/previous phases is outside timing.
func resetOutboxScaleQueue(t *testing.T, db *sql.DB, pending int) {
	t.Helper()
	if _, err := db.Exec(`UPDATE bkn_trace_projection_outbox
		SET status='pending',locked_until=NULL,lease_token=NULL,delivered_at=NULL,
		attempts=0,last_error_code=NULL,available_at=UTC_TIMESTAMP(6)-INTERVAL 2 DAY
		WHERE outbox_id>190148 AND status<>'pending'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE status='pending'").Scan(&count); err != nil || count != pending {
		t.Fatalf("restore fixture pending=%d want=%d error=%v", count, pending, err)
	}
}

func seedOutboxScale(t *testing.T, db *sql.DB, pending int) {
	t.Helper()
	const history = 190148
	for _, fixture := range []struct {
		count, offset int
		status        string
	}{{history, 0, "delivered"}, {pending, history, "pending"}} {
		// The sink receives distinct receipt IDs with 1.1 KiB valid JSON bodies.
		// Loading through MariaDB sequence tables is outside every measurement.
		_, err := db.Exec(fmt.Sprintf(`INSERT INTO bkn_trace_projection_outbox
			(outbox_id, aggregate_type, aggregate_id, event_type, event_id, payload, status, available_at, created_at)
			SELECT seq+?, 'receipt', CONCAT('receipt-',seq+?), 'receipt.completed', CONCAT('event-',seq+?),
			JSON_OBJECT('receipt_id',CONCAT('receipt-',seq+?), 'trace_id',CONCAT('trace-',seq+?),
			'receipt_status','completed','issued_at','2026-10-26T09:00:00Z','padding',REPEAT('x',1152)),
			?, UTC_TIMESTAMP(6)-INTERVAL 2 DAY, UTC_TIMESTAMP(6)-INTERVAL 2 DAY FROM seq_1_to_%d`, fixture.count),
			fixture.offset, fixture.offset, fixture.offset, fixture.offset, fixture.offset, fixture.status)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("ANALYZE TABLE bkn_trace_projection_outbox"); err != nil {
		t.Fatal(err)
	}
	var dataBytes, indexBytes, count, minID int64
	if err := db.QueryRow(`SELECT DATA_LENGTH,INDEX_LENGTH FROM information_schema.TABLES
		WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='bkn_trace_projection_outbox'`).Scan(&dataBytes, &indexBytes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*),MIN(outbox_id) FROM bkn_trace_projection_outbox WHERE status='pending'").Scan(&count, &minID); err != nil {
		t.Fatal(err)
	}
	if count != int64(pending) || minID != history+1 {
		t.Fatal("unexpected fixture size or first pending ID")
	}
	t.Logf("SCALE_FIXTURE terminal=%d pending=%d first_pending=%d data_bytes=%d index_bytes=%d", history, pending, minID, dataBytes, indexBytes)
}

type outboxScaleTiming struct {
	values                         []time.Duration
	total                          time.Duration
	failures, timeouts, overBudget int
}

func (m *outboxScaleTiming) record(elapsed time.Duration, err error) {
	m.values = append(m.values, elapsed)
	m.total += elapsed
	if err != nil {
		m.failures++
	}
	if errors.Is(err, context.DeadlineExceeded) {
		m.timeouts++
	}
	if elapsed >= 2*time.Second {
		m.overBudget++
	}
}

func (m *outboxScaleTiming) summary() string {
	if len(m.values) == 0 {
		return "n=0"
	}
	values := append([]time.Duration(nil), m.values...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return fmt.Sprintf("n=%d p50=%s p95=%s max=%s errors=%d deadline_timeouts=%d over_2s=%d", len(values), values[(len(values)-1)/2], values[(len(values)*95+99)/100-1], values[len(values)-1], m.failures, m.timeouts, m.overBudget)
}

// Wrappers time production Store and Sink calls, with no replacement of SQL,
// worker delivery decisions, lease fencing, or ACK transactions.
type outboxScaleStore struct {
	iprojectionoutbox.Store
	lease, ack outboxScaleTiming
}

func (s *outboxScaleStore) Lease(ctx context.Context, limit int, duration time.Duration) ([]iprojectionoutbox.Item, error) {
	started := time.Now()
	items, err := s.Store.Lease(ctx, limit, duration)
	s.lease.record(time.Since(started), err)
	return items, err
}

func (s *outboxScaleStore) MarkDelivered(ctx context.Context, item iprojectionoutbox.Item) error {
	started := time.Now()
	err := s.Store.MarkDelivered(ctx, item)
	s.ack.record(time.Since(started), err)
	return err
}

type outboxScaleSink struct {
	iprojectionoutbox.Sink
	timing outboxScaleTiming
}

func (s *outboxScaleSink) Project(ctx context.Context, item iprojectionoutbox.Item) error {
	started := time.Now()
	err := s.Sink.Project(ctx, item)
	s.timing.record(time.Since(started), err)
	return err
}

type outboxScaleNoopSink struct{}

func (outboxScaleNoopSink) Project(context.Context, iprojectionoutbox.Item) error { return nil }

func measureOutboxScaleWorker(t *testing.T, db *sql.DB, store *Store, name string, sink iprojectionoutbox.Sink, duration time.Duration) int {
	t.Helper()
	countDelivered := func() int {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE status='delivered'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := countDelivered()
	var pendingBefore int
	if err := db.QueryRow("SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE status='pending'").Scan(&pendingBefore); err != nil {
		t.Fatal(err)
	}
	measuredStore := &outboxScaleStore{Store: store}
	measuredSink := &outboxScaleSink{Sink: sink}
	worker := projectorsvc.NewWorker(measuredStore, measuredSink, projectorsvc.WorkerOptions{
		BatchSize: 1, LockDuration: 30 * time.Second, LeaseIOTimeout: 2 * time.Second,
	})
	started := time.Now()
	windowStarted, windowDelivered := started, 0
	var total projectorsvc.RunResult
	workerErrors := 0
	// Matches the busy production Drain loop: no sleep between successful
	// RunOnce calls, 1s idle/error polling. Check the measurement boundary between
	// transactions rather than cancelling the last claim and inventing a timeout.
	for time.Since(started) < duration {
		result, err := worker.RunOnce(context.Background())
		total.Leased += result.Leased
		total.Delivered += result.Delivered
		total.Retried += result.Retried
		total.Dead += result.Dead
		if err != nil {
			workerErrors++
			t.Logf("WORKER_ERROR sink=%s error=%v", name, err)
		}
		if elapsed := time.Since(windowStarted); elapsed >= 10*time.Second {
			t.Logf("WORKER_WINDOW sink=%s elapsed=%s delivered=%d rate=%.3f/s", name, elapsed, total.Delivered-windowDelivered, float64(total.Delivered-windowDelivered)/elapsed.Seconds())
			windowStarted, windowDelivered = time.Now(), total.Delivered
		}
		if err != nil || result.Leased == 0 {
			time.Sleep(time.Second)
		}
	}
	elapsed := time.Since(started)
	after := countDelivered()
	var pendingAfter int
	if err := db.QueryRow("SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE status='pending'").Scan(&pendingAfter); err != nil {
		t.Fatal(err)
	}
	t.Logf("WORKER_BACKLOG sink=%s pending_start=%d pending_end=%d", name, pendingBefore, pendingAfter)
	t.Logf("WORKER_SCALE sink=%s batch=1 elapsed=%s delivered=%d rate=%.3f/s leased=%d retried=%d dead=%d errors=%d sql_delivered_delta=%d lease_time=%.2f%% sink_time=%.2f%% ack_time=%.2f%%",
		name, elapsed, total.Delivered, float64(total.Delivered)/elapsed.Seconds(), total.Leased, total.Retried, total.Dead, workerErrors, after-before,
		100*measuredStore.lease.total.Seconds()/elapsed.Seconds(), 100*measuredSink.timing.total.Seconds()/elapsed.Seconds(), 100*measuredStore.ack.total.Seconds()/elapsed.Seconds())
	t.Logf("WORKER_LEASE sink=%s %s", name, measuredStore.lease.summary())
	t.Logf("WORKER_SINK sink=%s %s", name, measuredSink.timing.summary())
	t.Logf("WORKER_ACK sink=%s %s", name, measuredStore.ack.summary())
	if workerErrors != 0 || total.Retried != 0 || total.Dead != 0 || total.Delivered == 0 || after-before != total.Delivered || pendingBefore-pendingAfter != total.Delivered || measuredStore.lease.overBudget != 0 {
		t.Errorf("worker sink=%s did not sustain delivery within claim budget", name)
	}
	return total.Delivered
}
