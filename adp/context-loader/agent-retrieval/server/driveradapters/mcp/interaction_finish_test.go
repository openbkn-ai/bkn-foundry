// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

func TestInteractionFinishSubmissionAndConfirmation(t *testing.T) {
	for _, mode := range []string{"independent budget", "lost acknowledgement", "temporary 500", "persistent 500", "timeout", "caller disconnect", "abandoned race", "owner refusal", "invalid acknowledgement", "partial acknowledgements", "malformed acknowledgement", "throttled", "failed outcome", "cancelled outcome", "plain 401", "plain 403", "plain 409", "plain 429"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", "true")
			t.Setenv("BKN_TRACE_INTERACTION_FINISH_TIMEOUT_MS", "4000")
			if mode == "timeout" {
				t.Setenv("BKN_TRACE_INTERACTION_FINISH_TIMEOUT_MS", "25")
			}
			ctx, cancel := context.WithCancel(startArtifactTestContext())
			defer cancel()
			terminal, expectedStatus := "completed", "completed"
			switch mode {
			case "failed outcome":
				terminal, expectedStatus = "failed", "failed"
			case "cancelled outcome":
				// Public request and persisted Core response use different spellings.
				terminal, expectedStatus = "cancelled", "canceled"
			}
			posts, commits := 0, 0
			var originalBody string
			client := bkntrace.NewLifecycleClient("http://finish.test", &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					if mode == "owner refusal" {
						return lifecycleAdapterJSONResponse(http.StatusForbidden, map[string]any{"error": map[string]any{"code": "resource_not_disclosed"}}), nil
					}
					return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Interaction{InteractionID: "int-finish", ConversationID: "conv-committed", ExecutionStatus: "active", UpdatedAt: time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)}), nil
				}
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/finish") {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("X-BKN-Application-Principal-ID") != "cursor-app" || r.Header.Get("X-BKN-Effective-Subject-ID") != "user-1" {
					t.Fatal("finish lost trusted owner")
				}
				posts++
				raw, err := io.ReadAll(r.Body)
				_ = r.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if posts == 1 {
					originalBody = string(raw)
				} else if originalBody != string(raw) {
					t.Fatal("retry changed the original finish payload/key")
				}
				var body map[string]any
				if json.Unmarshal(raw, &body) != nil {
					t.Fatal("invalid finish JSON")
				}
				key, _ := body["idempotency_key"].(string)
				if key == "" {
					t.Fatal("missing stable finish key")
				}
				if mode == "independent budget" {
					deadline, ok := r.Context().Deadline()
					if !ok || time.Until(deadline) < 3*time.Second {
						t.Fatal("terminal POST inherited the observation deadline")
					}
				}
				if mode == "caller disconnect" {
					cancel()
					if r.Context().Err() != nil {
						return nil, r.Context().Err()
					}
				}
				if mode == "timeout" {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				if strings.HasPrefix(mode, "plain ") {
					status := http.StatusForbidden
					switch mode {
					case "plain 401":
						status = http.StatusUnauthorized
					case "plain 409":
						status = http.StatusConflict
					case "plain 429":
						status = http.StatusTooManyRequests
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("<html>request rejected</html>"))}, nil
				}
				if mode == "abandoned race" {
					return lifecycleAdapterJSONResponse(http.StatusConflict, map[string]any{"error": map[string]any{"code": "terminal_conflict", "current_status": "abandoned"}}), nil
				}
				if mode == "persistent 500" || mode == "temporary 500" && posts == 1 {
					return lifecycleAdapterJSONResponse(http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "internal_error", "retryable": false}}), nil
				}
				if mode == "throttled" {
					return lifecycleAdapterJSONResponse(http.StatusTooManyRequests, map[string]any{"error": map[string]any{"code": "rate_limited", "retryable": true}}), nil
				}
				if mode == "malformed acknowledgement" {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{invalid"))}, nil
				}
				if mode == "partial acknowledgements" {
					if posts == 1 {
						return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Interaction{InteractionID: "int-finish", ExecutionStatus: "active"}), nil
					}
					return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Interaction{ExecutionStatus: "completed"}), nil
				}
				if mode == "invalid acknowledgement" {
					return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Interaction{InteractionID: "int-finish"}), nil
				}
				if commits == 0 {
					commits++
				}
				if mode == "lost acknowledgement" && posts == 1 {
					return nil, io.ErrUnexpectedEOF
				}
				return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Interaction{InteractionID: "int-finish", ConversationID: "conv-committed", ExecutionStatus: expectedStatus, EvidenceStatus: "assembling"}), nil
			})})
			req := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: map[string]any{"interaction_id": "int-finish", "outcome": terminal, "answer": "actual business answer"}}}
			result, err := handleLifecycleTool(client, toolKeyFinishInteraction)(ctx, req)
			if err != nil || result == nil {
				t.Fatalf("missing finish result: %v", err)
			}
			switch mode {
			case "persistent 500", "timeout", "invalid acknowledgement", "partial acknowledgements", "malformed acknowledgement", "throttled", "plain 429":
				if !result.IsError {
					t.Fatal("unconfirmed finish returned successful degradation")
				}
				value := lifecycleErrorFromResult(t, result)
				if value["code"] != "interaction_finish_unconfirmed" || value["retryable"] != true || value["required_action"] != "retry_same_finish" {
					t.Fatalf("incorrect recovery guidance: %#v", value)
				}
				text, _ := mcpsdk.AsTextContent(result.Content[0])
				var envelope map[string]any
				requestID, _ := value["request_id"].(string)
				if json.Unmarshal([]byte(text.Text), &envelope) != nil || envelope["finish_confirmed"] != false || envelope["stage"] != "core_call" || requestID == "" {
					t.Fatalf("unconfirmed finish lost correlation: %#v", envelope)
				}
				if mode == "timeout" && posts != 1 || mode != "timeout" && posts != 3 {
					t.Fatalf("unbounded or skipped finish retries: %d", posts)
				}
			case "plain 401", "plain 403", "plain 409":
				if !result.IsError {
					t.Fatal("HTTP refusal became success")
				}
				value := lifecycleErrorFromResult(t, result)
				code, action := "permission_denied", "request_authorization"
				if mode == "plain 409" {
					code, action = "lifecycle_request_rejected", "contact_platform_operator"
				}
				if value["code"] != code || value["required_action"] != action || value["retryable"] != false || posts != 1 {
					t.Fatalf("HTTP refusal retried or misclassified: %#v posts=%d", value, posts)
				}
			case "abandoned race", "owner refusal":
				if !result.IsError {
					t.Fatal("domain refusal became success")
				}
				value := lifecycleErrorFromResult(t, result)
				expected := "terminal_conflict"
				wantPosts := 1
				if mode == "owner refusal" {
					expected = "resource_not_disclosed"
					wantPosts = 0
				}
				if value["code"] != expected || posts != wantPosts {
					t.Fatalf("domain refusal retried or overwritten: %#v posts=%d", value, posts)
				}
			default:
				if result.IsError || result.StructuredContent.(map[string]any)["execution_status"] != expectedStatus || commits != 1 {
					t.Fatalf("finish was not confirmed exactly once: result=%#v commits=%d", result, commits)
				}
				wantPosts := 1
				if mode == "lost acknowledgement" || mode == "temporary 500" {
					wantPosts = 2
				}
				if posts != wantPosts {
					t.Fatalf("posts=%d want=%d", posts, wantPosts)
				}
			}
		})
	}
}

func TestInteractionFinishTimeoutBounds(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{{"", 5 * time.Second}, {"0", 5 * time.Second}, {"-1", 5 * time.Second}, {"bad", 5 * time.Second}, {"30001", 5 * time.Second}, {"250", 250 * time.Millisecond}, {"30000", 30 * time.Second}} {
		t.Setenv("BKN_TRACE_INTERACTION_FINISH_TIMEOUT_MS", tc.raw)
		if got := interactionFinishTimeout(); got != tc.want {
			t.Fatalf("timeout %q got=%v want=%v", tc.raw, got, tc.want)
		}
	}
}
