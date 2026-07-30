package projectorsvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func TestProjectionFailureIsRetriedWithoutChangingDurableEvent(t *testing.T) {
	t.Parallel()

	store := &fakeOutboxStore{items: []iprojectionoutbox.Item{{
		ID: 1, EventID: "evt-1", Payload: []byte(`{"event_id":"evt-1"}`), Attempts: 0,
		CreatedAt: time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
	}}}
	sink := &fakeProjectionSink{err: errors.New("OpenSearch unavailable")}
	worker := projectorsvc.NewWorker(store, sink, projectorsvc.WorkerOptions{
		Now:         func() time.Time { return time.Date(2026, 7, 30, 10, 1, 0, 0, time.UTC) },
		MaxAttempts: 3,
		FullJitter: func(max time.Duration) time.Duration {
			return max / 4
		},
	})

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if result.Retried != 1 || result.Delivered != 0 || len(store.retried) != 1 {
		t.Fatalf("unexpected projection result: %#v", result)
	}
	if store.retried[0].EventID != "evt-1" || store.retried[0].Attempts != 0 {
		t.Fatal("worker changed the durable projection payload")
	}
	expectedRetryAt := time.Date(2026, 7, 30, 10, 1, 0, 0, time.UTC).
		Add(500 * time.Millisecond)
	if !store.retryAt.Equal(expectedRetryAt) {
		t.Fatalf("retry did not use full-jitter delay: %s", store.retryAt)
	}
}

func TestProjectionWorkerRecordsOldestLeasedEventLag(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 10, 1, 0, 0, time.UTC)
	store := &fakeOutboxStore{items: []iprojectionoutbox.Item{{
		ID: 1, EventID: "evt-lag", Payload: []byte(`{}`), CreatedAt: now.Add(-45 * time.Second),
	}}}
	metrics := &projectionTestMetrics{gauges: make(map[string]float64)}
	worker := projectorsvc.NewWorker(store, &fakeProjectionSink{}, projectorsvc.WorkerOptions{
		Now: func() time.Time { return now }, Metrics: metrics,
	})
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("run projection worker: %v", err)
	}
	if metrics.gauges[icoremetrics.ProjectionLagSeconds] != 45 {
		t.Fatalf("unexpected projection lag: %#v", metrics.gauges)
	}
}

func TestPermanentProjectionFailureMovesDirectlyToDLQ(t *testing.T) {
	t.Parallel()

	store := &fakeOutboxStore{items: []iprojectionoutbox.Item{{ID: 1, EventID: "evt-1"}}}
	sink := &fakeProjectionSink{
		err: iprojectionoutbox.Permanent(errors.New("invalid projection document")),
	}
	worker := projectorsvc.NewWorker(store, sink, projectorsvc.WorkerOptions{MaxAttempts: 10})

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if result.Dead != 1 || result.Retried != 0 || len(store.dead) != 1 {
		t.Fatalf("permanent failure was not moved directly to DLQ: %#v", result)
	}
	if store.deadCode != "permanent_projection_error" {
		t.Fatalf("permanent failure classification was lost: %q", store.deadCode)
	}
}

func TestDrainProjectsAllCurrentlyAvailableItems(t *testing.T) {
	t.Parallel()

	store := &fakeOutboxStore{
		items: []iprojectionoutbox.Item{
			{ID: 1, EventID: "evt-1"},
			{ID: 2, EventID: "evt-2"},
			{ID: 3, EventID: "evt-3"},
		},
	}
	worker := projectorsvc.NewWorker(
		store, &fakeProjectionSink{}, projectorsvc.WorkerOptions{BatchSize: 2},
	)

	result, err := worker.Drain(context.Background())
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if result.Delivered != 3 || len(store.delivered) != 3 || result.Leased != 3 {
		t.Fatalf("drain did not project all available items: %#v", result)
	}
}

func TestRetryWindowExpiryMovesEventToDLQ(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	store := &fakeOutboxStore{items: []iprojectionoutbox.Item{{
		ID: 1, EventID: "evt-expired", CreatedAt: now.Add(-24 * time.Hour),
	}}}
	worker := projectorsvc.NewWorker(
		store,
		&fakeProjectionSink{err: errors.New("OpenSearch unavailable")},
		projectorsvc.WorkerOptions{
			Now:         func() time.Time { return now },
			MaxAttempts: 100,
			MaxRetryAge: 24 * time.Hour,
		},
	)

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if result.Dead != 1 || result.Retried != 0 {
		t.Fatalf("expired retry window did not move event to DLQ: %#v", result)
	}
	if store.deadCode != "retry_window_expired" {
		t.Fatalf("retry-window failure classification was lost: %q", store.deadCode)
	}
}

type fakeOutboxStore struct {
	items     []iprojectionoutbox.Item
	retried   []iprojectionoutbox.Item
	delivered []uint64
	dead      []uint64
	deadCode  string
	retryAt   time.Time
}

func (s *fakeOutboxStore) Lease(_ context.Context, _ int, _ time.Duration) ([]iprojectionoutbox.Item, error) {
	items := append([]iprojectionoutbox.Item(nil), s.items...)
	s.items = nil
	return items, nil
}
func (s *fakeOutboxStore) MarkDelivered(_ context.Context, item iprojectionoutbox.Item) error {
	s.delivered = append(s.delivered, item.ID)
	return nil
}
func (s *fakeOutboxStore) MarkRetry(_ context.Context, item iprojectionoutbox.Item, _ string, retryAt time.Time) error {
	s.retried = append(s.retried, item)
	s.retryAt = retryAt
	return nil
}
func (s *fakeOutboxStore) MoveToDLQ(_ context.Context, item iprojectionoutbox.Item, code string) error {
	s.dead = append(s.dead, item.ID)
	s.deadCode = code
	return nil
}

type fakeProjectionSink struct{ err error }

func (s *fakeProjectionSink) Project(context.Context, iprojectionoutbox.Item) error {
	return s.err
}

type projectionTestMetrics struct {
	gauges map[string]float64
}

func (*projectionTestMetrics) Increment(string)   {}
func (*projectionTestMetrics) Add(string, uint64) {}
func (m *projectionTestMetrics) Set(name string, value float64) {
	m.gauges[name] = value
}
