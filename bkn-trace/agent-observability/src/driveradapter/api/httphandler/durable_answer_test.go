// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
)

type durableAnswerHTTPFixture struct {
	mux          *http.ServeMux
	store        *sessionstore.Store
	conversation sessionvo.Conversation
	interaction  sessionvo.Interaction
}

func newDurableAnswerHTTPFixture(t *testing.T, enabled bool) durableAnswerHTTPFixture {
	t.Helper()
	store := sessionstore.New()
	mux := http.NewServeMux()
	httphandler.RegisterSessionRoutes(mux, "/api/agent-observability/v1", httphandler.NewSessionHandler(sessionsvc.New(store, sessionsvc.Options{DurableAnswerEnabled: enabled})))
	convResponse := performLifecycleRequest(t, mux, http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", `{"external_conversation_key":"durable-answer","idempotency_key":"ensure"}`)
	if convResponse.Code != http.StatusCreated {
		t.Fatalf("ensure=%d %s", convResponse.Code, convResponse.Body.String())
	}
	var conv sessionvo.Conversation
	decodeLifecycleResponse(t, convResponse, &conv)
	started := performLifecycleRequest(t, mux, http.MethodPost, "/api/agent-observability/v1/conversations/"+conv.ID+"/interactions", `{"idempotency_key":"start"}`)
	if started.Code != http.StatusCreated {
		t.Fatalf("start=%d %s", started.Code, started.Body.String())
	}
	var interaction sessionvo.Interaction
	decodeLifecycleResponse(t, started, &interaction)
	return durableAnswerHTTPFixture{mux: mux, store: store, conversation: conv, interaction: interaction}
}
func durableAnswerHTTPArtifact(t *testing.T, interactionID, content string) evidencevo.EvidenceArtifact {
	t.Helper()
	artifact, validation := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{ArtifactID: "art_result_durable", ArtifactType: evidencevo.ArtifactTypeResult, RequestID: "req-durable-http", TraceID: "4b3d59daeff5bfbb23d46c47a5051ec9", InteractionID: interactionID, ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Content: content, AccountID: "user-1", AccountType: "user", EffectiveSubjectID: "user-1", ApplicationPrincipalID: "app-1"})
	if len(validation) > 0 {
		t.Fatalf("invalid artifact fixture: %+v", validation)
	}
	return artifact
}
func durableAnswerHTTPBody(t *testing.T, artifact evidencevo.EvidenceArtifact) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"outcome": "completed", "idempotency_key": "finish-durable", "answer_artifact": artifact})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func durableAnswerTasks(t *testing.T, store *sessionstore.Store) int {
	t.Helper()
	items, err := store.Lease(context.Background(), 100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range items {
		if item.EventType == sessionsvc.AnswerArtifactRequested {
			count++
		}
	}
	return count
}
func TestDurableAnswerHTTPCommitsLargeAnswerAndReleasesConversation(t *testing.T) {
	f := newDurableAnswerHTTPFixture(t, true)
	// Cross the old 2MiB lifecycle limit with a real JSON body, not a fake header.
	artifact := durableAnswerHTTPArtifact(t, f.interaction.ID, strings.Repeat("a", (2<<20)+128))
	body := durableAnswerHTTPBody(t, artifact)
	if len(body) <= 2<<20 {
		t.Fatal("fixture did not cross legacy body limit")
	}
	response := performLifecycleRequest(t, f.mux, http.MethodPost, "/api/agent-observability/v1/interactions/"+f.interaction.ID+"/finish", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("durable finish=%d %s", response.Code, response.Body.String())
	}
	var finished sessionvo.Interaction
	decodeLifecycleResponse(t, response, &finished)
	if finished.ExecutionStatus != sessionvo.InteractionCompleted || finished.EvidenceStatus != sessionvo.EvidenceAssembling || finished.ClosureManifest == nil || finished.ClosureManifest.AnswerArtifactRef != "artifact:"+artifact.ArtifactID || durableAnswerTasks(t, f.store) != 1 {
		t.Fatalf("not atomically pending: %+v", finished)
	}
	items, err := f.store.Lease(context.Background(), 100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.EventType != sessionsvc.AnswerArtifactRequested {
			continue
		}
		var saved struct {
			Owner    sessionvo.Owner             `json:"owner"`
			Artifact evidencevo.EvidenceArtifact `json:"artifact"`
		}
		if err := json.Unmarshal(item.Payload, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Artifact.Content != artifact.Content || saved.Artifact.ContentHash != artifact.ContentHash || saved.Artifact.InteractionID != f.interaction.ID || saved.Owner.EffectiveSubjectID != "user-1" {
			t.Fatal("durable outbox omitted or changed the governed answer body")
		}
	}
	gateOff := http.NewServeMux()
	httphandler.RegisterSessionRoutes(gateOff, "/api/agent-observability/v1", httphandler.NewSessionHandler(sessionsvc.New(f.store, sessionsvc.Options{})))
	replay := performLifecycleRequest(t, gateOff, http.MethodPost, "/api/agent-observability/v1/interactions/"+f.interaction.ID+"/finish", string(body))
	if replay.Code != http.StatusOK || durableAnswerTasks(t, f.store) != 1 {
		t.Fatalf("large committed replay failed after gate disabled: %d %s", replay.Code, replay.Body.String())
	}
	next := performLifecycleRequest(t, gateOff, http.MethodPost, "/api/agent-observability/v1/conversations/"+f.conversation.ID+"/interactions", `{"idempotency_key":"next-start"}`)
	if next.Code != http.StatusCreated {
		t.Fatalf("next turn blocked=%d %s", next.Code, next.Body.String())
	}
	var active sessionvo.Interaction
	decodeLifecycleResponse(t, next, &active)
	rejected := performLifecycleRequest(t, gateOff, http.MethodPost, "/api/agent-observability/v1/interactions/"+active.ID+"/finish", string(durableAnswerHTTPBody(t, durableAnswerHTTPArtifact(t, active.ID, "new answer"))))
	if rejected.Code != http.StatusUnprocessableEntity || durableAnswerTasks(t, f.store) != 1 {
		t.Fatalf("gate-off new body was staged: %d %s", rejected.Code, rejected.Body.String())
	}
	var configurationError struct {
		Error struct {
			Code      string `json:"code"`
			Action    string `json:"required_action"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	decodeLifecycleResponse(t, rejected, &configurationError)
	if configurationError.Error.Code != string(sessionsvc.CodeAnswerHandoffDisabled) || configurationError.Error.Action != "request_durable_answer_configuration_review" || configurationError.Error.Retryable {
		t.Fatalf("gate-off error misdirected caller: %+v", configurationError.Error)
	}

}
func TestDurableAnswerHTTPPreservesLegacyReferenceFinish(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		f := newDurableAnswerHTTPFixture(t, enabled)
		response := performLifecycleRequest(t, f.mux, http.MethodPost, "/api/agent-observability/v1/interactions/"+f.interaction.ID+"/finish", `{"outcome":"completed","idempotency_key":"legacy-finish","answer_artifact_ref":"artifact:legacy"}`)
		if response.Code != http.StatusOK || durableAnswerTasks(t, f.store) != 0 {
			t.Fatalf("legacy finish=%d %s", response.Code, response.Body.String())
		}
	}
}
func TestDurableAnswerHTTPRejectsUnauthorizedOrInvalidArtifactWithoutTask(t *testing.T) {
	for _, failure := range []string{"auth", "permission", "owner", "interaction", "type", "hash", "gate disabled"} {
		t.Run(failure, func(t *testing.T) {
			f := newDurableAnswerHTTPFixture(t, failure != "gate disabled")
			artifact := durableAnswerHTTPArtifact(t, f.interaction.ID, "answer")
			switch failure {
			case "owner":
				artifact.ApplicationPrincipalID = "foreign-app"
			case "interaction":
				artifact.InteractionID = "foreign-interaction"
			case "type":
				artifact.ArtifactType = evidencevo.ArtifactTypeQuestion
			case "hash":
				artifact.ContentHash = "sha256:" + strings.Repeat("0", 64)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/interactions/"+f.interaction.ID+"/finish", bytes.NewReader(durableAnswerHTTPBody(t, artifact)))
			request.Header.Set("Content-Type", "application/json")
			if failure != "auth" {
				setTrustedOwnerHeaders(request)
			}
			if failure == "permission" {
				request.Header.Set("X-BKN-Effective-Subject-ID", "foreign-user")
			}
			response := httptest.NewRecorder()
			f.mux.ServeHTTP(response, request)
			if response.Code < 400 || durableAnswerTasks(t, f.store) != 0 {
				t.Fatalf("unsafe %s accepted: %d %s", failure, response.Code, response.Body.String())
			}
			read := performLifecycleRequest(t, f.mux, http.MethodGet, "/api/agent-observability/v1/interactions/"+f.interaction.ID, "")
			var current sessionvo.Interaction
			decodeLifecycleResponse(t, read, &current)
			if current.ExecutionStatus != sessionvo.InteractionActive {
				t.Fatalf("rejected finish mutated lifecycle: %+v", current)
			}
		})
	}
}

func TestDurableAnswerHTTPSizeFailureRequiresOperatorReview(t *testing.T) {
	f := newDurableAnswerHTTPFixture(t, true)
	// The envelope is within its 17MiB parsing bound; the artifact itself exceeds
	// its unchanged 16MiB contract and must not become a model retry loop.
	body := `{"outcome":"completed","idempotency_key":"oversized","answer_artifact":{"padding":"` + strings.Repeat("a", 16<<20) + `"}}`
	response := performLifecycleRequest(t, f.mux, http.MethodPost, "/api/agent-observability/v1/interactions/"+f.interaction.ID+"/finish", body)
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
			Action    string `json:"required_action"`
			Message   string `json:"message"`
		} `json:"error"`
	}
	decodeLifecycleResponse(t, response, &envelope)
	if response.Code != http.StatusUnprocessableEntity || envelope.Error.Code != string(sessionsvc.CodeAnswerArtifactStorageLimit) || envelope.Error.Retryable || envelope.Error.Action != "request_storage_configuration_review" || !strings.Contains(envelope.Error.Message, "without truncating") || durableAnswerTasks(t, f.store) != 0 {
		t.Fatalf("unsafe size contract: status=%d error=%+v", response.Code, envelope.Error)
	}
	read := performLifecycleRequest(t, f.mux, http.MethodGet, "/api/agent-observability/v1/interactions/"+f.interaction.ID, "")
	var current sessionvo.Interaction
	decodeLifecycleResponse(t, read, &current)
	if current.ExecutionStatus != sessionvo.InteractionActive {
		t.Fatal("size rejection claimed completed")
	}
}
