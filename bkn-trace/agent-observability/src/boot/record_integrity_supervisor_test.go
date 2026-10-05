// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
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
	if !strings.Contains(output.String(), "one candidate temporarily unavailable") {
		t.Fatalf("diagnostic omitted batch cause: %s", output.String())
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
