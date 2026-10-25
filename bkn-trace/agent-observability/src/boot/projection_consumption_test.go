// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

type singleClaimStore struct {
	mu        sync.Mutex
	items     []iprojectionoutbox.Item
	delivered chan uint64
	idle      chan struct{}
}

func (s *singleClaimStore) Lease(ctx context.Context, limit int, _ time.Duration) ([]iprojectionoutbox.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit != 1 {
		return nil, context.Canceled
	}
	if len(s.items) == 0 {
		select {
		case s.idle <- struct{}{}:
		default:
		}
		return nil, nil
	}
	item := s.items[0]
	s.items = s.items[1:]
	return []iprojectionoutbox.Item{item}, nil
}
func (s *singleClaimStore) MarkDelivered(_ context.Context, item iprojectionoutbox.Item) error {
	s.delivered <- item.ID
	return nil
}
func (*singleClaimStore) MarkRetry(context.Context, iprojectionoutbox.Item, string, time.Time) error {
	return nil
}
func (*singleClaimStore) MoveToDLQ(context.Context, iprojectionoutbox.Item, string) error { return nil }
func TestProjectionConsumesReadyTasksWithoutIntervalBetweenClaims(t *testing.T) {
	store := &singleClaimStore{items: []iprojectionoutbox.Item{{ID: 1}, {ID: 2}, {ID: 3}}, delivered: make(chan uint64, 3), idle: make(chan struct{}, 2)}
	worker := projectorsvc.NewWorker(store, shutdownProjectionSink{}, projectorsvc.WorkerOptions{BatchSize: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); runProjectionWorker(ctx, time.Hour, worker, icoremetrics.Noop{}) }()
	for expected := uint64(1); expected <= 3; expected++ {
		select {
		case got := <-store.delivered:
			if got != expected {
				t.Fatal(got)
			}
		case <-time.After(time.Second):
			t.Fatal("ready tasks waited for the polling interval")
		}
	}
	select {
	case <-store.idle:
	case <-time.After(time.Second):
		t.Fatal("queue did not reach idle")
	}
	select {
	case <-store.idle:
		t.Fatal("empty queue was polled without backoff")
	case <-time.After(10 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not stop")
	}
}
