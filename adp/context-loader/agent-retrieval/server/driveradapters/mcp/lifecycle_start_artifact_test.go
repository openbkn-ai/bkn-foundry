// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestStartInteractionPreservesCommittedResultAcrossQuestionArtifactFailures(t *testing.T) {
	tests := []struct {
		name            string
		status          int
		code            string
		endpointMissing bool
		wantAttempts    int
	}{
		{name: "forbidden", status: http.StatusForbidden, code: "FORBIDDEN", wantAttempts: 1},
		{name: "unauthorized", status: http.StatusUnauthorized, code: "UNAUTHORIZED", wantAttempts: 1},
		{name: "invalid artifact", status: http.StatusBadRequest, code: "INVALID_PARAMETER", wantAttempts: 1},
		{name: "ingest credential not configured", status: http.StatusServiceUnavailable, code: "INGEST_AUTH_NOT_CONFIGURED", wantAttempts: 3},
		{name: "retryable outage", status: http.StatusServiceUnavailable, code: "UNAVAILABLE", wantAttempts: 3},
		{name: "artifact endpoint not configured", endpointMissing: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			startCalls, artifactCalls := 0, 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/agent-observability/v1/conversations/conv-committed/interactions":
					if r.Header.Get("X-BKN-Application-Principal-ID") != "cursor-app" || r.Header.Get("X-BKN-Effective-Subject-ID") != "user-1" {
						t.Errorf("Core must receive the same authenticated owner as the response")
					}
					startCalls++
					_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
						InteractionID: "int-committed", ConversationID: "conv-committed",
						ExecutionStatus: "active", EvidenceStatus: "pending",
						LeaseToken: "lease-committed", LeaseEpoch: 7,
						CreatedAt: time.Date(2026, 10, 3, 3, 0, 0, 0, time.UTC),
					})
				case "/api/agent-observability/v1/evidence/artifacts":
					artifactCalls++
					w.WriteHeader(test.status)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
						"code": test.code, "message": "question artifact could not be recorded",
					}})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer backend.Close()
			endpoint := backend.URL + "/api/agent-observability/v1/evidence/artifacts"
			if test.endpointMissing {
				endpoint = ""
			}
			t.Setenv("BKN_TRACE_ARTIFACT_ENDPOINT", endpoint)
			t.Setenv("BKN_TRACE_ARTIFACT_TOKEN", "artifact-token")

			result, err := handleLifecycleTool(bkntrace.NewLifecycleClient(backend.URL, backend.Client()), "bkn_start_interaction")(
				startArtifactTestContext(), startArtifactTestRequest(),
			)
			if startCalls != 1 || artifactCalls != test.wantAttempts {
				t.Fatalf("Core start calls=%d artifact attempts=%d, want 1 and %d", startCalls, artifactCalls, test.wantAttempts)
			}
			if err != nil || result == nil || result.IsError {
				t.Fatalf("committed Start must survive question artifact failure: result=%#v err=%v", result, err)
			}
			if result.Meta == nil {
				t.Fatal("capture failure omitted Trace metadata")
			}
			meta := result.Meta.AdditionalFields[traceAvailabilityMetaKey].(map[string]any)
			if meta["lifecycle_recorded"] != true || meta["recorded"] != false {
				t.Fatalf("committed start and capture failure must be distinguished: %#v", meta)
			}
			want := map[string]any{
				"conversation_id": "conv-committed", "interaction_id": "int-committed", "execution_status": "active",
				"owner": bkntrace.Owner{ApplicationPrincipalID: "cursor-app", EffectiveSubjectType: "user", EffectiveSubjectID: "user-1"},
			}
			if !reflect.DeepEqual(result.StructuredContent, want) {
				t.Fatalf("Start must return authoritative IDs/status/owner: got=%#v want=%#v", result.StructuredContent, want)
			}
			text, ok := mcpsdk.AsTextContent(result.Content[0])
			var fallback map[string]any
			if !ok || json.Unmarshal([]byte(text.Text), &fallback) != nil || !reflect.DeepEqual(fallback, normalizeLifecycleOwnerTestJSON(t, want)) {
				t.Fatalf("text fallback must preserve authoritative IDs/status: %#v", result.Content)
			}
			if len(result.Content) != 2 {
				t.Fatalf("degraded start should append one trace diagnostic: %#v", result.Content)
			}
			diagnosticText, ok := mcpsdk.AsTextContent(result.Content[1])
			var diagnostic map[string]any
			if !ok || json.Unmarshal([]byte(strings.TrimPrefix(diagnosticText.Text, traceAvailabilityDiagnosticPrefix)), &diagnostic) != nil {
				t.Fatalf("trace diagnostic is not JSON text: %#v", result.Content[1])
			}
			trace, ok := diagnostic["bkn_trace"].(map[string]any)
			if !ok || trace["lifecycle_recorded"] != true {
				t.Fatalf("trace diagnostic lost committed lifecycle state: %#v", diagnostic)
			}
		})
	}
}

func TestStartInteractionStillRejectsCoreFailuresBeforeQuestionArtifact(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		code      string
		transport bool
	}{
		{name: "owner mismatch", status: http.StatusForbidden, code: "owner_mismatch"},
		{name: "lease conflict", status: http.StatusConflict, code: "lease_conflict"},
		{name: "interaction in progress", status: http.StatusConflict, code: "interaction_in_progress"},
		{name: "Core unavailable", status: http.StatusServiceUnavailable, code: "trace_core_unavailable"},
		{name: "Core transport failure", code: "trace_core_unavailable", transport: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifactCalls := 0
			artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				artifactCalls++
				w.WriteHeader(http.StatusCreated)
			}))
			defer artifact.Close()
			t.Setenv("BKN_TRACE_ARTIFACT_ENDPOINT", artifact.URL)
			coreCalls := 0
			client := &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				coreCalls++
				if test.transport {
					return nil, errors.New("Core connection failed")
				}
				return lifecycleAdapterJSONResponse(test.status, map[string]any{"error": map[string]any{
					"code": test.code, "message": "Core refused Start",
				}}), nil
			})}
			result, err := handleLifecycleTool(bkntrace.NewLifecycleClient("http://bkn-trace.test", client), "bkn_start_interaction")(
				startArtifactTestContext(), startArtifactTestRequest(),
			)
			if test.code == "trace_core_unavailable" {
				if err != nil || result == nil || result.IsError || result.StructuredContent.(map[string]any)["trace_recorded"] != false {
					t.Fatalf("Core outage must be explicit unrecorded response: %#v err=%v", result, err)
				}
				if coreCalls != 1 || artifactCalls != 0 {
					t.Fatalf("Core calls=%d artifact calls=%d", coreCalls, artifactCalls)
				}
				return
			}
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("Core failure must remain a tool error: result=%#v err=%v", result, err)
			}
			var envelope struct {
				Error lifecycleError `json:"error"`
			}
			text, ok := mcpsdk.AsTextContent(result.Content[0])
			if !ok || json.Unmarshal([]byte(text.Text), &envelope) != nil || envelope.Error.Code != test.code {
				t.Fatalf("Core error code changed: %#v", result.Content)
			}
			if coreCalls != 1 || artifactCalls != 0 {
				t.Fatalf("Core calls=%d artifact calls=%d, want 1 and 0", coreCalls, artifactCalls)
			}
		})
	}
}

func startArtifactTestContext() context.Context {
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{RequestID: "req-start-artifact-boundary"})
	return common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "cursor-app"},
	})
}

func startArtifactTestRequest() mcpsdk.CallToolRequest {
	return mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: map[string]any{
		"conversation_id": "conv-committed", "conversation_mode": "continue",
		"question": "查询 BOM", "agent_name": "供应链分析助手",
	}}}
}

func normalizeLifecycleOwnerTestJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
