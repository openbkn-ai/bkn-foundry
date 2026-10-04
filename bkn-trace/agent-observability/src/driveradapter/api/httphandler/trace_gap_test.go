// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler_test

import (
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
	"net/http"
	"testing"
)

func TestManagedFinishPersistsExplicitTraceGaps(t *testing.T) {
	handler := httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{}))
	mux := http.NewServeMux()
	httphandler.RegisterSessionRoutes(mux, "/api/agent-observability/v1", handler)
	var conversation sessionvo.Conversation
	decodeLifecycleResponse(t, performLifecycleRequest(t, mux, http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", `{"external_conversation_key":"gap","idempotency_key":"gap-conv"}`), &conversation)
	var interaction sessionvo.Interaction
	decodeLifecycleResponse(t, performLifecycleRequest(t, mux, http.MethodPost, "/api/agent-observability/v1/conversations/"+conversation.ID+"/interactions", `{"idempotency_key":"gap-int"}`), &interaction)
	path := "/api/agent-observability/v1/interactions/" + interaction.ID + "/finish"
	body := `{"outcome":"completed","idempotency_key":"gap-finish","answer_artifact_ref":"artifact:answer","partial_reasons":["trace_call_unrecorded:execute_tool:12345678-1234-1234-1234-1234567890ab"]}`
	response := performLifecycleRequest(t, mux, http.MethodPost, path, body)
	if response.Code != http.StatusOK {
		t.Fatalf("explicit gap finish: %d %s", response.Code, response.Body.String())
	}
	var finished sessionvo.Interaction
	decodeLifecycleResponse(t, response, &finished)
	if finished.EvidenceStatus != sessionvo.EvidencePartial || finished.ClosureManifest == nil || len(finished.ClosureManifest.SystemPartialReasons) != 1 {
		t.Fatalf("gap disappeared from immutable closure: %#v", finished)
	}
	if response := performLifecycleRequest(t, mux, http.MethodPost, path, body); response.Code != http.StatusOK {
		t.Fatalf("exact gap replay: %d", response.Code)
	}
	changed := `{"outcome":"completed","idempotency_key":"gap-finish","answer_artifact_ref":"artifact:answer","partial_reasons":[]}`
	if response := performLifecycleRequest(t, mux, http.MethodPost, path, changed); response.Code != http.StatusConflict {
		t.Fatalf("changed gap was allowed on terminal replay: %d %s", response.Code, response.Body.String())
	}
}
