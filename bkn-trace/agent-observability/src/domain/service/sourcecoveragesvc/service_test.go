package sourcecoveragesvc

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestObserveDegradesSourceWhenCollectorDropsRecords(t *testing.T) {
	store := &fakeStore{}
	service := New(store, &fakeReader{snapshots: []Snapshot{{RefusedLogs: 0}, {RefusedLogs: 3}}}, Options{
		SourceID: "otel-runtime", DeploymentID: "observability/otelcol-contrib",
	})

	if err := service.Observe(context.Background()); err != nil {
		t.Fatalf("baseline observe: %v", err)
	}
	if err := service.Observe(context.Background()); err != nil {
		t.Fatalf("loss observe: %v", err)
	}
	coverage, found, err := store.Get(context.Background(), "otel-runtime", "observability/otelcol-contrib")
	if err != nil || !found || coverage.State != observabilityvo.SourceCoverageDegraded || coverage.DroppedRecords != 3 {
		t.Fatalf("expected degraded coverage, got %+v found=%t err=%v", coverage, found, err)
	}
}

func TestObserveUsesFirstCollectorSnapshotOnlyAsBaseline(t *testing.T) {
	store := &fakeStore{}
	service := New(store, &fakeReader{snapshots: []Snapshot{{RefusedLogs: 1000, FailedLogs: 20}}}, Options{
		SourceID: "otel-runtime", DeploymentID: "observability/otelcol-contrib",
	})

	if err := service.Observe(context.Background()); err != nil {
		t.Fatalf("baseline observe: %v", err)
	}
	if _, found, _ := store.Get(context.Background(), "otel-runtime", "observability/otelcol-contrib"); found {
		t.Fatal("first snapshot must not re-count historical Collector counters")
	}
}

func TestObserveRecoversOnlyAfterTwoHealthyDrainedSamples(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{coverage: observabilityvo.SourceCoverage{
		SourceID: "otel-runtime", DeploymentID: "observability/otelcol-contrib",
		State: observabilityvo.SourceCoverageDegraded, Reason: "telemetry_dropped", DroppedRecords: 3,
		FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now, Version: 1,
	}}
	service := New(store, &fakeReader{snapshots: []Snapshot{
		{QueueSize: 512, QueueCapacity: 512},
		{QueueSize: 512, QueueCapacity: 512},
		{QueueSize: 512, QueueCapacity: 512},
	}}, Options{SourceID: "otel-runtime", DeploymentID: "observability/otelcol-contrib"})

	if err := service.Observe(context.Background()); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	if store.coverage.State != observabilityvo.SourceCoverageDegraded {
		t.Fatalf("recovered after one healthy sample: %+v", store.coverage)
	}
	if err := service.Observe(context.Background()); err != nil {
		t.Fatalf("second healthy observe: %v", err)
	}
	if err := service.Observe(context.Background()); err != nil {
		t.Fatalf("third healthy observe: %v", err)
	}
	if store.coverage.State != observabilityvo.SourceCoverageHealthy || store.coverage.RecoveredAt == nil {
		t.Fatalf("expected recovered coverage after two samples: %+v", store.coverage)
	}
}

type fakeReader struct {
	snapshots []Snapshot
}

func (reader *fakeReader) Read(context.Context) (Snapshot, error) {
	result := reader.snapshots[0]
	reader.snapshots = reader.snapshots[1:]
	return result, nil
}

type fakeStore struct {
	coverage observabilityvo.SourceCoverage
}

func (store *fakeStore) Get(_ context.Context, _, _ string) (observabilityvo.SourceCoverage, bool, error) {
	return store.coverage, store.coverage.SourceID != "", nil
}

func (store *fakeStore) UpsertDegraded(_ context.Context, coverage observabilityvo.SourceCoverage) error {
	if store.coverage.SourceID != "" {
		coverage.Version = store.coverage.Version + 1
	} else {
		coverage.Version = 1
	}
	store.coverage = coverage
	return nil
}

func (store *fakeStore) MarkHealthyAfterCatchUp(_ context.Context, _, _ string, version uint64) error {
	if store.coverage.Version != version {
		return ErrVersionConflict
	}
	now := time.Now().UTC()
	store.coverage.State = observabilityvo.SourceCoverageHealthy
	store.coverage.Reason = ""
	store.coverage.RecoveredAt = &now
	store.coverage.Version++
	return nil
}
