// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	memorystore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
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
	store := &integritySnapshotStore{Store: memorystore.New(), snapshots: map[string]sessionvo.EvidenceSnapshot{}}
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
	clock := now
	batch := func(after string) (string, error) {
		return service.persistRecordIntegrityBatch(context.Background(), after, 100, func() time.Time { return clock })
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
	clock = clock.Add(recordIntegrityRetryInitial - time.Nanosecond)
	if next, err := batch(""); next != "a" || err != nil || store.reads != reads {
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
	if _, exists := service.integrityRetries.entries["a"]; exists {
		t.Fatal("successful recovery retained retry state")
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
			if kind == "store_unavailable" {
				store.readErr = errors.New("store unavailable")
			}
			batch := func() (string, error) {
				return service.persistRecordIntegrityBatch(context.Background(), "", 10, func() time.Time { return clock })
			}
			for attempt, delay := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
				if next, err := batch(); next != "int" || err == nil || store.reads != attempt+1 {
					t.Fatalf("scheduled attempt %d: next=%q reads=%d err=%v", attempt, next, store.reads, err)
				}
				// Polling before the deadline neither re-reads evidence nor repeats errors.
				clock = clock.Add(delay - time.Nanosecond)
				for poll := 0; poll < 3; poll++ {
					if next, err := batch(); next != "int" || err != nil || store.reads != attempt+1 {
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
				if len(service.integrityRetries.entries) != 0 {
					t.Fatal("successful recovery did not clear backoff")
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
	store.readErr = errors.New("store unavailable")
	batch := func() (string, error) {
		return service.persistRecordIntegrityBatch(context.Background(), "", 10, func() time.Time { return clock })
	}
	if _, err := batch(); err == nil {
		t.Fatal("expected initial failure")
	}
	clock = clock.Add(recordIntegrityRetryInitial)
	if _, err := batch(); err == nil {
		t.Fatal("expected second failure")
	}
	if err := store.InvalidateRecordIntegrity(context.Background(), "int", owner); err != nil {
		t.Fatal(err)
	}
	alignIntegrityFixtureVersions(t, store)
	if _, err := batch(); err == nil || store.reads != 3 {
		t.Fatalf("changed source did not bypass backoff: reads=%d err=%v", store.reads, err)
	}
	if retry := service.integrityRetries.entries["int"]; retry.delay != recordIntegrityRetryInitial {
		t.Fatalf("changed source inherited old delay: %+v", retry)
	}
	clock = clock.Add(recordIntegrityRetryInitial)
	store.readErr = nil
	if _, err := batch(); err != nil {
		t.Fatal(err)
	}
	if len(service.integrityRetries.entries) != 0 {
		t.Fatal("successful changed source retained retry state")
	}
}

func TestMaterializationRetryStateHasBoundedCapacity(t *testing.T) {
	service := &Service{}
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	service.deferRecordIntegrityRetry("oldest", 1, now.Add(-time.Minute))
	for i := 0; i < recordIntegrityRetryLimit; i++ {
		service.deferRecordIntegrityRetry(strconv.Itoa(i), 1, now)
	}
	if len(service.integrityRetries.entries) != recordIntegrityRetryLimit {
		t.Fatalf("retry state exceeds capacity: %d", len(service.integrityRetries.entries))
	}
	if _, exists := service.integrityRetries.entries["oldest"]; exists {
		t.Fatal("did not evict earliest scheduled retry")
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
	page, err := store.ListRecordIntegrityCandidates(context.Background(), "", 2)
	if err != nil || len(page) != 2 {
		t.Fatalf("deferred page fixture: %+v err=%v", page, err)
	}
	for _, candidate := range page {
		service.deferRecordIntegrityRetry(candidate.ID, candidate.IntegritySourceVersion, now)
	}
	next, err := service.persistRecordIntegrityBatch(context.Background(), "", 2, func() time.Time { return now })
	if err != nil || next != "b" || store.reads != 0 {
		t.Fatalf("deferred page ended sweep or read evidence: next=%q reads=%d err=%v", next, store.reads, err)
	}
	next, err = service.persistRecordIntegrityBatch(context.Background(), next, 2, func() time.Time { return now })
	if err != nil || next != "int" || store.reads != 1 {
		t.Fatalf("normal candidate behind deferred page did not run: next=%q reads=%d err=%v", next, store.reads, err)
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
