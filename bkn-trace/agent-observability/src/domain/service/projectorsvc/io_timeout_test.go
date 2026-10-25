// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package projectorsvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

type deadlineOutbox struct {
	fakeOutboxStore
	lease       func(context.Context) error
	acknowledge func(context.Context) error
}

func (s *deadlineOutbox) Lease(ctx context.Context, _ int, _ time.Duration) ([]iprojectionoutbox.Item, error) {
	if s.lease != nil {
		if err := s.lease(ctx); err != nil {
			return nil, err
		}
	}
	return s.fakeOutboxStore.Lease(ctx, 1, time.Second)
}
func (s *deadlineOutbox) MarkDelivered(ctx context.Context, item iprojectionoutbox.Item) error {
	if s.acknowledge != nil {
		if err := s.acknowledge(ctx); err != nil {
			return err
		}
	}
	return s.fakeOutboxStore.MarkDelivered(ctx, item)
}
func (s *deadlineOutbox) MarkRetry(ctx context.Context, item iprojectionoutbox.Item, code string, at time.Time) error {
	if s.acknowledge != nil {
		if err := s.acknowledge(ctx); err != nil {
			return err
		}
	}
	return s.fakeOutboxStore.MarkRetry(ctx, item, code, at)
}
func (s *deadlineOutbox) MoveToDLQ(ctx context.Context, item iprojectionoutbox.Item, code string) error {
	if s.acknowledge != nil {
		if err := s.acknowledge(ctx); err != nil {
			return err
		}
	}
	return s.fakeOutboxStore.MoveToDLQ(ctx, item, code)
}

type deadlineAnswerHandler struct{ handle, dead func(context.Context) error }

func (h deadlineAnswerHandler) HandleAnswerArtifact(ctx context.Context, _ iprojectionoutbox.Item) error {
	return h.handle(ctx)
}
func (h deadlineAnswerHandler) MarkAnswerArtifactDead(ctx context.Context, _ iprojectionoutbox.Item) error {
	if h.dead != nil {
		return h.dead(ctx)
	}
	return nil
}
func requireDeadline(t *testing.T, ctx context.Context, max time.Duration) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > max || ctx.Err() != nil {
		t.Fatalf("expected fresh bounded context: deadline=%v err=%v", deadline, ctx.Err())
	}
}
func TestAnswerTimeoutUsesFreshACKForRetryOrDead(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "dead"}[terminal], func(t *testing.T) {
			item := iprojectionoutbox.Item{ID: 1, EventID: "answer", EventType: "artifact.persist_requested"}
			if terminal {
				item.Attempts = 9
			}
			store := &deadlineOutbox{fakeOutboxStore: fakeOutboxStore{items: []iprojectionoutbox.Item{item}}}
			store.acknowledge = func(ctx context.Context) error { requireDeadline(t, ctx, time.Second); return nil }
			handler := deadlineAnswerHandler{handle: func(ctx context.Context) error { requireDeadline(t, ctx, time.Second); <-ctx.Done(); return ctx.Err() }, dead: func(ctx context.Context) error { requireDeadline(t, ctx, time.Second); return nil }}
			worker := projectorsvc.NewWorker(store, &fakeProjectionSink{}, projectorsvc.WorkerOptions{AnswerArtifactHandler: handler, LeaseIOTimeout: time.Second, AnswerTaskIOTimeout: 20 * time.Millisecond, AnswerAckIOTimeout: time.Second})
			result, err := worker.RunOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !terminal && result.Retried != 1 || terminal && result.Dead != 1 {
				t.Fatal("timed out task was not durably acknowledged", result)
			}
		})
	}
}
func TestAnswerDeliveredACKHasIndependentDeadline(t *testing.T) {
	item := iprojectionoutbox.Item{ID: 1, EventType: "artifact.persist_requested"}
	store := &deadlineOutbox{fakeOutboxStore: fakeOutboxStore{items: []iprojectionoutbox.Item{item}}, acknowledge: func(ctx context.Context) error { requireDeadline(t, ctx, time.Second); <-ctx.Done(); return ctx.Err() }}
	worker := projectorsvc.NewWorker(store, &fakeProjectionSink{}, projectorsvc.WorkerOptions{AnswerArtifactHandler: deadlineAnswerHandler{handle: func(context.Context) error { return nil }}, AnswerTaskIOTimeout: time.Second, AnswerAckIOTimeout: 20 * time.Millisecond})
	result, err := worker.RunOnce(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || result.Delivered != 0 {
		t.Fatalf("uncertain ACK claimed delivery: %+v %v", result, err)
	}
}
func TestProjectionLeaseIOTimeoutAndParentCancellation(t *testing.T) {
	store := &deadlineOutbox{lease: func(ctx context.Context) error { requireDeadline(t, ctx, time.Second); <-ctx.Done(); return ctx.Err() }}
	worker := projectorsvc.NewWorker(store, &fakeProjectionSink{}, projectorsvc.WorkerOptions{LeaseIOTimeout: 20 * time.Millisecond})
	if _, err := worker.RunOnce(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store = &deadlineOutbox{fakeOutboxStore: fakeOutboxStore{items: []iprojectionoutbox.Item{{ID: 1, EventType: "artifact.persist_requested"}}}, acknowledge: func(context.Context) error { t.Fatal("canceled worker attempted ACK"); return nil }}
	worker = projectorsvc.NewWorker(store, &fakeProjectionSink{}, projectorsvc.WorkerOptions{AnswerArtifactHandler: deadlineAnswerHandler{handle: func(context.Context) error { cancel(); return ctx.Err() }}, AnswerTaskIOTimeout: time.Second, AnswerAckIOTimeout: time.Second})
	if _, err := worker.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestNonAnswerHandlerKeepsOriginalContext(t *testing.T) {
	store := &fakeOutboxStore{items: []iprojectionoutbox.Item{{ID: 1, EventType: "historical_provenance.build_requested"}}}
	handler := &contextCheckingProvenance{t: t}
	worker := projectorsvc.NewWorker(store, &fakeProjectionSink{}, projectorsvc.WorkerOptions{HistoricalProvenanceHandler: handler, AnswerTaskIOTimeout: 20 * time.Millisecond, AnswerAckIOTimeout: 20 * time.Millisecond})
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type contextCheckingProvenance struct{ t *testing.T }

func (h *contextCheckingProvenance) HandleHistoricalProvenance(ctx context.Context, _ iprojectionoutbox.Item) error {
	if _, ok := ctx.Deadline(); ok {
		h.t.Fatal("answer deadline leaked to existing EE handler")
	}
	return nil
}

func TestLateAnswerSuccessStillRetriesWithinFreshACKBudget(t *testing.T) {
	store := &deadlineOutbox{fakeOutboxStore: fakeOutboxStore{items: []iprojectionoutbox.Item{{ID: 1, EventType: "artifact.persist_requested"}}}, acknowledge: func(ctx context.Context) error { requireDeadline(t, ctx, time.Second); return nil }}
	handler := deadlineAnswerHandler{handle: func(ctx context.Context) error { <-ctx.Done(); return nil }}
	worker := projectorsvc.NewWorker(store, &fakeProjectionSink{}, projectorsvc.WorkerOptions{AnswerArtifactHandler: handler, AnswerTaskIOTimeout: 20 * time.Millisecond, AnswerAckIOTimeout: time.Second})
	result, err := worker.RunOnce(context.Background())
	if err != nil || result.Retried != 1 || result.Delivered != 0 {
		t.Fatalf("late success falsely acknowledged: %+v %v", result, err)
	}
}
func TestAnswerDeadCallbackAndDLQShareOneACKBudget(t *testing.T) {
	store := &deadlineOutbox{fakeOutboxStore: fakeOutboxStore{items: []iprojectionoutbox.Item{{ID: 1, EventType: "artifact.persist_requested"}}}, acknowledge: func(context.Context) error {
		t.Fatal("exhausted ACK budget attempted another state transition")
		return nil
	}}
	handler := deadlineAnswerHandler{handle: func(context.Context) error { return iprojectionoutbox.Permanent(errors.New("invalid artifact")) }, dead: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	worker := projectorsvc.NewWorker(store, &fakeProjectionSink{}, projectorsvc.WorkerOptions{AnswerArtifactHandler: handler, AnswerTaskIOTimeout: time.Second, AnswerAckIOTimeout: 20 * time.Millisecond})
	result, err := worker.RunOnce(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || result.Dead != 0 {
		t.Fatalf("exhausted ACK budget claimed DLQ: %+v %v", result, err)
	}
}

// singleClaimOutbox models one ready slot and exposes body loading separately.
type singleClaimOutbox struct {
	deadlineOutbox
	load func(context.Context) ([]byte, error)
}

func (s *singleClaimOutbox) Lease(ctx context.Context, _ int, _ time.Duration) ([]iprojectionoutbox.Item, error) {
	require := s.lease
	if require != nil {
		if err := require(ctx); err != nil {
			return nil, err
		}
	}
	if len(s.items) == 0 {
		return nil, nil
	}
	item := s.items[0]
	s.items = s.items[1:]
	return []iprojectionoutbox.Item{item}, nil
}
func (s *singleClaimOutbox) LoadPayload(ctx context.Context, _ iprojectionoutbox.Item) ([]byte, error) {
	return s.load(ctx)
}

func TestAnswerBodySharesTaskBudgetWithDelivery(t *testing.T) {
	store := &singleClaimOutbox{deadlineOutbox: deadlineOutbox{fakeOutboxStore: fakeOutboxStore{items: []iprojectionoutbox.Item{{ID: 1, EventType: "artifact.persist_requested", PayloadDeferred: true}}}}}
	var taskDeadline time.Time
	store.lease = func(ctx context.Context) error { requireDeadline(t, ctx, 100*time.Millisecond); return nil }
	store.load = func(ctx context.Context) ([]byte, error) {
		requireDeadline(t, ctx, time.Second)
		taskDeadline, _ = ctx.Deadline()
		return []byte("answer"), nil
	}
	handler := payloadCheckingAnswerHandler{handle: func(ctx context.Context, item iprojectionoutbox.Item) error {
		deadline, _ := ctx.Deadline()
		if !deadline.Equal(taskDeadline) || string(item.Payload) != "answer" {
			t.Fatal("body load and delivery must share the same task budget")
		}
		return nil
	}}
	worker := projectorsvc.NewWorker(store, &fakeProjectionSink{}, projectorsvc.WorkerOptions{LeaseIOTimeout: 100 * time.Millisecond, AnswerTaskIOTimeout: time.Second, AnswerAckIOTimeout: time.Second, AnswerArtifactHandler: handler})
	result, err := worker.Drain(context.Background())
	if err != nil || result.Delivered != 1 {
		t.Fatalf("%+v %v", result, err)
	}
}

type payloadCheckingAnswerHandler struct {
	handle func(context.Context, iprojectionoutbox.Item) error
}

func (h payloadCheckingAnswerHandler) HandleAnswerArtifact(ctx context.Context, item iprojectionoutbox.Item) error {
	return h.handle(ctx, item)
}
func (h payloadCheckingAnswerHandler) MarkAnswerArtifactDead(context.Context, iprojectionoutbox.Item) error {
	return nil
}

func TestAnswerBodyReadFailureDoesNotBlockNextTask(t *testing.T) {
	for _, mode := range []string{"timeout-retry", "timeout-dead", "lost-lease"} {
		t.Run(mode, func(t *testing.T) {
			item := iprojectionoutbox.Item{ID: 1, EventType: "artifact.persist_requested", PayloadDeferred: true}
			if mode == "timeout-dead" {
				item.Attempts = 9
			}
			store := &singleClaimOutbox{deadlineOutbox: deadlineOutbox{fakeOutboxStore: fakeOutboxStore{items: []iprojectionoutbox.Item{item, {ID: 2, EventType: "interaction.completed"}}}}}
			store.load = func(ctx context.Context) ([]byte, error) {
				requireDeadline(t, ctx, time.Second)
				if mode == "lost-lease" {
					return nil, iprojectionoutbox.ErrLeaseLost
				}
				<-ctx.Done()
				return nil, ctx.Err()
			}

			handler := deadlineAnswerHandler{handle: func(context.Context) error { t.Fatal("unloaded body reached artifact storage"); return nil }}
			sink := &fakeProjectionSink{}
			worker := projectorsvc.NewWorker(store, sink, projectorsvc.WorkerOptions{AnswerArtifactHandler: handler, AnswerTaskIOTimeout: 20 * time.Millisecond, AnswerAckIOTimeout: time.Second, FullJitter: func(time.Duration) time.Duration { return time.Minute }})
			result, err := worker.Drain(context.Background())
			if err != nil || result.Delivered != 1 || sink.calls != 1 {
				t.Fatalf("later projection blocked: %+v %v", result, err)
			}
			switch mode {
			case "timeout-retry":
				if result.Retried != 1 || !store.retryAt.After(time.Now()) {
					t.Fatalf("read timeout did not enter backoff: %+v", result)
				}
			case "timeout-dead":
				if result.Dead != 1 {
					t.Fatalf("read failure cannot exhaust retries: %+v", result)
				}
			case "lost-lease":
				if result.Retried != 0 || result.Dead != 0 || len(store.delivered) != 1 || store.delivered[0] != 2 {
					t.Fatalf("stale worker recorded an answer result: %+v", result)
				}
			}
		})
	}
}
