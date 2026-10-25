// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionsvc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func stagedAnswerFixture(t *testing.T, clocks ...func() time.Time) (*sessionsvc.Service, *sessionstore.Store, sessionsvc.TerminateInteractionCommand) {
	t.Helper()
	ctx := context.Background()
	clock := time.Now
	if len(clocks) > 0 {
		clock = clocks[0]
	}
	store := sessionstore.NewWithClock(clock)
	svc := sessionsvc.New(store, sessionsvc.Options{DurableAnswerEnabled: true, Now: clock})
	owner := sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user"}
	conv, err := svc.EnsureCurrentConversation(ctx, sessionsvc.EnsureConversationCommand{Owner: owner, ExternalConversationKey: "answer-test", IdempotencyKey: "conversation"})
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := svc.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conv.ID, IdempotencyKey: "start"})
	if err != nil {
		t.Fatal(err)
	}
	artifact := evidencevo.EvidenceArtifact{ArtifactID: "artifact_answer_test", ArtifactType: evidencevo.ArtifactTypeResult, RequestID: "req_answer_test", InteractionID: interaction.ID, ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Content: map[string]any{"answer": "done", "precise": json.Number("18446744073709551615")}, AccountID: "user", AccountType: "user", EffectiveSubjectID: "user", ApplicationPrincipalID: "app"}
	normalized, validation := evidencevo.NormalizeArtifact(artifact)
	if len(validation) > 0 {
		t.Fatal(validation)
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	return svc, store, sessionsvc.TerminateInteractionCommand{Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted, TerminalIdempotencyKey: "finish", DeriveManifest: true, AnswerArtifact: raw, Manifest: sessionvo.ClosureManifest{Version: "3.0.0", CompletionReason: "completed"}}
}
func findAnswerTask(t *testing.T, store *sessionstore.Store) iprojectionoutbox.Item {
	t.Helper()
	items, err := store.Lease(context.Background(), 100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.EventType == sessionsvc.AnswerArtifactRequested {
			return item
		}
	}
	t.Fatal("missing answer task")
	return iprojectionoutbox.Item{}
}
func TestDurableAnswerFinishesBusinessBeforeArtifactAndReplays(t *testing.T) {
	svc, store, command := stagedAnswerFixture(t)
	ctx := context.Background()
	first, err := svc.TerminateInteraction(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if first.ExecutionStatus != sessionvo.InteractionCompleted || first.EvidenceStatus != sessionvo.EvidenceAssembling || first.ClosureManifest.AnswerArtifactPersistence.State != sessionvo.AnswerArtifactPending {
		t.Fatalf("unexpected committed finish: %+v", first)
	}
	count := store.PendingProjectionCount()
	// Same answer across a new request retains its immutable hash, not trace diagnostics.
	var artifact map[string]any
	decoder := json.NewDecoder(bytes.NewReader(command.AnswerArtifact))
	decoder.UseNumber()
	_ = decoder.Decode(&artifact)
	artifact["bkn.request.id"] = "req_answer_retry"
	raw, _ := json.Marshal(artifact)
	replay := command
	replay.AnswerArtifact = raw
	if _, err = svc.TerminateInteraction(ctx, replay); err != nil || store.PendingProjectionCount() != count {
		t.Fatalf("replay duplicated or refused: %v", err)
	}
	if _, err = svc.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: command.Owner, ConversationID: first.ConversationID, IdempotencyKey: "next"}); err != nil {
		t.Fatalf("completed business still blocks next interaction: %v", err)
	}
	task := findAnswerTask(t, store)
	handler := sessionsvc.NewAnswerArtifactHandler(svc, evidencestore.New())
	if err = handler.HandleAnswerArtifact(ctx, task); err != nil {
		t.Fatal(err)
	}
	settled, err := svc.GetInteraction(ctx, command.Owner, command.InteractionID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.EvidenceStatus != sessionvo.EvidenceComplete || settled.ExecutionStatus != sessionvo.InteractionCompleted || settled.ClosureManifest.AnswerArtifactPersistence.State != sessionvo.AnswerArtifactSettled {
		t.Fatalf("not settled: %+v", settled)
	}
	replay.AnswerArtifact = nil
	replay.Manifest.AnswerArtifactRef = first.ClosureManifest.AnswerArtifactRef
	count = store.PendingProjectionCount()
	if _, err = svc.TerminateInteraction(ctx, replay); err != nil || store.PendingProjectionCount() != count {
		t.Fatalf("settled reference replay: %v", err)
	}
	artifact["content"] = map[string]any{"answer": "changed"}
	artifact["content_hash"] = ""
	replay.AnswerArtifact, _ = json.Marshal(artifact)
	if _, err = svc.TerminateInteraction(ctx, replay); err == nil {
		t.Fatal("changed answer replaced committed answer")
	}
}

type lostAnswerACK struct {
	writer iartifactstore.Writer
	calls  int
}

func (writer *lostAnswerACK) StoreArtifact(ctx context.Context, artifact evidencevo.EvidenceArtifact) (bool, error) {
	created, err := writer.writer.StoreArtifact(ctx, artifact)
	writer.calls++
	if writer.calls == 1 && err == nil {
		return false, errors.New("answer stored but ACK lost")
	}
	return created, err
}
func TestDurableAnswerLostArtifactACKIsIdempotent(t *testing.T) {
	svc, store, command := stagedAnswerFixture(t)
	ctx := context.Background()
	if _, err := svc.TerminateInteraction(ctx, command); err != nil {
		t.Fatal(err)
	}
	writer := &lostAnswerACK{writer: evidencestore.New()}
	handler := sessionsvc.NewAnswerArtifactHandler(svc, writer)
	task := findAnswerTask(t, store)
	if err := handler.HandleAnswerArtifact(ctx, task); err == nil {
		t.Fatal("missing simulated ACK loss")
	}
	pending, _ := svc.GetInteraction(ctx, command.Owner, command.InteractionID)
	if pending.ClosureManifest.AnswerArtifactPersistence.State != sessionvo.AnswerArtifactPending {
		t.Fatal("uncertain write marked settled")
	}
	if err := handler.HandleAnswerArtifact(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := handler.HandleAnswerArtifact(ctx, task); err != nil {
		t.Fatal(err)
	}
	revisions, err := svc.ListAssemblyRevisions(ctx, command.Owner, command.InteractionID)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("duplicate settlement revision: %d %v", len(revisions), err)
	}
}
func TestDurableAnswerDeadAndPoisonRecoveryKeepBusinessCompleted(t *testing.T) {
	for _, poison := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "malformed"}[poison], func(t *testing.T) {
			svc, store, command := stagedAnswerFixture(t)
			ctx := context.Background()
			if _, err := svc.TerminateInteraction(ctx, command); err != nil {
				t.Fatal(err)
			}
			task := findAnswerTask(t, store)
			original := task
			handler := sessionsvc.NewAnswerArtifactHandler(svc, evidencestore.New())
			if poison {
				task.Payload = nil
				task.PayloadTooLarge = true
			}
			if err := handler.MarkAnswerArtifactDead(ctx, task); err != nil {
				t.Fatal(err)
			}
			current, _ := svc.GetInteraction(ctx, command.Owner, command.InteractionID)
			if current.ExecutionStatus != sessionvo.InteractionCompleted || current.EvidenceStatus != sessionvo.EvidencePartial || current.ClosureManifest.AnswerArtifactPersistence.State != sessionvo.AnswerArtifactDead {
				t.Fatalf("dead task changed business outcome: %+v", current)
			}
			revisions, _ := svc.ListAssemblyRevisions(ctx, command.Owner, command.InteractionID)
			if len(revisions) != 1 || !slices.Contains(revisions[0].PartialReasons, "answer_artifact_delivery_failed") {
				t.Fatal("missing truthful dead answer reason")
			}
			if len(current.ClosureManifest.SystemPartialReasons) != 0 {
				t.Fatal("changed immutable caller reasons")
			}
			if err := handler.HandleAnswerArtifact(ctx, original); err != nil {
				t.Fatal(err)
			}
			revisions, _ = svc.ListAssemblyRevisions(ctx, command.Owner, command.InteractionID)
			if len(revisions) != 2 || revisions[1].Completeness != sessionvo.EvidenceComplete {
				t.Fatalf("replay did not append recovery revision: %+v", revisions)
			}
		})
	}
}
func TestDurableAnswerRejectsDisabledOwnerAndBudget(t *testing.T) {
	for _, kind := range []string{"disabled", "owner", "budget", "snapshot-only"} {
		t.Run(kind, func(t *testing.T) {
			svc, store, command := stagedAnswerFixture(t)
			switch kind {
			case "disabled":
				svc = sessionsvc.New(store, sessionsvc.Options{})
			case "owner":
				var body map[string]any
				_ = json.Unmarshal(command.AnswerArtifact, &body)
				body["bkn.account.id"] = "another-user"
				command.AnswerArtifact, _ = json.Marshal(body)
			case "snapshot-only":
				var body map[string]json.RawMessage
				_ = json.Unmarshal(command.AnswerArtifact, &body)
				delete(body, "content")
				body["snapshot_ref"] = json.RawMessage(`"https://snapshot.invalid/result"`)
				command.AnswerArtifact, _ = json.Marshal(body)
			case "budget":
				command.AnswerArtifact = []byte(strings.Repeat(" ", sessionsvc.MaxAnswerArtifactBytes+1))
			}
			before := store.PendingProjectionCount()
			if _, err := svc.TerminateInteraction(context.Background(), command); err == nil || store.PendingProjectionCount() != before {
				t.Fatalf("invalid answer committed task: %v", err)
			}
			current, _ := svc.GetInteraction(context.Background(), command.Owner, command.InteractionID)
			if current.ExecutionStatus != sessionvo.InteractionActive {
				t.Fatal("invalid input changed active interaction")
			}
		})
	}
}

func TestDurableAnswerDeadlineAndLateRecoveryPreserveBusiness(t *testing.T) {
	now := time.Now().UTC()
	svc, store, command := stagedAnswerFixture(t, func() time.Time { return now })
	ctx := context.Background()
	if _, err := svc.TerminateInteraction(ctx, command); err != nil {
		t.Fatal(err)
	}
	task := findAnswerTask(t, store)
	now = now.Add(time.Hour)
	assembled, err := svc.AssembleDueInteractions(ctx, 10)
	if err != nil || len(assembled) != 1 {
		t.Fatalf("deadline: %d, %v", len(assembled), err)
	}
	current, _ := svc.GetInteraction(ctx, command.Owner, command.InteractionID)
	if current.ExecutionStatus != sessionvo.InteractionCompleted || current.EvidenceStatus != sessionvo.EvidencePartial {
		t.Fatalf("deadline changed business status: %+v", current)
	}
	revisions, _ := svc.ListAssemblyRevisions(ctx, command.Owner, command.InteractionID)
	if len(revisions) != 1 || !slices.Contains(revisions[0].PartialReasons, "answer_artifact_pending_deadline") {
		t.Fatalf("missing pending deadline reason: %+v", revisions)
	}
	if err := sessionsvc.NewAnswerArtifactHandler(svc, evidencestore.New()).HandleAnswerArtifact(ctx, task); err != nil {
		t.Fatal(err)
	}
	revisions, _ = svc.ListAssemblyRevisions(ctx, command.Owner, command.InteractionID)
	if len(revisions) != 2 || revisions[1].Completeness != sessionvo.EvidenceComplete {
		t.Fatalf("no new recovery revision: %+v", revisions)
	}
}
