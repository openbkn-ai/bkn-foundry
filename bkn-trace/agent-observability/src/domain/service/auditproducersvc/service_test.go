// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package auditproducersvc

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type readerFunc func(context.Context) (Snapshot, error)

func (f readerFunc) Read(ctx context.Context) (Snapshot, error) { return f(ctx) }

type gapStore struct {
	coverage         observabilityvo.SourceCoverage
	found            bool
	getErr, writeErr error
	gets, writes     int
}

func (s *gapStore) Get(_ context.Context, source, deployment string) (observabilityvo.SourceCoverage, bool, error) {
	s.gets++
	if source != gapSourceID || deployment != gapDeploymentID {
		panic("unexpected gap key")
	}
	return s.coverage, s.found, s.getErr
}
func (s *gapStore) UpsertDegraded(_ context.Context, v observabilityvo.SourceCoverage) error {
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	if !s.found {
		s.coverage = v
		s.found = true
	} else {
		if v.DroppedRecords > s.coverage.DroppedRecords {
			s.coverage.DroppedRecords = v.DroppedRecords
		}
		s.coverage.LastObservedAt = v.LastObservedAt
	}
	return nil
}
func (*gapStore) MarkHealthyAfterCatchUp(context.Context, string, string, uint64) error {
	panic("historical gap must never be cleared")
}
func healthySnapshot(now time.Time) Snapshot {
	return Snapshot{SchemaVersion: "1.0", InstanceID: "publisher-a", ObservedAt: now, Status: "healthy", Reason: "delivery_observed", LastDeliveredAt: &now}
}

func TestPublisherStatusLifecycleKeepsHistoricalGapAcrossRecoveryAndRestarts(t *testing.T) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	snapshot := healthySnapshot(now)
	var readErr error
	store := &gapStore{}
	reader := readerFunc(func(context.Context) (Snapshot, error) { return snapshot, readErr })
	service := New(store, reader, Options{Now: func() time.Time { return now }})
	ctx := context.Background()
	if err := service.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	if got := service.Status(); got.Status != "healthy" || got.HistoricalGap != nil || got.DroppedRecords == nil || *got.DroppedRecords != 0 {
		t.Fatalf("fresh status: %+v", got)
	}
	snapshot.Status, snapshot.Reason, snapshot.DroppedRecords = "degraded", "dropped_queue_full", 3
	snapshot.LastFailureAt = &now
	if err := service.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	if store.writes != 1 || store.coverage.DroppedRecords != 3 {
		t.Fatalf("unchanged count rewrote gap: writes=%d", store.writes)
	}
	firstGap := service.Status().HistoricalGapSince
	now = now.Add(time.Second)
	snapshot.ObservedAt = now
	snapshot.Status, snapshot.Reason = "healthy", "delivery_observed"
	snapshot.LastDeliveredAt = &now
	if err := service.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	got := service.Status()
	if got.Status != "healthy" || got.HistoricalGap == nil || !*got.HistoricalGap || !got.HistoricalGapSince.Equal(*firstGap) || store.writes != 1 {
		t.Fatalf("recovery erased gap: %+v", got)
	}
	// Safe process restart resets only its current absolute counter.
	snapshot = healthySnapshot(now)
	snapshot.InstanceID = "publisher-b"
	if err := service.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	got = service.Status()
	if got.DroppedRecords == nil || *got.DroppedRecords != 0 || got.HistoricalGap == nil || !*got.HistoricalGap {
		t.Fatalf("historical count leaked into new process: %+v", got)
	}
	// AO restart reloads durable gap evidence; it does not invent a total.
	service = New(store, reader, Options{Now: func() time.Time { return now }})
	if err := service.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	got = service.Status()
	if got.HistoricalGap == nil || !*got.HistoricalGap || *got.DroppedRecords != 0 {
		t.Fatalf("AO restart lost gap: %+v", got)
	}
	gets := store.gets
	// Returned pointers cannot mutate shared cached status.
	*got.HistoricalGap = false
	*got.DroppedRecords = 999
	if current := service.Status(); !*current.HistoricalGap || *current.DroppedRecords != 0 || store.gets != gets {
		t.Fatal("Status mutated cache or read dependencies")
	}
	now = now.Add(observationTTL)
	got = service.Status()
	if got.Status != "unknown" || got.Reason != "audit_producer_status_stale" || got.HistoricalGap == nil || !*got.HistoricalGap || got.DroppedRecords != nil {
		t.Fatalf("stale observation overclaimed current status: %+v", got)
	}
	readErr = errors.New("private URL or backend error")
	store.getErr = errors.New("private database detail")
	if err := service.Observe(ctx); err == nil {
		t.Fatal("transport failure missing")
	}
	got = service.Status()
	if got.Status != "unknown" || got.Reason != "audit_producer_status_unavailable" || got.HistoricalGap == nil || !*got.HistoricalGap {
		t.Fatalf("failed monitoring changed producer health or gap: %+v", got)
	}
}

func TestUnmonitoredAndFailedGapStoreNeverInventNoHistoricalGap(t *testing.T) {
	now := time.Now().UTC()
	store := &gapStore{getErr: errors.New("raw storage failure")}
	service := New(store, nil, Options{Now: func() time.Time { return now }})
	if got := service.Status(); got.Status != "unknown" || got.Reason != "audit_producer_not_monitored" || got.HistoricalGap != nil || store.gets != 0 {
		t.Fatalf("default fabricated observation: %+v", got)
	}
	if err := service.Observe(context.Background()); err != ErrGapPersistenceUnavailable {
		t.Fatalf("unsafe error: %v", err)
	}
	if service.Status().HistoricalGap != nil {
		t.Fatal("failed gap read invented false")
	}
	snapshot := healthySnapshot(now)
	snapshot.Status, snapshot.Reason, snapshot.DroppedRecords = "degraded", "dropped_closed", math.MaxUint64
	snapshot.LastFailureAt = &now
	store.getErr = nil
	store.writeErr = errors.New("secret write failure")
	service = New(store, readerFunc(func(context.Context) (Snapshot, error) { return snapshot, nil }), Options{Now: func() time.Time { return now }})
	if err := service.Observe(context.Background()); err == nil {
		t.Fatal("lost persistence failure")
	}
	if got := service.Status(); got.HistoricalGap == nil || !*got.HistoricalGap || *got.DroppedRecords != math.MaxUint64 {
		t.Fatalf("known loss vanished on write failure: %+v", got)
	}
	store.writeErr = nil
	if err := service.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.coverage.DroppedRecords != math.MaxInt64 {
		t.Fatal("durable signed count overflowed")
	}
	nilStore := New(nil, readerFunc(func(context.Context) (Snapshot, error) { return healthySnapshot(now), nil }), Options{Now: func() time.Time { return now }})
	if err := nilStore.Observe(context.Background()); err != nil || nilStore.Status().HistoricalGap != nil {
		t.Fatal("nil store invented historical completeness")
	}
}

func TestSnapshotValidationAndSafeFailureReasons(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"schema", func(v *Snapshot) { v.SchemaVersion = "2" }},
		{"empty_instance", func(v *Snapshot) { v.InstanceID = " " }},
		{"long_instance", func(v *Snapshot) { v.InstanceID = string(make([]byte, 129)) }},
		{"control_instance", func(v *Snapshot) { v.InstanceID = "safe\nsecret" }},
		{"non_ASCII_instance", func(v *Snapshot) { v.InstanceID = "实例" }},
		{"stale", func(v *Snapshot) { v.ObservedAt = now.Add(-observationTTL); v.LastDeliveredAt = &v.ObservedAt }},
		{"future", func(v *Snapshot) { v.ObservedAt = now.Add(5*time.Second + time.Nanosecond) }},
		{"not_UTC", func(v *Snapshot) { v.ObservedAt = now.In(time.FixedZone("local", 3600)) }},
		{"delivery_after_observation", func(v *Snapshot) { future := now.Add(time.Second); v.LastDeliveredAt = &future }},
		{"healthy_without_delivery", func(v *Snapshot) { v.LastDeliveredAt = nil }},
		{"healthy_after_later_failure", func(v *Snapshot) {
			earlier := now.Add(-time.Second)
			v.LastDeliveredAt = &earlier
			v.LastFailureAt = &now
			v.DroppedRecords = 1
		}},
		{"drop_after_later_delivery", func(v *Snapshot) {
			earlier := now.Add(-time.Second)
			v.Status, v.Reason, v.DroppedRecords = "degraded", "dropped_queue_full", 1
			v.LastFailureAt = &earlier
		}},
		{"unknown_with_delivery", func(v *Snapshot) { v.Status, v.Reason = "unknown", "delivery_not_observed" }},
		{"raw_reason", func(v *Snapshot) { v.Reason = "private endpoint credentials" }},
		{"invalid_status", func(v *Snapshot) { v.Status = "unavailable" }},
		{"drop_without_count", func(v *Snapshot) { v.Status, v.Reason = "degraded", "dropped_invalid"; v.LastFailureAt = &now }},
		{"drop_without_time", func(v *Snapshot) { v.Status, v.Reason, v.DroppedRecords = "degraded", "dropped_queue_full", 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := healthySnapshot(now)
			tc.mutate(&v)
			s := New(nil, readerFunc(func(context.Context) (Snapshot, error) { return v, nil }), Options{Now: func() time.Time { return now }})
			if err := s.Observe(context.Background()); !errors.Is(err, ErrObservationUnavailable) {
				t.Fatalf("invalid observation accepted: %v", err)
			}
			if got := s.Status(); got.Status != "unknown" || got.Reason != "audit_producer_status_unavailable" || got.DroppedRecords != nil {
				t.Fatalf("invalid metadata escaped: %+v", got)
			}
		})
	}
	// Read completion advances Now; an ordinary response must not be rejected
	// merely because its producer timestamp followed request initiation.
	v := healthySnapshot(now)
	s := New(nil, readerFunc(func(context.Context) (Snapshot, error) {
		now = now.Add(time.Second)
		v.ObservedAt = now
		v.LastDeliveredAt = &now
		return v, nil
	}), Options{Now: func() time.Time { return now }})
	if err := s.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	v.ObservedAt = now.Add(5 * time.Second)
	if !validSnapshot(v, now) {
		t.Fatal("permitted5s skew rejected")
	}
	for _, reason := range []string{"kafka_not_configured", "publisher_unavailable"} {
		v = Snapshot{SchemaVersion: "1.0", InstanceID: "a", ObservedAt: now, Status: "degraded", Reason: reason}
		if !validSnapshot(v, now) {
			t.Fatalf("startup state %s rejected", reason)
		}
	}
}

func TestStatusConcurrentWithObservationIsCacheOnly(t *testing.T) {
	now := time.Now().UTC()
	snapshot := healthySnapshot(now)
	s := New(nil, readerFunc(func(context.Context) (Snapshot, error) { return snapshot, nil }), Options{Now: func() time.Time { return now }})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = s.Status()
			}
		}()
	}
	for i := 0; i < 20; i++ {
		if err := s.Observe(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}
