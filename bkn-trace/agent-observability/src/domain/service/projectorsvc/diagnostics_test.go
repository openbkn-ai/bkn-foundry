// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package projectorsvc_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/coremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

type projectionSinkFunc func(context.Context, iprojectionoutbox.Item) error

func (f projectionSinkFunc) Project(ctx context.Context, item iprojectionoutbox.Item) error {
	return f(ctx, item)
}

type settlementFailureStore struct{ *fakeOutboxStore }

func (s *settlementFailureStore) MarkRetry(context.Context, iprojectionoutbox.Item, string, time.Time) error {
	return iprojectionoutbox.ErrLeaseLost
}
func TestProjectionDiagnosticsCountFailuresWithoutAbortingBatchOrLeakingCause(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	items := []iprojectionoutbox.Item{}
	for i := uint32(0); i < 8; i++ {
		items = append(items, iprojectionoutbox.Item{ID: uint64(i + 1), EventID: "safe-event", AggregateType: "receipt", AggregateID: "safe-receipt", EventType: "receipt.completed", CreatedAt: now, Attempts: i, Payload: []byte("private payload"), LeaseToken: "private lease"})
	}
	items = append(items, iprojectionoutbox.Item{ID: 9, EventID: "permanent", CreatedAt: now}, iprojectionoutbox.Item{ID: 10, EventID: "healthy", CreatedAt: now})
	store := &fakeOutboxStore{items: items}
	metrics := coremetrics.New()
	sink := projectionSinkFunc(func(_ context.Context, item iprojectionoutbox.Item) error {
		if item.ID == 10 {
			return nil
		}
		if item.ID == 9 {
			return iprojectionoutbox.Permanent(errors.New("private permanent body"))
		}
		return transientProjectionError()
	})
	result, err := projectorsvc.NewWorker(store, sink, projectorsvc.WorkerOptions{Now: func() time.Time { return now }, Metrics: metrics}).Drain(context.Background())
	if err != nil || result.Retried != 8 || result.Dead != 1 || result.Delivered != 1 {
		t.Fatalf("failure aborted batch: %+v %v", result, err)
	}
	// Retry attempts 1,2,4,8 plus the permanent terminal decision.
	if strings.Count(logs.String(), "projection attempt failed") != 5 || strings.Contains(logs.String(), "private") || !strings.Contains(logs.String(), `"http_status":503`) || !strings.Contains(logs.String(), "permanent_projection_error") {
		t.Fatalf("unsafe/unbounded diagnostics: %s", logs.String())
	}
	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	for _, expected := range []string{"bkn_trace_projection_attempt_errors_total 9", "bkn_trace_projection_retries_total 8", "bkn_trace_projection_dead_total 1"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("missing counter %s: %s", expected, response.Body.String())
		}
	}
	// A lost lease must not be counted as a confirmed retry transition.
	lost := &settlementFailureStore{&fakeOutboxStore{items: []iprojectionoutbox.Item{{ID: 11, CreatedAt: now}}}}
	if _, err := projectorsvc.NewWorker(lost, &fakeProjectionSink{err: transientProjectionError()}, projectorsvc.WorkerOptions{Now: func() time.Time { return now }, Metrics: metrics}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(response.Body.String(), "bkn_trace_projection_retries_total 8") {
		t.Fatal("lost lease inflated confirmed retry count")
	}
}
