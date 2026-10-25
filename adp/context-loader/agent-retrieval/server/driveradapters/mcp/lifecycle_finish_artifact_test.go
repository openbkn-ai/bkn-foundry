// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

func TestFinishAnswerArtifactTimeoutReleasesInteractionForNextTurn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) { testFinishAnswerArtifactGate(t, enabled) })
	}
}

func testFinishAnswerArtifactGate(t *testing.T, enabled bool) {
	t.Helper()
	t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", fmt.Sprint(enabled))
	backend := httptest.NewServer(newFinishArtifactCoreFixture())
	defer backend.Close()
	backendURL := backend.URL
	var questionCalls, resultCalls atomic.Int32
	artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Type string `json:"artifact_type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch body.Type {
		case "question":
			questionCalls.Add(1)
			w.WriteHeader(http.StatusCreated)
		case "result":
			resultCalls.Add(1)
			<-r.Context().Done()
		default:
			t.Errorf("unexpected artifact type %q", body.Type)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer artifact.Close()
	t.Setenv("BKN_TRACE_ARTIFACT_ENDPOINT", artifact.URL)
	t.Setenv("BKN_TRACE_ARTIFACT_TOKEN", "fixture-token")
	t.Setenv("BKN_TRACE_ARTIFACT_TIMEOUT_MS", "20")
	var nextStartStatus atomic.Int32
	client := bkntrace.NewLifecycleClient(backendURL, &http.Client{Timeout: 2 * time.Second, Transport: lifecycleAdapterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(req)
		if err == nil && req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/conversations/conv-committed/interactions") {
			nextStartStatus.Store(int32(response.StatusCode))
		}
		return response, err
	})})
	ctx := startArtifactTestContext()
	started, err := handleLifecycleTool(client, "bkn_start_interaction")(ctx, startArtifactTestRequest())
	if err != nil || started == nil || started.IsError {
		t.Fatalf("start fixture failed: %+v %v", started, err)
	}
	view := normalizeLifecycleOwnerTestJSON(t, started.StructuredContent)
	id, _ := view["interaction_id"].(string)
	if id == "" || questionCalls.Load() != 1 {
		t.Fatalf("invalid started interaction=%q questionCalls=%d", id, questionCalls.Load())
	}
	finish := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: map[string]any{"interaction_id": id, "outcome": "completed", "answer": "the actual answer"}}}
	finishStarted := time.Now()
	finished, err := handleLifecycleTool(client, "bkn_finish_interaction")(ctx, finish)
	finishElapsed := time.Since(finishStarted)
	if err != nil || finished == nil {
		t.Fatalf("artifact timeout not exercised: result=%+v err=%v attempts=%d", finished, err, resultCalls.Load())
	}
	if !enabled && finished.StructuredContent != nil {
		if normalizeLifecycleOwnerTestJSON(t, finished.StructuredContent)["execution_status"] == "completed" {
			t.Error("failed answer artifact must not claim completed")
		}
	}
	var current bkntrace.Interaction
	apiErr, err := client.Call(ctx, http.MethodGet, "/interactions/"+url.PathEscape(id), nil, &current)
	if err != nil || apiErr != nil {
		t.Fatalf("read authoritative interaction: %v %v", apiErr, err)
	}
	if enabled {
		if finished.IsError || current.ExecutionStatus != "completed" || current.EvidenceStatus != "assembling" || resultCalls.Load() != 0 {
			t.Errorf("SQL committed finish must survive unavailable artifact endpoint: finish=%+v current=%+v resultUploads=%d", finished, current, resultCalls.Load())
		}
	} else if current.ExecutionStatus != "active" || resultCalls.Load() == 0 {
		t.Errorf("disabled gate changed legacy failure behavior: finish=%+v current=%+v resultUploads=%d", finished, current, resultCalls.Load())
	}
	// A distinct invocation avoids treating a Start idempotency replay as proof
	// that the conversation slot was released.
	nextCtx := common.SetTraceContextToCtx(ctx, common.TraceContext{RequestID: "req-after-answer-artifact-timeout"})
	next, err := handleLifecycleTool(client, "bkn_start_interaction")(nextCtx, startArtifactTestRequest())
	if enabled {
		if err != nil || next == nil || next.IsError {
			t.Errorf("durable finish left next Start blocked: %+v err=%v", next, err)
		} else if normalizeLifecycleOwnerTestJSON(t, next.StructuredContent)["interaction_id"] == id {
			t.Error("next Start replayed the old interaction")
		}
	} else if err != nil || next == nil || !next.IsError || nextStartStatus.Load() != http.StatusConflict {
		t.Errorf("disabled gate changed legacy active-slot contract: %+v err=%v", next, err)
	}
	t.Logf("authoritative_status=%s next_start_http=%d artifact_attempts=%d finish_elapsed_ms=%d", current.ExecutionStatus, nextStartStatus.Load(), resultCalls.Load(), finishElapsed.Milliseconds())
}

// Mirror the public Core contracts used here: an unexpired active slot returns
// interaction_in_progress, and completed requires a durable answer reference.
func newFinishArtifactCoreFixture() http.Handler {
	var mu sync.Mutex
	var current bkntrace.Interaction
	serial := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("X-BKN-Application-Principal-ID") != "cursor-app" || r.Header.Get("X-BKN-Effective-Subject-ID") != "user-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fail := func(status int, code string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": code, "retryable": false}})
		}
		switch {
		case r.URL.Path == "/api/agent-observability/v1/conversations/conv-committed/interactions" && r.Method == http.MethodPost:
			if current.ExecutionStatus == "active" {
				fail(http.StatusConflict, "interaction_in_progress")
				return
			}
			serial++
			current = bkntrace.Interaction{InteractionID: fmt.Sprintf("int-finish-%d", serial), ConversationID: "conv-committed", ExecutionStatus: "active", EvidenceStatus: "pending", LeaseToken: "lease", LeaseEpoch: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(current)
		case r.URL.Path == "/api/agent-observability/v1/interactions/"+current.InteractionID && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(current)
		case r.URL.Path == "/api/agent-observability/v1/interactions/"+current.InteractionID+"/finish" && r.Method == http.MethodPost:
			var body struct {
				Outcome   string         `json:"outcome"`
				AnswerRef string         `json:"answer_artifact_ref"`
				Artifact  map[string]any `json:"answer_artifact"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				fail(http.StatusBadRequest, "interaction_required")
				return
			}
			switch body.Outcome {
			case "completed":
				if body.Artifact != nil {
					if body.Artifact["artifact_type"] != "result" || body.Artifact["interaction_id"] != current.InteractionID || body.Artifact["application_principal_id"] != "cursor-app" || body.Artifact["effective_subject_id"] != "user-1" || body.Artifact["content"] != "the actual answer" {
						fail(http.StatusUnprocessableEntity, "invalid_evidence_artifact")
						return
					}
					body.AnswerRef = "artifact:" + body.Artifact["artifact_id"].(string)
				}
				if body.AnswerRef == "" {
					fail(http.StatusUnprocessableEntity, "closure_manifest_invalid")
					return
				}
				current.ExecutionStatus = "completed"
			case "failed", "handed_off":
				current.ExecutionStatus = body.Outcome
			case "cancelled":
				current.ExecutionStatus = "canceled"
			default:
				fail(http.StatusUnprocessableEntity, "interaction_required")
				return
			}
			current.EvidenceStatus = "partial"
			if body.Artifact != nil {
				current.EvidenceStatus = "assembling"
			}
			_ = json.NewEncoder(w).Encode(current)
		default:
			if !strings.HasPrefix(r.URL.Path, "/api/agent-observability/v1/") {
				fail(http.StatusNotFound, "invalid_route")
				return
			}
			fail(http.StatusNotFound, "interaction_required")
		}
	})
}

func TestDurableAnswerRetryKeepsContentIdentityWithoutRetryingUploads(t *testing.T) {
	t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", "true")
	var bodies []map[string]any
	var bodyMu sync.Mutex
	var artifactCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyMu.Lock()
		defer bodyMu.Unlock()
		if r.URL.Path == "/artifact" {
			artifactCalls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodGet {
			if len(bodies) >= 4 {
				artifact := bodies[3]["answer_artifact"].(map[string]any)
				_ = json.NewEncoder(w).Encode(bkntrace.Interaction{InteractionID: "int-retry", ConversationID: "conv-committed", ExecutionStatus: "completed", EvidenceStatus: "complete", UpdatedAt: time.Now().UTC(), ClosureManifest: &bkntrace.ClosureManifest{AnswerArtifactRef: "artifact:" + artifact["artifact_id"].(string)}})
				return
			}
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{InteractionID: "int-retry", ConversationID: "conv-committed", ExecutionStatus: "active", UpdatedAt: time.Date(2026, 10, 25, 0, 0, len(bodies), 0, time.UTC)})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		bodies = append(bodies, body)
		if len(bodies) <= 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "trace_core_unavailable", "message": "retry", "retryable": true}})
			return
		}
		evidence := "assembling"
		if len(bodies) >= 5 {
			evidence = "complete"
		}
		_ = json.NewEncoder(w).Encode(bkntrace.Interaction{InteractionID: "int-retry", ConversationID: "conv-committed", ExecutionStatus: "completed", EvidenceStatus: evidence})
	}))
	defer backend.Close()
	t.Setenv("BKN_TRACE_ARTIFACT_ENDPOINT", backend.URL+"/artifact")
	req := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: map[string]any{"interaction_id": "int-retry", "outcome": "completed", "answer": "same answer"}}}
	client := bkntrace.NewLifecycleClient(backend.URL, backend.Client())
	for i := 0; i < 3; i++ {
		ctx := common.SetTraceContextToCtx(startArtifactTestContext(), common.TraceContext{RequestID: fmt.Sprintf("retry-%d", i)})
		result, err := handleLifecycleTool(client, "bkn_finish_interaction")(ctx, req)
		if err != nil || result == nil {
			t.Fatalf("retry=%d result=%+v err=%v", i, result, err)
		}
		if i == 0 && (!result.IsError || lifecycleErrorFromResult(t, result)["code"] != "interaction_finish_unconfirmed") {
			t.Fatal("exhausted finish must remain unconfirmed")
		}
		expectedEvidence := "assembling"
		if i == 2 {
			expectedEvidence = "complete"
		}
		if i > 0 && (result.IsError || normalizeLifecycleOwnerTestJSON(t, result.StructuredContent)["evidence_status"] != expectedEvidence) {
			t.Fatalf("Core authoritative state changed: %+v", result)
		}
	}
	bodyMu.Lock()
	defer bodyMu.Unlock()
	if len(bodies) != 5 || artifactCalls.Load() != 0 {
		t.Fatalf("unexpected calls: finish=%d artifact=%d", len(bodies), artifactCalls.Load())
	}
	if bodies[0]["idempotency_key"] != bodies[3]["idempotency_key"] || bodies[0]["idempotency_key"] != bodies[4]["idempotency_key"] {
		t.Fatal("request context changed finish idempotency key")
	}
	first := bodies[0]["answer_artifact"].(map[string]any)
	second := bodies[3]["answer_artifact"].(map[string]any)
	for _, key := range []string{"artifact_id", "content_hash", "content", "interaction_id", "application_principal_id", "effective_subject_id"} {
		if first[key] != second[key] {
			t.Fatalf("unstable content identity %s", key)
		}
	}
	if first["bkn.request.id"] == second["bkn.request.id"] || first["trace_id"] == second["trace_id"] {
		t.Fatal("retry fixture did not vary diagnostic context")
	}
	if bodies[4]["answer_artifact"] != nil || bodies[4]["answer_artifact_ref"] != "artifact:"+first["artifact_id"].(string) {
		t.Fatal("committed retry reconstructed the artifact instead of reusing Core's reference")
	}
	if _, mutated := req.GetArguments()["answer_artifact"]; mutated {
		t.Fatal("caller request mutated")
	}
	if _, mutated := req.GetArguments()["idempotency_key"]; mutated {
		t.Fatal("caller idempotency arguments mutated")
	}
}

func TestDurableAnswerCoreRefusalAndUntrustedInputNeverUploadOrFallback(t *testing.T) {
	for _, stage := range []string{"read forbidden", "finish forbidden", "finish unavailable", "untrusted artifact", "untrusted artifact reference"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", "true")
			var posts, uploads atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/artifact" {
					uploads.Add(1)
					w.WriteHeader(201)
					return
				}
				if r.Method == http.MethodGet && stage != "read forbidden" {
					_ = json.NewEncoder(w).Encode(bkntrace.Interaction{InteractionID: "int-denied", ConversationID: "conv-committed", ExecutionStatus: "active", UpdatedAt: time.Now().UTC()})
					return
				}
				if r.Method == http.MethodPost {
					posts.Add(1)
				}
				status := http.StatusForbidden
				if stage == "finish unavailable" {
					status = http.StatusServiceUnavailable
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "permission_denied", "message": "denied"}})
			}))
			defer backend.Close()
			t.Setenv("BKN_TRACE_ARTIFACT_ENDPOINT", backend.URL+"/artifact")
			args := map[string]any{"interaction_id": "int-denied", "outcome": "completed", "answer": "answer"}
			switch stage {
			case "untrusted artifact":
				args["answer_artifact"] = map[string]any{"content": "forged"}
			case "untrusted artifact reference":
				args["answer_artifact_ref"] = "artifact:forged"
			}
			result, err := handleLifecycleTool(bkntrace.NewLifecycleClient(backend.URL, backend.Client()), "bkn_finish_interaction")(startArtifactTestContext(), mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: args}})
			if err != nil || result == nil || uploads.Load() != 0 {
				t.Fatalf("refusal fallback: %+v err=%v uploads=%d", result, err, uploads.Load())
			}
			want := int32(1)
			if stage == "read forbidden" || strings.HasPrefix(stage, "untrusted artifact") {
				want = 0
			}
			if posts.Load() != want {
				t.Fatalf("post attempts=%d want=%d", posts.Load(), want)
			}
		})
	}
}

func TestDurableAnswerCaptureDisabledKeepsLegacyFinishWithoutArtifacts(t *testing.T) {
	for _, test := range []struct {
		gate          string
		localDisabled bool
	}{{"false", true}, {"true", true}, {"true", false}} {
		t.Run(fmt.Sprintf("gate=%s/local=%v", test.gate, test.localDisabled), func(t *testing.T) {
			t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", test.gate)
			bkntrace.SetEvidencePublisher(nil)
			if test.localDisabled {
				bkntrace.SetEvidencePublisher(disabledCapturePublisher{})
			}
			t.Cleanup(func() { bkntrace.SetEvidencePublisher(nil) })
			var uploads atomic.Int32
			artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { uploads.Add(1); w.WriteHeader(201) }))
			defer artifact.Close()
			t.Setenv("BKN_TRACE_ARTIFACT_ENDPOINT", artifact.URL)
			posts := 0
			disabled := false
			client := bkntrace.NewLifecycleClient("http://core.test", &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				budget := time.Second
				if r.Method == http.MethodPost {
					budget = 5 * time.Second
				}
				if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > budget {
					t.Error("finish preparation/submission exceeded its phase budget")
				}

				if r.Method == http.MethodGet {
					return lifecycleAdapterJSONResponse(200, bkntrace.Interaction{InteractionID: "int-disabled", ConversationID: "conv-committed", ExecutionStatus: "active", CaptureEnabled: &disabled}), nil
				}
				posts++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["answer_artifact"] != nil || body["answer_artifact_ref"] != nil {
					t.Error("capture-disabled finish included evidence")
				}
				return lifecycleAdapterJSONResponse(200, bkntrace.Interaction{InteractionID: "int-disabled", ConversationID: "conv-committed", ExecutionStatus: "completed", CaptureEnabled: &disabled}), nil
			})})
			result, err := handleLifecycleTool(client, "bkn_finish_interaction")(startArtifactTestContext(), mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: map[string]any{"interaction_id": "int-disabled", "outcome": "completed", "answer": "answer"}}})
			if err != nil || result == nil || result.IsError || posts != 1 || uploads.Load() != 0 {
				t.Fatalf("disabled finish changed: result=%+v posts=%d uploads=%d err=%v", result, posts, uploads.Load(), err)
			}
		})
	}
}
