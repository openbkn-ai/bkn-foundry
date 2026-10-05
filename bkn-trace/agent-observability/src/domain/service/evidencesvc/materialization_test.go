// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"slices"
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
	next, err := service.PersistRecordIntegrityBatch(context.Background(), "", 100)
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
	if next, err := service.PersistRecordIntegrityBatch(context.Background(), next, 100); next != "" || err != nil {
		t.Fatalf("pass did not terminate: %q %v", next, err)
	}
	snapshot := store.snapshots["a"]
	snapshot.CallFacts[0].Input = store.snapshots["b"].CallFacts[0].Input
	store.snapshots["a"] = snapshot
	if next, err := service.PersistRecordIntegrityBatch(context.Background(), "", 100); next != "a" || err != nil {
		t.Fatalf("next sweep failed retry: %q %v", next, err)
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
