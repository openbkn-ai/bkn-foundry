// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
)

type integrityBatchProbe struct {
	mu      sync.Mutex
	cursors []string
	limits  []int
	cancel  context.CancelFunc
}

func (p *integrityBatchProbe) PersistRecordIntegrityBatch(_ context.Context, cursor string, limit int) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cursors = append(p.cursors, cursor)
	p.limits = append(p.limits, limit)
	switch len(p.cursors) {
	case 1:
		return "int-10", errors.New("one candidate temporarily unavailable")
	case 2:
		return "", nil
	default:
		p.cancel()
		return "", nil
	}
}
func TestRecordIntegritySupervisorBoundedCursorAndCancellation(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &integrityBatchProbe{cancel: cancel}
	done := make(chan struct{})
	go func() { runRecordIntegritySupervisor(ctx, probe, time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	if !strings.Contains(output.String(), "record integrity materialization batch incomplete; failure_phases=unknown") {
		t.Fatalf("missing safe batch diagnostic: %s", output.String())
	}
	if strings.Contains(output.String(), "one candidate temporarily unavailable") || strings.Contains(output.String(), "int-10") {
		t.Fatalf("batch diagnostic exposed raw error or cursor: %s", output.String())
	}
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if len(probe.cursors) != 3 || probe.cursors[0] != "" || probe.cursors[1] != "int-10" || probe.cursors[2] != "" {
		t.Fatalf("failed candidates must not prevent keyset progress and subsequent revisit: %v", probe.cursors)
	}
	for _, limit := range probe.limits {
		if limit != 10 {
			t.Fatalf("unbounded batch: %d", limit)
		}
	}
}
func TestRecordIntegritySupervisorCancelledDoesNotGenerate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	probe := &integrityBatchProbe{cancel: cancel}
	runRecordIntegritySupervisor(ctx, probe, time.Millisecond)
	if len(probe.cursors) != 0 {
		t.Fatal("cancelled supervisor still read evidence")
	}
}

func TestRecordIntegritySupervisorErrorLogThrottle(t *testing.T) {
	var throttle recordIntegrityErrorLogThrottle
	start := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	failure := errors.New("private backend detail")
	cases := []struct {
		after time.Duration
		err   error
		want  bool
	}{
		{0, nil, false},
		{0, failure, true},
		{time.Minute, evidencesvc.ErrRecordIntegrityClaimFailed, false},
		{2 * time.Minute, nil, false}, // Healthy and skipped batches cannot reset the window.
		{3 * time.Minute, evidencesvc.ErrRecordIntegrityCandidateQueryFailed, false},
		{5*time.Minute - time.Nanosecond, failure, false},
		{5 * time.Minute, failure, true},
		{6 * time.Minute, nil, false},
		{7 * time.Minute, failure, false},
		{10 * time.Minute, failure, true},
	}
	for i, tc := range cases {
		if got := throttle.shouldLog(start.Add(tc.after), tc.err); got != tc.want {
			t.Fatalf("case %d at %s: got %v, want %v", i, tc.after, got, tc.want)
		}
	}
}

type cancellationIntegrityBatcher struct{ started chan context.Context }

func (p cancellationIntegrityBatcher) PersistRecordIntegrityBatch(ctx context.Context, _ string, limit int) (string, error) {
	p.started <- ctx
	<-ctx.Done()
	return "private-cursor", ctx.Err()
}

func TestRecordIntegritySupervisorCancellationStopsActiveBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan context.Context, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runRecordIntegritySupervisor(ctx, cancellationIntegrityBatcher{started}, time.Hour)
	}()
	select {
	case batchCtx := <-started:
		deadline, ok := batchCtx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Error("batch must retain its30s deadline")
		}
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("batch did not start")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled batch did not stop supervisor")
	}
}

type phaseIntegrityBatcher struct {
	failure error
	cancel  context.CancelFunc
	calls   int
}

func (p *phaseIntegrityBatcher) PersistRecordIntegrityBatch(context.Context, string, int) (string, error) {
	p.calls++
	if p.calls == 1 {
		return "private-interaction-id", p.failure
	}
	p.cancel()
	return "", nil
}

func TestRecordIntegritySupervisorLogsAllowlistedFailurePhases(t *testing.T) {
	private := errors.New("private-backend-payload private-interaction-id")
	for _, tc := range []struct {
		name   string
		err    error
		phases string
	}{
		{"query", evidencesvc.ErrRecordIntegrityCandidateQueryFailed, "query"},
		{"claim", evidencesvc.ErrRecordIntegrityClaimFailed, "claim"},
		{"persist", evidencesvc.ErrRecordIntegrityResultPersistenceFailed, "persist"},
		{"inspection", evidencesvc.ErrRecordIntegrityInspectionDeferred, "inspection"},
		{"wrapped", fmt.Errorf("private-wrapper: %w", evidencesvc.ErrRecordIntegrityClaimFailed), "claim"},
		{"joined", errors.Join(private, evidencesvc.ErrRecordIntegrityInspectionDeferred, evidencesvc.ErrRecordIntegrityResultPersistenceFailed, evidencesvc.ErrRecordIntegrityCandidateQueryFailed, evidencesvc.ErrRecordIntegrityClaimFailed, evidencesvc.ErrRecordIntegrityClaimFailed), "query,claim,persist,inspection"},
		// Text resembling a sentinel must not be guessed into a trusted category.
		{"unknown", errors.New("record integrity claim failed; private-interaction-id"), "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(previous)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			batcher := &phaseIntegrityBatcher{failure: errors.Join(private, tc.err), cancel: cancel}
			runRecordIntegritySupervisor(ctx, batcher, time.Nanosecond)
			logged := output.String()
			if !strings.Contains(logged, "failure_phases="+tc.phases+";") || !strings.Contains(logged, "infrastructure failures may not have persisted retry status") {
				t.Fatalf("missing safe failure phase/context: %s", logged)
			}
			if strings.Contains(logged, "private-") || strings.Contains(logged, "inspect persisted materialization status") {
				t.Fatalf("unsafe or misleading diagnostic: %s", logged)
			}
			if strings.Count(logged, "record integrity materialization batch incomplete") != 1 {
				t.Fatalf("unexpected log count: %s", logged)
			}
		})
	}
	if phases := recordIntegrityFailurePhases(nil); phases != "" {
		t.Fatalf("nil error produced failure phase %q", phases)
	}
}
