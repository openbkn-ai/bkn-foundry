// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestProjectionSupervisorRetriesRebuildBeforeStartingWorker(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	workerStarted := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runProjectionSupervisor(
			ctx,
			time.Millisecond,
			func(context.Context) error {
				if attempts.Add(1) == 1 {
					return errors.New("OpenSearch unavailable")
				}
				return nil
			},
			func(context.Context) {
				close(workerStarted)
			},
			nil,
		)
	}()

	select {
	case <-workerStarted:
	case <-time.After(time.Second):
		t.Fatal("projection worker did not start after rebuild recovered")
	}
	<-done
	if attempts.Load() != 2 {
		t.Fatalf("unexpected rebuild attempts: %d", attempts.Load())
	}
}

func TestProjectionSupervisorStopsWhileRebuildIsUnavailable(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	workerStarted := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runProjectionSupervisor(
			ctx,
			time.Hour,
			func(context.Context) error {
				return errors.New("OpenSearch unavailable")
			},
			func(context.Context) {
				workerStarted <- struct{}{}
			},
			nil,
		)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("projection supervisor did not stop after cancellation")
	}
	select {
	case <-workerStarted:
		t.Fatal("projection worker started before a successful rebuild")
	default:
	}
}
