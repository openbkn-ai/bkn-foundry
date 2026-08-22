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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func TestStopAndDrainProjectionWorker(t *testing.T) {
	t.Parallel()

	store := &shutdownOutboxStore{
		items: []iprojectionoutbox.Item{{ID: 1, EventID: "evt-1"}},
	}
	worker := projectorsvc.NewWorker(
		store, shutdownProjectionSink{}, projectorsvc.WorkerOptions{},
	)
	workerContext, stopWorker := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-workerContext.Done()
	}()

	if err := stopAndDrainProjectionWorker(
		context.Background(), stopWorker, &workers, worker,
	); err != nil {
		t.Fatalf("stop and drain: %v", err)
	}
	if len(store.delivered) != 1 || store.delivered[0] != 1 {
		t.Fatalf("durable outbox was not drained: %#v", store.delivered)
	}
}

type shutdownOutboxStore struct {
	items     []iprojectionoutbox.Item
	delivered []uint64
}

func (s *shutdownOutboxStore) Lease(
	context.Context, int, time.Duration,
) ([]iprojectionoutbox.Item, error) {
	items := append([]iprojectionoutbox.Item(nil), s.items...)
	s.items = nil
	return items, nil
}

func (s *shutdownOutboxStore) MarkDelivered(
	_ context.Context, item iprojectionoutbox.Item,
) error {
	s.delivered = append(s.delivered, item.ID)
	return nil
}

func (*shutdownOutboxStore) MarkRetry(
	context.Context, iprojectionoutbox.Item, string, time.Time,
) error {
	return nil
}

func (*shutdownOutboxStore) MoveToDLQ(
	context.Context, iprojectionoutbox.Item, string,
) error {
	return nil
}

type shutdownProjectionSink struct{}

func (shutdownProjectionSink) Project(
	context.Context, iprojectionoutbox.Item,
) error {
	return nil
}
