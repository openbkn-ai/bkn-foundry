// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionsvc_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/captureadmission"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

func TestDurableAnswerUnknownAdmissionCannotStageBody(t *testing.T) {
	_, store, command := stagedAnswerFixture(t)
	service := sessionsvc.New(store, sessionsvc.Options{DurableAnswerEnabled: true, CaptureAdmission: captureadmission.New(0, "enabled")})
	before := store.PendingProjectionCount()
	_, err := service.TerminateInteraction(context.Background(), command)
	current, _ := service.GetInteraction(context.Background(), command.Owner, command.InteractionID)
	if !sessionsvc.IsCode(err, sessionsvc.CodeCaptureUnavailable) || current.ExecutionStatus != sessionvo.InteractionActive || store.PendingProjectionCount() != before {
		t.Fatalf("unknown admission staged answer: status=%s count=%d err=%v", current.ExecutionStatus, store.PendingProjectionCount(), err)
	}
}
func TestDurableAnswerCommittedReplayDoesNotDependOnCurrentPolicyOrGate(t *testing.T) {
	for _, state := range []string{"unknown", "disabled"} {
		t.Run(state, func(t *testing.T) {
			firstService, store, command := stagedAnswerFixture(t)
			committed, err := firstService.TerminateInteraction(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			view := captureadmission.New(0, "enabled")
			if state == "disabled" {
				view = captureadmission.NewStable(2, "disabled")
			}
			service := sessionsvc.New(store, sessionsvc.Options{DurableAnswerEnabled: false, CaptureAdmission: view})
			before := store.PendingProjectionCount()
			replay, err := service.TerminateInteraction(context.Background(), command)
			if err != nil || replay.ID != committed.ID || replay.ClosureManifest.AnswerArtifactRef != committed.ClosureManifest.AnswerArtifactRef || store.PendingProjectionCount() != before {
				t.Fatalf("policy-dependent committed replay: %+v %v", replay, err)
			}
			var artifact map[string]json.RawMessage
			if err = json.Unmarshal(command.AnswerArtifact, &artifact); err != nil {
				t.Fatal(err)
			}
			artifact["content"] = json.RawMessage(`{"answer":"changed"}`)
			delete(artifact, "content_hash")
			changed := command
			changed.AnswerArtifact, err = json.Marshal(artifact)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.TerminateInteraction(context.Background(), changed); err == nil || store.PendingProjectionCount() != before {
				t.Fatal("policy change bypassed immutable answer validation")
			}
			foreign := command
			foreign.Owner.EffectiveSubjectID = "foreign"
			if _, err = service.TerminateInteraction(context.Background(), foreign); !sessionsvc.IsCode(err, sessionsvc.CodeResourceNotDisclosed) {
				t.Fatalf("replay bypassed ownership: %v", err)
			}
		})
	}
}
func TestDurableAnswerDisabledClosureNeverStoresBodyAndRemainsReplayable(t *testing.T) {
	_, store, command := stagedAnswerFixture(t)
	service := sessionsvc.New(store, sessionsvc.Options{DurableAnswerEnabled: true, CaptureAdmission: captureadmission.NewStable(2, "disabled")})
	before := store.PendingProjectionCount()
	closed, err := service.TerminateInteraction(context.Background(), command)
	if err != nil || closed.ExecutionStatus != sessionvo.InteractionCompleted || closed.ClosureManifest.AnswerArtifactPersistence != nil || closed.ClosureManifest.AnswerArtifactRef != "" {
		t.Fatalf("disabled closure changed: %+v %v", closed, err)
	}
	items, err := store.Lease(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.EventType == sessionsvc.AnswerArtifactRequested {
			t.Fatal("disabled policy stored an answer task")
		}
	}
	if store.PendingProjectionCount() < before {
		t.Fatal("disabled closure lost existing records")
	}
	unknown := sessionsvc.New(store, sessionsvc.Options{CaptureAdmission: captureadmission.New(0, "enabled")})
	if _, err = unknown.TerminateInteraction(context.Background(), command); err != nil {
		t.Fatalf("original policy closure cannot replay: %v", err)
	}
}
func TestDurableAnswerStaticSizeErrorDoesNotDiscloseForeignInteraction(t *testing.T) {
	service, _, command := stagedAnswerFixture(t)
	command.AnswerArtifact = []byte(strings.Repeat(" ", sessionsvc.MaxAnswerArtifactBytes+1))
	command.Owner.EffectiveSubjectID = "foreign"
	if _, err := service.TerminateInteraction(context.Background(), command); !sessionsvc.IsCode(err, sessionsvc.CodeResourceNotDisclosed) {
		t.Fatalf("size processing bypassed canonical ownership: %v", err)
	}
}

func TestDurableAnswerHandoffDisabledIsAConfigurationFailure(t *testing.T) {
	_, store, command := stagedAnswerFixture(t)
	service := sessionsvc.New(store, sessionsvc.Options{})
	before := store.PendingProjectionCount()
	result, err := service.TerminateInteraction(context.Background(), command)
	if !sessionsvc.IsCode(err, sessionsvc.CodeAnswerHandoffDisabled) || result.ExecutionStatus == sessionvo.InteractionCompleted || store.PendingProjectionCount() != before {
		t.Fatalf("disabled handoff should require configuration review: %+v %v", result, err)
	}
	current, _ := service.GetInteraction(context.Background(), command.Owner, command.InteractionID)
	if current.ExecutionStatus != sessionvo.InteractionActive {
		t.Fatal("gate-off request changed lifecycle")
	}
}
