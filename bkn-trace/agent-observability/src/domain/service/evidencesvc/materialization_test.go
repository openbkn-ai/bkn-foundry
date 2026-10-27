// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	memorystore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestUnmaterializedIntegrityReadDoesNotInspectLiveEvidenceOrInventComplete(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	store := &integritySnapshotStore{Store: memorystore.New(), snapshot: snapshot, readErr: errors.New("live evidence must not be read")}
	ctx := context.Background()
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner})
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
	report, applicable, err := service.inspectRecordIntegrityWithScope(ctx, "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
	if err != nil || report != nil || !applicable || store.reads != 0 {
		t.Fatalf("historical report must stay unknown without live reads: report=%+v applicable=%v reads=%d err=%v", report, applicable, store.reads, err)
	}
	entries := []evidencevo.ConversationSummary{{ConversationID: "conv"}}
	if err := service.applyConversationRecordIntegrity(ctx, entries, evidencevo.QueryScope{AccountID: "user", AccountType: "user"}); err != nil || entries[0].CurrentRecordIntegrity != nil || !entries[0].RecordIntegrityCheckFailed || store.reads != 0 {
		t.Fatalf("unmaterialized round must signal absent verdict without reading live dependency: %+v reads=%d err=%v", entries, store.reads, err)
	}
}

func alignIntegrityFixtureVersions(t *testing.T, store *integritySnapshotStore) {
	t.Helper()
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		if i, found := tx.PeekInteraction(store.snapshot.Interaction.ID); found {
			store.snapshot.Interaction.IntegritySourceVersion = i.IntegritySourceVersion
		}
		for id, snapshot := range store.snapshots {
			if i, found := tx.PeekInteraction(id); found {
				snapshot.Interaction.IntegritySourceVersion = i.IntegritySourceVersion
				store.snapshots[id] = snapshot
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func materializeIntegrityFixture(t *testing.T, service *Service) {
	t.Helper()
	var store *integritySnapshotStore
	switch s := service.sessionStore.(type) {
	case *integritySnapshotStore:
		store = s
	case *integrityCandidateStore:
		store = s.integritySnapshotStore
	default:
		t.Fatalf("unsupported fixture store %T", service.sessionStore)
	}
	alignIntegrityFixtureVersions(t, store)
	if _, err := service.PersistRecordIntegrityBatch(context.Background(), "", 100); err != nil {
		t.Fatal(err)
	}
}

func TestMaterializedIntegrityGenerationThenPagesHaveNoLiveReads(t *testing.T) {
	for _, kind := range []string{"complete", "failed_call", "missing_content", "missing_outcome", "no_calls"} {
		t.Run(kind, func(t *testing.T) {
			snapshot, _, _ := integrityFixture()
			switch kind {
			case "failed_call":
				snapshot.CallFacts[0].Status = sessionvo.AttemptFailed
				snapshot.CallFacts[0].Error, snapshot.CallFacts[0].Output = snapshot.CallFacts[0].Output, nil
				snapshot.Receipts[0].Status = sessionvo.ReceiptFailed
			case "missing_content":
				snapshot.CallFacts[0].Output = nil
			case "missing_outcome":
				snapshot.CallFacts[0].Status = sessionvo.AttemptPending
				snapshot.Receipts[0].Status = sessionvo.ReceiptPending
			case "no_calls":
				snapshot.CallFacts, snapshot.Receipts, snapshot.Operations = nil, nil, nil
			}
			service := integrityServiceForSnapshot(t, snapshot)
			store := service.sessionStore.(*integritySnapshotStore)
			materializeIntegrityFixture(t, service)
			if store.reads != 1 {
				t.Fatalf("background must strictly inspect once: %d", store.reads)
			}
			if state := materializationRetryState(t, store, "int"); state.Status != isessionstore.RecordIntegritySucceeded {
				t.Fatalf("strict verdict not durably successful: %+v", state)
			}
			store.readErr = errors.New("page must not read evidence")
			scope := evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
			first, applicable, err := service.inspectRecordIntegrityWithScope(context.Background(), "int", scope)
			if err != nil || (kind == "no_calls" && (applicable || first != nil)) || (kind != "no_calls" && (first == nil || !applicable)) {
				t.Fatalf("persisted verdict: report=%+v applicable=%v err=%v", first, applicable, err)
			}
			if first != nil {
				want := "complete"
				if kind == "missing_content" || kind == "missing_outcome" {
					want = "missing"
				}
				if first.Status != want || (want == "missing" && len(first.Missing) == 0) {
					t.Fatalf("business verdict changed: %+v", first)
				}
			}
			conversations := []evidencevo.ConversationSummary{{ConversationID: "conv"}}
			interactions := []evidencevo.InteractionListSummary{{InteractionID: "int"}}
			if err := service.applyConversationRecordIntegrity(context.Background(), conversations, scope); err != nil {
				t.Fatal(err)
			}
			if err := service.applyInteractionRecordIntegrity(context.Background(), interactions, scope); err != nil {
				t.Fatal(err)
			}
			if store.reads != 1 || conversations[0].RecordIntegrityCheckFailed || interactions[0].RecordIntegrityCheckFailed {
				t.Fatalf("page read incurred evidence I/O: reads=%d conversations=%+v interactions=%+v", store.reads, conversations, interactions)
			}
			if first != nil && (!first.CheckedAt.Equal(conversations[0].CurrentRecordIntegrity.CheckedAt) || !first.CheckedAt.Equal(interactions[0].CurrentRecordIntegrity.CheckedAt)) {
				t.Fatal("page refresh fabricated a new verification time")
			}
			if report, applicable, err := service.inspectRecordIntegrityWithScope(context.Background(), "int", evidencevo.QueryScope{AccountID: "other", AccountType: "user"}); err != nil || report != nil || applicable || store.reads != 1 {
				t.Fatalf("foreign reader got report: %+v %v %v", report, applicable, err)
			}
		})
	}
}

func TestStoredIntegrityVersionAndActiveRoundCannotInventComplete(t *testing.T) {
	checked := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	interaction := sessionvo.Interaction{ExecutionStatus: sessionvo.InteractionCompleted, IntegritySourceVersion: 2,
		StoredRecordIntegrity: &sessionvo.StoredRecordIntegrity{SourceVersion: 1, Applicable: true, Report: &sessionvo.RecordIntegrity{Status: "complete", CheckedAt: checked, Scope: "registered_call_records"}}}
	if report, applicable, err := storedRecordIntegrity(interaction); err != nil || report != nil || !applicable {
		t.Fatalf("stale report became current: %+v %v %v", report, applicable, err)
	}
	interaction.ExecutionStatus = sessionvo.InteractionActive
	if report, applicable, err := storedRecordIntegrity(interaction); err != nil || report != nil || applicable {
		t.Fatalf("active round acquired a verdict: %+v %v %v", report, applicable, err)
	}
}

func TestMaterializationFailureContinuesCursorAndRetriesWithoutFalseVerdict(t *testing.T) {
	_, owner, now := integrityFixture()
	clock := now
	store := &integritySnapshotStore{Store: memorystore.NewWithClock(func() time.Time { return clock }), snapshots: map[string]sessionvo.EvidenceSnapshot{}}
	for _, id := range []string{"a", "b"} {
		snapshot, _, _ := integrityFixture()
		snapshot.Interaction.ID = id
		for i := range snapshot.Operations {
			snapshot.Operations[i].InteractionID = id
		}
		for i := range snapshot.CallFacts {
			snapshot.CallFacts[i].InteractionID = id
		}
		for i := range snapshot.Receipts {
			snapshot.Receipts[i].InteractionID = id
		}
		if id == "a" {
			snapshot.CallFacts[0].Input.Inline = []byte("invalid")
		}
		store.snapshots[id] = snapshot
	}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner})
		for _, id := range []string{"a", "b"} {
			tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	alignIntegrityFixtureVersions(t, store)
	service := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
	batch := func(after string) (string, error) {
		return service.PersistRecordIntegrityBatch(context.Background(), after, 100)
	}
	next, err := batch("")
	if next != "b" || err == nil {
		t.Fatalf("failed row blocked cursor: next=%q err=%v", next, err)
	}
	scope := evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
	if report, _, err := service.inspectRecordIntegrityWithScope(context.Background(), "a", scope); err != nil || report != nil {
		t.Fatalf("failed verification fabricated verdict: %+v %v", report, err)
	}
	if report, _, err := service.inspectRecordIntegrityWithScope(context.Background(), "b", scope); err != nil || report == nil || report.Status != "complete" {
		t.Fatalf("later row not saved: %+v %v", report, err)
	}
	if next, err := batch(next); next != "" || err != nil {
		t.Fatalf("pass did not terminate: %q %v", next, err)
	}
	reads := store.reads
	clock = clock.Add(isessionstore.RecordIntegrityBackoff(1) - time.Nanosecond)
	if next, err := batch(""); next != "" || err != nil || store.reads != reads {
		t.Fatalf("deferred candidate must advance cursor without inspection or duplicate error: next=%q reads=%d err=%v", next, store.reads, err)
	}
	// New work behind the deferred candidate must still materialize in this pass.
	newSnapshot, _, _ := integrityFixture()
	newSnapshot.Interaction.ID = "c"
	for i := range newSnapshot.Operations {
		newSnapshot.Operations[i].InteractionID = "c"
	}
	for i := range newSnapshot.CallFacts {
		newSnapshot.CallFacts[i].InteractionID = "c"
	}
	for i := range newSnapshot.Receipts {
		newSnapshot.Receipts[i].InteractionID = "c"
	}
	store.snapshots["c"] = newSnapshot
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveInteraction(sessionvo.Interaction{ID: "c", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	alignIntegrityFixtureVersions(t, store)
	if next, err := batch(""); next != "c" || err != nil || store.reads != reads+1 {
		t.Fatalf("deferred candidate blocked new work: next=%q reads=%d err=%v", next, store.reads, err)
	}
	if report, _, err := service.inspectRecordIntegrityWithScope(context.Background(), "c", scope); err != nil || report == nil || report.Status != "complete" {
		t.Fatalf("new work was not persisted: %+v err=%v", report, err)
	}
	snapshot := store.snapshots["a"]
	snapshot.CallFacts[0].Input = store.snapshots["b"].CallFacts[0].Input
	store.snapshots["a"] = snapshot
	clock = clock.Add(time.Nanosecond)
	if next, err := batch(""); next != "a" || err != nil {
		t.Fatalf("next sweep failed retry: %q %v", next, err)
	}
	if state := materializationRetryState(t, store, "a"); state.Status != isessionstore.RecordIntegritySucceeded {
		t.Fatalf("recovery not durably succeeded: %+v", state)
	}
}

func TestMaterializationUsesSnapshotVersionAndRejectsConcurrentInvalidation(t *testing.T) {
	for _, invalidate := range []bool{false, true} {
		t.Run(map[bool]string{false: "stable", true: "invalidated"}[invalidate], func(t *testing.T) {
			snapshot, owner, _ := integrityFixture()
			snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a"}
			service := integrityServiceForSnapshot(t, snapshot)
			store := service.sessionStore.(*integritySnapshotStore)
			alignIntegrityFixtureVersions(t, store)
			artifact := captureFixture("a", "rows")
			artifact.InteractionID, artifact.OperationID, artifact.RequestID, artifact.TraceID = "int", "op", "req", "trace"
			artifact.ArtifactType = evidencevo.ArtifactTypeDataResult
			artifact.AccountID, artifact.AccountType = owner.ApplicationPrincipalID, "app"
			reader := &integrityCountingArtifacts{captureArtifactReader: captureArtifactReader{hook: func(string) (iartifactstore.CaptureReadResult, error) {
				if invalidate {
					if err := store.InvalidateRecordIntegrity(context.Background(), "int", owner); err != nil {
						return iartifactstore.CaptureReadResult{}, err
					}
				}
				return iartifactstore.CaptureReadResult{Artifact: artifact, Found: true, Exists: true}, nil
			}}}
			service.artifactStore = reader
			if _, err := service.PersistRecordIntegrityBatch(context.Background(), "", 100); err != nil {
				t.Fatal(err)
			}
			if len(reader.ids) != 1 {
				t.Fatalf("strict generation did not read source: %v", reader.ids)
			}
			report, applicable, err := service.inspectRecordIntegrityWithScope(context.Background(), "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
			if err != nil || !applicable || (invalidate && report != nil) || (!invalidate && (report == nil || report.Status != "complete")) {
				t.Fatalf("CAS/report mismatch: %+v %v %v", report, applicable, err)
			}
			if len(reader.ids) != 1 || store.reads != 1 {
				t.Fatal("cached read repeated strict evidence inspection")
			}
		})
	}
}

func TestMaterializationRepeatedFailureBacksOffAndCapsDelay(t *testing.T) {
	for _, kind := range []string{"invalid_payload", "store_unavailable"} {
		t.Run(kind, func(t *testing.T) {
			snapshot, _, clock := integrityFixture()
			if kind == "invalid_payload" {
				snapshot.CallFacts[0].Input.Inline = []byte("invalid")
			}
			service := integrityServiceForSnapshot(t, snapshot)
			store := service.sessionStore.(*integritySnapshotStore)
			alignIntegrityFixtureVersions(t, store)
			store.clock = func() time.Time { return clock }
			if kind == "store_unavailable" {
				store.readErr = errors.New("store unavailable")
			}
			batch := func() (string, error) {
				return service.PersistRecordIntegrityBatch(context.Background(), "", 10)
			}
			for attempt, delay := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
				if next, err := batch(); next != "int" || err == nil || store.reads != attempt+1 {
					t.Fatalf("scheduled attempt %d: next=%q reads=%d err=%v", attempt, next, store.reads, err)
				}
				// Polling before the deadline neither re-reads evidence nor repeats errors.
				clock = clock.Add(delay - time.Nanosecond)
				for poll := 0; poll < 3; poll++ {
					if next, err := batch(); next != "" || err != nil || store.reads != attempt+1 {
						t.Fatalf("premature retry %d: next=%q reads=%d err=%v", attempt, next, store.reads, err)
					}
				}
				clock = clock.Add(time.Nanosecond)
			}
			pending, err := store.ListRecordIntegrityCandidates(context.Background(), "", 10)
			if err != nil || len(pending) != 1 {
				t.Fatalf("failure fabricated a stored verdict: %+v err=%v", pending, err)
			}
			if kind == "store_unavailable" {
				// Dependency recovery does not change the source version.
				store.readErr = nil
				if _, err := batch(); err != nil {
					t.Fatalf("dependency recovery failed: %v", err)
				}
				if state := materializationRetryState(t, store, "int"); state.Status != isessionstore.RecordIntegritySucceeded {
					t.Fatalf("recovery not durable: %+v", state)
				}
				pending, err = store.ListRecordIntegrityCandidates(context.Background(), "", 10)
				if err != nil || len(pending) != 0 {
					t.Fatalf("recovered candidate did not persist: %+v err=%v", pending, err)
				}
			}
		})
	}
}

func TestMaterializationSourceChangeBypassesBackoffAndResetsDelay(t *testing.T) {
	snapshot, owner, clock := integrityFixture()
	service := integrityServiceForSnapshot(t, snapshot)
	store := service.sessionStore.(*integritySnapshotStore)
	alignIntegrityFixtureVersions(t, store)
	store.clock = func() time.Time { return clock }
	store.readErr = errors.New("store unavailable")
	batch := func() (string, error) {
		return service.PersistRecordIntegrityBatch(context.Background(), "", 10)
	}
	if _, err := batch(); err == nil {
		t.Fatal("expected initial failure")
	}
	clock = clock.Add(isessionstore.RecordIntegrityBackoff(1))
	if _, err := batch(); err == nil {
		t.Fatal("expected second failure")
	}
	oldState := materializationRetryState(t, store, "int")
	if err := store.InvalidateRecordIntegrity(context.Background(), "int", owner); err != nil {
		t.Fatal(err)
	}
	alignIntegrityFixtureVersions(t, store)
	if _, err := batch(); err == nil || store.reads != 3 {
		t.Fatalf("changed source did not bypass backoff: reads=%d err=%v", store.reads, err)
	}
	if retry := materializationRetryState(t, store, "int"); retry.Attempts != 1 || retry.NextAttemptAt.Sub(clock) != isessionstore.RecordIntegrityBackoff(1) {
		t.Fatalf("changed source inherited old delay: %+v", retry)
	}
	preserved, found, err := store.GetRecordIntegrityRetry(context.Background(), "int", oldState.SourceVersion)
	if err != nil || !found || preserved.Status != isessionstore.RecordIntegritySuperseded || preserved.Reason != "source_changed" || preserved.Attempts != oldState.Attempts || !preserved.FirstAttemptAt.Equal(oldState.FirstAttemptAt) || !preserved.LastAttemptAt.Equal(oldState.LastAttemptAt) || !preserved.NextAttemptAt.Equal(oldState.NextAttemptAt) {
		t.Fatalf("old source retry history lost: %+v %v", preserved, err)
	}
	clock = clock.Add(isessionstore.RecordIntegrityBackoff(1))
	store.readErr = nil
	if _, err := batch(); err != nil {
		t.Fatal(err)
	}
	if state := materializationRetryState(t, store, "int"); state.Status != isessionstore.RecordIntegritySucceeded {
		t.Fatalf("changed source recovery not durable: %+v", state)
	}
}

func TestMaterializationStopsAfterEightAttemptsOrThirtyMinutes(t *testing.T) {
	for _, mode := range []string{"attempts", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			snapshot, _, clock := integrityFixture()
			service := integrityServiceForSnapshot(t, snapshot)
			store := service.sessionStore.(*integritySnapshotStore)
			store.clock = func() time.Time { return clock }
			alignIntegrityFixtureVersions(t, store)
			store.readErr = errors.New("stable dependency failure")
			batch := func() error { _, err := service.PersistRecordIntegrityBatch(context.Background(), "", 10); return err }
			if err := batch(); err == nil {
				t.Fatal("initial failure missing")
			}
			if mode == "attempts" {
				for i := 1; i < isessionstore.RecordIntegrityMaxAttempts; i++ {
					clock = materializationRetryState(t, store, "int").NextAttemptAt
					if err := batch(); err == nil {
						t.Fatalf("attempt %d failure missing", i+1)
					}
				}
			} else {
				clock = clock.Add(isessionstore.RecordIntegrityRetryDeadline)
				if err := batch(); err != nil {
					t.Fatal(err)
				}
			}
			state := materializationRetryState(t, store, "int")
			wantAttempts, wantReason := 8, "attempts_exhausted"
			if mode == "deadline" {
				wantAttempts, wantReason = 1, "retry_deadline_exceeded"
			}
			if state.Status != isessionstore.RecordIntegrityExhausted || state.Attempts != wantAttempts || state.Reason != wantReason {
				t.Fatalf("terminal state: %+v", state)
			}
			reads := store.reads
			clock = clock.Add(24 * time.Hour)
			if err := batch(); err != nil || store.reads != reads {
				t.Fatalf("exhausted source inspected again: reads=%d err=%v", store.reads, err)
			}
			report, applicable, err := service.inspectRecordIntegrityWithScope(context.Background(), "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
			if err != nil || report != nil || !applicable {
				t.Fatalf("exhaustion fabricated verdict: %+v %v %v", report, applicable, err)
			}
		})
	}
}

func TestMaterializationDeferredPageStillReachesNormalCandidate(t *testing.T) {
	snapshot, _, now := integrityFixture()
	service := integrityServiceForSnapshot(t, snapshot)
	store := service.sessionStore.(*integritySnapshotStore)
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		for _, id := range []string{"a", "b"} {
			tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	alignIntegrityFixtureVersions(t, store)
	store.clock = func() time.Time { return now }
	page, err := store.ListRecordIntegrityCandidates(context.Background(), "", 2)
	if err != nil || len(page) != 2 {
		t.Fatalf("deferred page fixture: %+v err=%v", page, err)
	}
	for _, candidate := range page {
		claim, claimed, claimErr := store.ClaimRecordIntegrityRetry(context.Background(), candidate.ID, candidate.IntegritySourceVersion)
		if claimErr != nil || !claimed {
			t.Fatalf("claim: %v %v", claimed, claimErr)
		}
		if _, finishErr := store.FinishRecordIntegrityRetry(context.Background(), claim, nil, "inspection_failed"); finishErr != nil {
			t.Fatal(finishErr)
		}
	}
	next, err := service.PersistRecordIntegrityBatch(context.Background(), "", 2)
	if err != nil || next != "int" || store.reads != 1 {
		t.Fatalf("deferred rows consumed candidate page: next=%q reads=%d err=%v", next, store.reads, err)
	}

	report, applicable, err := service.inspectRecordIntegrityWithScope(context.Background(), "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
	if err != nil || !applicable || report == nil || report.Status != "complete" {
		t.Fatalf("normal candidate was not saved: %+v err=%v", report, err)
	}
}

type blockingIntegritySnapshotStore struct {
	*integritySnapshotStore
	started chan struct{}
	release chan struct{}
}

func (s *blockingIntegritySnapshotStore) ReadEvidenceSnapshot(ctx context.Context, id string) (sessionvo.EvidenceSnapshot, bool, error) {
	close(s.started)
	select {
	case <-s.release:
		return s.integritySnapshotStore.ReadEvidenceSnapshot(ctx, id)
	case <-ctx.Done():
		return sessionvo.EvidenceSnapshot{}, false, ctx.Err()
	}
}

func TestMaterializationSlowBatchDoesNotBlockCancelledCaller(t *testing.T) {
	snapshot, _, _ := integrityFixture()
	service := integrityServiceForSnapshot(t, snapshot)
	store := service.sessionStore.(*integritySnapshotStore)
	alignIntegrityFixtureVersions(t, store)
	blocking := &blockingIntegritySnapshotStore{integritySnapshotStore: store, started: make(chan struct{}), release: make(chan struct{})}
	service.sessionStore = blocking
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := service.PersistRecordIntegrityBatch(ctx, "", 10); first <- err }()
	defer func() { close(blocking.release); <-first }()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("first batch did not enter snapshot read")
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	second := make(chan error, 1)
	go func() { _, err := service.PersistRecordIntegrityBatch(cancelled, "", 10); second <- err }()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled caller returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry mutex held across slow storage IO blocked cancellation")
	}
}

func TestMaterializationOwnerProfileAllowsAppArtifactAndRejectsForeignOwner(t *testing.T) {
	snapshot, owner, _ := integrityFixture()
	snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a"}
	service := integrityServiceForSnapshot(t, snapshot)
	store := service.sessionStore.(*integritySnapshotStore)
	alignIntegrityFixtureVersions(t, store)
	artifact := captureFixture("a", "rows")
	artifact.InteractionID, artifact.OperationID, artifact.RequestID, artifact.TraceID = "int", "op", "req", "trace"
	artifact.ArtifactType = evidencevo.ArtifactTypeDataResult
	artifact.AccountID, artifact.AccountType = owner.ApplicationPrincipalID, "app"
	reader := &materializationScopedArtifactReader{artifact: artifact}
	service.artifactStore = reader
	if _, err := service.PersistRecordIntegrityBatch(context.Background(), "", 100); err != nil {
		t.Fatalf("matching app artifact stayed pending: %v", err)
	}
	if len(reader.scopes) != 1 || !evidencevo.MatchesArtifactScope(artifact, reader.scopes[0]) {
		t.Fatalf("materialization did not pass the owner access profile: %+v", reader.scopes)
	}
	if report, applicable, err := service.inspectRecordIntegrityWithScope(context.Background(), "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"}); err != nil || !applicable || report == nil {
		t.Fatalf("matching app artifact was not materialized: report=%+v applicable=%v err=%v", report, applicable, err)
	}

	snapshot, owner, _ = integrityFixture()
	snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a"}
	service = integrityServiceForSnapshot(t, snapshot)
	store = service.sessionStore.(*integritySnapshotStore)
	alignIntegrityFixtureVersions(t, store)
	artifact.AccountID = "foreign-app"
	reader = &materializationScopedArtifactReader{artifact: artifact}
	service.artifactStore = reader
	if _, err := service.PersistRecordIntegrityBatch(context.Background(), "", 100); err == nil {
		t.Fatal("foreign app artifact was accepted")
	}
	if len(reader.scopes) != 1 || evidencevo.MatchesArtifactScope(artifact, reader.scopes[0]) {
		t.Fatalf("foreign app artifact passed owner access profile: %+v", reader.scopes)
	}
}

type materializationScopedArtifactReader struct {
	iartifactstore.ArtifactStorePort
	artifact evidencevo.EvidenceArtifact
	scopes   []evidencevo.QueryScope
}

func (r *materializationScopedArtifactReader) ReadArtifactForCapture(_ context.Context, _ string, scope evidencevo.QueryScope, _ int64) (iartifactstore.CaptureReadResult, error) {
	r.scopes = append(r.scopes, scope)
	if !evidencevo.MatchesArtifactScope(r.artifact, scope) {
		return iartifactstore.CaptureReadResult{Found: false, Exists: true}, nil
	}
	return iartifactstore.CaptureReadResult{Artifact: r.artifact, Found: true, Exists: true}, nil
}

func TestUnmaterializedIntegrityFilterReportsIncompleteResults(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	store := &integrityCandidateStore{integritySnapshotStore: &integritySnapshotStore{Store: memorystore.New(), snapshot: snapshot}}
	ctx := context.Background()
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner, Status: sessionvo.ConversationClosed, CreatedAt: now})
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now, TerminalAt: &now})
		tx.SaveOperation(snapshot.Operations[0])
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := New(evidencestore.New(), WithSessionStore(store), WithProjectionSource(&capturingProjectionSource{}), WithCurrentRecordIntegrity())
	scope := evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
	for _, filter := range []string{"complete", "missing"} {
		options := evidencevo.SummaryQueryOptions{Scope: scope, RecordIntegrity: filter}
		page, err := service.ListConversations(ctx, options)
		if err != nil || len(page.Entries) != 0 || !page.Partial || !slices.Contains(page.PartialReasons, "record_integrity_check_failed") {
			t.Fatalf("uncomputed conversation must not certify a complete filtered result: %+v %v", page, err)
		}
		options.ConversationID = "conv"
		rounds, err := service.ListInteractions(ctx, options)
		if err != nil || len(rounds.Entries) != 0 || !rounds.Partial || !slices.Contains(rounds.PartialReasons, "record_integrity_check_failed") {
			t.Fatalf("uncomputed round must not certify a complete filtered result: %+v %v", rounds, err)
		}
	}
	summary, found, err := service.GetInteractionSummary(ctx, "int", scope)
	if err != nil || !found || summary.CurrentRecordIntegrity != nil || !summary.RecordIntegrityCheckFailed {
		t.Fatalf("uncomputed detail must signal absent verdict: %+v %v", summary, err)
	}
	if store.reads != 0 {
		t.Fatalf("page performed live verification: %d", store.reads)
	}
}

func TestConversationRetainsKnownMissingWithUnmaterializedRound(t *testing.T) {
	for _, verdict := range []string{"missing", "complete"} {
		t.Run(verdict, func(t *testing.T) {
			snapshot, owner, now := integrityFixture()
			if verdict == "missing" {
				snapshot.CallFacts[0].Output = nil
			}
			store := &integrityCandidateStore{integritySnapshotStore: &integritySnapshotStore{Store: memorystore.New(), snapshot: snapshot}}
			ctx := context.Background()
			if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
				tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner, Status: sessionvo.ConversationClosed, CreatedAt: now})
				tx.SaveInteraction(sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now, TerminalAt: &now})
				tx.SaveOperation(snapshot.Operations[0])
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			service := New(evidencestore.New(), WithSessionStore(store), WithProjectionSource(&capturingProjectionSource{}), WithCurrentRecordIntegrity())
			materializeIntegrityFixture(t, service)
			if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
				tx.SaveInteraction(sessionvo.Interaction{ID: "uncomputed", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now.Add(time.Minute), TerminalAt: &now})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			scope := evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
			page, err := service.ListConversations(ctx, evidencevo.SummaryQueryOptions{Scope: scope, RecordIntegrity: verdict})
			if err != nil || !page.Partial || !slices.Contains(page.PartialReasons, "record_integrity_check_failed") {
				t.Fatalf("unknown round coverage must remain explicit: %+v %v", page, err)
			}
			if verdict == "missing" {
				if len(page.Entries) != 1 || page.Entries[0].CurrentRecordIntegrity == nil || page.Entries[0].CurrentRecordIntegrity.Status != "missing" || len(page.Entries[0].CurrentRecordIntegrity.Missing) == 0 {
					t.Fatalf("known missing was swallowed: %+v", page)
				}
			} else if len(page.Entries) != 0 {
				t.Fatalf("unknown round must not certify complete: %+v", page)
			}
			if store.reads != 1 {
				t.Fatalf("aggregation did live verification: %d", store.reads)
			}
		})
	}
}

func materializationRetryState(t *testing.T, store *integritySnapshotStore, id string) isessionstore.RecordIntegrityRetry {
	t.Helper()
	var version uint64
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		i, ok := tx.PeekInteraction(id)
		if !ok {
			t.Fatalf("missing fixture interaction %s", id)
		}
		version = i.IntegritySourceVersion
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, ok, err := store.GetRecordIntegrityRetry(context.Background(), id, version)
	if err != nil || !ok {
		t.Fatalf("missing durable retry for fixture: found=%v err=%v", ok, err)
	}
	return state
}

func TestMaterializationCancelledInspectionPersistsAttemptWithoutVerdict(t *testing.T) {
	snapshot, _, _ := integrityFixture()
	service := integrityServiceForSnapshot(t, snapshot)
	store := service.sessionStore.(*integritySnapshotStore)
	alignIntegrityFixtureVersions(t, store)
	blocking := &blockingIntegritySnapshotStore{integritySnapshotStore: store, started: make(chan struct{}), release: make(chan struct{})}
	service.sessionStore = blocking
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer close(blocking.release)
	done := make(chan error, 1)
	go func() { _, err := service.PersistRecordIntegrityBatch(ctx, "", 10); done <- err }()
	select {
	case <-blocking.started:
	case <-time.After(3 * time.Second):
		t.Fatal("inspection not entered")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled attempt did not report failure")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled inspection did not finish")
	}
	state := materializationRetryState(t, store, "int")
	if state.Status != isessionstore.RecordIntegrityRetryWait || state.Attempts != 1 || state.Reason != "attempt_canceled" || state.Token != "" {
		t.Fatalf("cancelled attempt not durably recorded: %+v", state)
	}
	report, applicable, err := service.inspectRecordIntegrityWithScope(context.Background(), "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
	if err != nil || !applicable || report != nil {
		t.Fatalf("cancelled attempt fabricated verdict: %+v %v %v", report, applicable, err)
	}
}

// A fixed byte limit cannot recover by retrying the same source generation.
// Keep its verdict unknown, but retain a useful reason and inspect it only once.
func TestMaterializationBudgetFailureStopsWithoutCertifyingOrRetrying(t *testing.T) {
	snapshot, owner, clock := integrityFixture()
	service := integrityServiceForSnapshot(t, snapshot)
	store := service.sessionStore.(*integritySnapshotStore)
	alignIntegrityFixtureVersions(t, store)
	store.clock = func() time.Time { return clock }
	originalOutput := store.snapshot.CallFacts[0].Output
	store.snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:oversized"}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("inspection wrote to artifact store: %s", r.Method)
		}
		_, _ = w.Write([]byte(strings.Repeat("x", (8<<20)+1)))
	}))
	defer server.Close()
	service.artifactStore = opensearchevidencestore.New(opensearch.NewWithHTTPClient(server.URL, opensearch.AuthConfig{}, server.Client()), "evidence")
	if _, err := service.PersistRecordIntegrityBatch(context.Background(), "", 10); !errors.Is(err, ErrRecordIntegrityInspectionDeferred) {
		t.Fatalf("missing failure phase: %v", err)
	}
	state := materializationRetryState(t, store, "int")
	if state.Status != isessionstore.RecordIntegrityExhausted || state.Attempts != 1 || state.Reason != "inspection_budget_exceeded" || state.LastFailureReason != "inspection_budget_exceeded" {
		t.Fatalf("fixed budget was treated as transient: %+v", state)
	}
	reads := store.reads
	clock = clock.Add(24 * time.Hour)
	if _, err := service.PersistRecordIntegrityBatch(context.Background(), "", 10); err != nil || store.reads != reads || requests.Load() != 1 {
		t.Fatalf("budget failure retried: reads=%d requests=%d err=%v", store.reads, requests.Load(), err)
	}
	report, applicable, err := service.inspectRecordIntegrityWithScope(context.Background(), "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
	if err != nil || report != nil || !applicable {
		t.Fatalf("budget failure certified a verdict: %+v %v %v", report, applicable, err)
	}
	if err := store.InvalidateRecordIntegrity(context.Background(), "int", owner); err != nil {
		t.Fatal(err)
	}
	alignIntegrityFixtureVersions(t, store)
	store.snapshot.CallFacts[0].Output = originalOutput
	if _, err := service.PersistRecordIntegrityBatch(context.Background(), "", 10); err != nil {
		t.Fatal(err)
	}
	if next := materializationRetryState(t, store, "int"); next.Status != isessionstore.RecordIntegritySucceeded || next.Attempts != 1 {
		t.Fatalf("new generation did not recover: %+v", next)
	}
}

// The response byte counter also caps error bodies. An oversized 503 must keep
// ordinary dependency backoff instead of being stopped as fixed content size.
func TestMaterializationOversizedHTTPFailureRemainsRetryable(t *testing.T) {
	snapshot, _, clock := integrityFixture()
	snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:oversized"}
	service := integrityServiceForSnapshot(t, snapshot)
	store := service.sessionStore.(*integritySnapshotStore)
	alignIntegrityFixtureVersions(t, store)
	store.clock = func() time.Time { return clock }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(strings.Repeat("private", (8<<20)/7+1)))
	}))
	defer server.Close()
	service.artifactStore = opensearchevidencestore.New(opensearch.NewWithHTTPClient(server.URL, opensearch.AuthConfig{}, server.Client()), "evidence")
	if _, err := service.PersistRecordIntegrityBatch(context.Background(), "", 10); !errors.Is(err, ErrRecordIntegrityInspectionDeferred) {
		t.Fatalf("failure missing: %v", err)
	}
	state := materializationRetryState(t, store, "int")
	if state.Status != isessionstore.RecordIntegrityRetryWait || state.LastFailureReason != "inspection_failed" || state.Attempts != 1 {
		t.Fatalf("upstream failure incorrectly stopped: %+v", state)
	}
}
