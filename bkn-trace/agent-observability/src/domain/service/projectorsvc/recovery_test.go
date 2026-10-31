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

type replayTimeStore struct {
	*fakeOutboxStore
	replayed time.Time
	readErr  error
	reads    int
	deadline time.Time
}

func (s *replayTimeStore) ProjectionReplayTime(ctx context.Context, _ string) (time.Time, error) {
	s.reads++
	s.deadline, _ = ctx.Deadline()
	return s.replayed, s.readErr
}
func transientProjectionError() error {
	return &iprojectionoutbox.DependencyError{Cause: errors.New("private response"), Kind: "opensearch_http_error", HTTPStatus: 503, Recoverable: true}
}
func TestTransientProjectionSurvivesAttemptLimitAndDeliversAfterRecovery(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	item := iprojectionoutbox.Item{ID: 1, EventID: "event", AggregateType: "receipt", AggregateID: "receipt", AggregateVersion: 3, CreatedAt: now.Add(-time.Minute), Attempts: 9, Payload: []byte(`{"unchanged":true}`)}
	store := &fakeOutboxStore{items: []iprojectionoutbox.Item{item}}
	sink := &fakeProjectionSink{err: transientProjectionError()}
	worker := projectorsvc.NewWorker(store, sink, projectorsvc.WorkerOptions{Now: func() time.Time { return now }, FullJitter: func(max time.Duration) time.Duration { return max / 2 }})
	result, err := worker.RunOnce(context.Background())
	if err != nil || result.Retried != 1 || result.Dead != 0 || len(store.retried) != 1 {
		t.Fatalf("premature dead: result=%+v err=%v", result, err)
	}
	retry := store.retried[0]
	retry.Attempts++
	if retry.AggregateVersion != 3 || string(retry.Payload) != string(item.Payload) {
		t.Fatal("retry rewrote immutable input")
	}
	store.items = []iprojectionoutbox.Item{retry}
	sink.err = nil
	result, err = worker.RunOnce(context.Background())
	if err != nil || result.Delivered != 1 || len(store.dead) != 0 {
		t.Fatalf("recovery did not deliver: %+v %v", result, err)
	}
}
func TestProjectionRecoveryWindowUsesExplicitReplayWithoutChangingCreation(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                 string
		replayed             time.Time
		readErr              error
		wantRetry, wantError bool
	}{
		{name: "recent replay", replayed: now.Add(-time.Minute), wantRetry: true},
		{name: "no replay"},
		{name: "expired replay", replayed: now.Add(-24 * time.Hour)},
		{name: "metadata failure", readErr: errors.New("read failed"), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := iprojectionoutbox.Item{ID: 1, EventID: "old-event", CreatedAt: now.Add(-48 * time.Hour), Attempts: 10}
			store := &replayTimeStore{fakeOutboxStore: &fakeOutboxStore{items: []iprojectionoutbox.Item{item}}, replayed: tc.replayed, readErr: tc.readErr}
			worker := projectorsvc.NewWorker(store, &fakeProjectionSink{err: transientProjectionError()}, projectorsvc.WorkerOptions{Now: func() time.Time { return now }})
			result, err := worker.RunOnce(context.Background())
			if (err != nil) != tc.wantError || (result.Retried == 1) != tc.wantRetry || store.reads != 1 {
				t.Fatalf("result=%+v err=%v reads=%d", result, err, store.reads)
			}
			if store.deadline.IsZero() {
				t.Fatal("replay metadata read has no deadline")
			}
			if tc.wantError && (len(store.dead) != 0 || len(store.retried) != 0) {
				t.Fatal("metadata error must not consume the recovery window")
			}
			if tc.wantRetry && !store.retried[0].CreatedAt.Equal(item.CreatedAt) {
				t.Fatal("replay rewrote creation time")
			}
			if !tc.wantRetry && !tc.wantError && (result.Dead != 1 || store.deadCode != "retry_window_expired") {
				t.Fatal("final deadline lost")
			}
		})
	}
}

func TestDependencyRecoveryRetainsFiniteBoundsAndOtherHandlerPolicies(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, event string
		created     time.Time
		failure     error
		wantDead    bool
	}{
		{"deadline", "receipt.completed", now.Add(-24 * time.Hour), transientProjectionError(), true},
		{"unknown age", "receipt.completed", time.Time{}, transientProjectionError(), true},
		{"permanent", "receipt.completed", now.Add(-time.Minute), iprojectionoutbox.Permanent(transientProjectionError()), true},
		{"unknown failure", "receipt.completed", now.Add(-time.Minute), errors.New("unknown"), true},
		{"fresh transient", "receipt.completed", now.Add(-time.Minute), transientProjectionError(), false},
		{"answer handler", "artifact.persist_requested", now.Add(-time.Minute), transientProjectionError(), true},
		{"revision handler", "revision.input.sealed", now.Add(-time.Minute), transientProjectionError(), true},
		{"provenance handler", "historical_provenance.build_requested", now.Add(-time.Minute), transientProjectionError(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &replayTimeStore{fakeOutboxStore: &fakeOutboxStore{items: []iprojectionoutbox.Item{{ID: 1, EventID: "test", EventType: tc.event, CreatedAt: tc.created, Attempts: 9}}}}
			opts := projectorsvc.WorkerOptions{Now: func() time.Time { return now }, FullJitter: func(time.Duration) time.Duration { return 0 }}
			switch tc.event {
			case "artifact.persist_requested":
				opts.AnswerArtifactHandler = &recoveryAnswerHandler{err: tc.failure}
			case "revision.input.sealed":
				opts.RevisionInputHandler = &fakeRevisionInputHandler{err: tc.failure}
			case "historical_provenance.build_requested":
				opts.HistoricalProvenanceHandler = &fakeHistoricalProvenanceHandler{err: tc.failure}
			}
			result, err := projectorsvc.NewWorker(store, &fakeProjectionSink{err: tc.failure}, opts).RunOnce(context.Background())
			if err != nil || (result.Dead == 1) != tc.wantDead || result.Dead+result.Retried != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !tc.wantDead && store.retryAt.Before(now.Add(time.Second)) {
				t.Fatal("extended recovery can spin without a minimum delay")
			}
			if tc.name != "deadline" && store.reads != 0 {
				t.Fatal("unnecessary replay read on healthy-age or unrelated handler")
			}
		})
	}
}

type recoveryAnswerHandler struct{ err error }

func (h *recoveryAnswerHandler) HandleAnswerArtifact(context.Context, iprojectionoutbox.Item) error {
	return h.err
}
func (h *recoveryAnswerHandler) MarkAnswerArtifactDead(context.Context, iprojectionoutbox.Item) error {
	return nil
}

func TestRecoveryBackoffDoesNotScheduleBeyondDeadline(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	store := &fakeOutboxStore{items: []iprojectionoutbox.Item{{ID: 1, CreatedAt: now.Add(-24*time.Hour + time.Millisecond), Attempts: 30}}}
	result, err := projectorsvc.NewWorker(store, &fakeProjectionSink{err: transientProjectionError()}, projectorsvc.WorkerOptions{Now: func() time.Time { return now }, FullJitter: func(time.Duration) time.Duration { return time.Minute }}).RunOnce(context.Background())
	if err != nil || result.Retried != 1 || !store.retryAt.Equal(now.Add(time.Millisecond)) {
		t.Fatalf("unbounded final retry delay: %+v %v at=%s", result, err, store.retryAt)
	}
}
