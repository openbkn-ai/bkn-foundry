// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

type recordingFunctionTrace struct{ opened bool }

func (trace *recordingFunctionTrace) Open(ctx context.Context, _ *interfaces.ActionExecution,
	_ interfaces.CallerRuntimeCredential) (context.Context, func(string), error) {
	trace.opened = true
	return ctx, func(string) {}, nil
}

func (*recordingFunctionTrace) Run(ctx context.Context, _ string,
	run func(context.Context) (any, error)) (any, error) {
	return run(ctx)
}

func TestCancelledFunctionActionDoesNotCreateManagedInteraction(t *testing.T) {
	controller := gomock.NewController(t)
	logs := omock.NewMockActionLogsService(controller)
	access := omock.NewMockAgentOperatorAccess(controller)
	trace := &recordingFunctionTrace{}
	execution, actionType, req := aggregatedOnceFixture(t, "exec-cancelled")
	execution.ProxyPermissionSnapshot[0].ResourceType = interfaces.ProxyTargetTypeFunction
	service := &actionSchedulerService{
		logsService: logs, aoAccess: access, permissions: &actionPermissionStub{}, functionTrace: trace,
	}
	var stored []interfaces.ObjectExecutionResult
	logs.EXPECT().AppendResults(gomock.Any(), execution.KNID, execution.ID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, _ int, results []interfaces.ObjectExecutionResult) error {
			stored = append(stored, results...)
			return nil
		})
	logs.EXPECT().MarkExecutionRunning(gomock.Any(), execution.KNID, execution.ID).Return(nil)
	logs.EXPECT().GetExecutionStatus(gomock.Any(), execution.KNID, execution.ID).
		Return(interfaces.ExecutionStatusCancelled, nil).AnyTimes()
	logs.EXPECT().FinishExecution(gomock.Any(), execution.KNID, execution.ID, gomock.Any()).Return(nil)
	service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{})
	if trace.opened {
		t.Fatal("cancelled action created a managed Interaction")
	}
	if len(stored) != 1 || stored[0].Status != interfaces.ObjectStatusCancelled {
		t.Fatalf("cancelled execution results = %#v", stored)
	}
}

func TestManagedActionFunctionUsesRegisteredOperationAfterCallerInteractionEnds(t *testing.T) {
	var paths []string
	var pathsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathsMu.Lock()
		paths = append(paths, r.URL.Path)
		pathsMu.Unlock()
		if r.Header.Get("X-BKN-Application-Principal-ID") != "app-1" ||
			r.Header.Get("X-BKN-Effective-Subject-ID") != "user-1" {
			t.Errorf("incorrect Trace Core owner headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/operations/op_original"):
			_, _ = w.Write([]byte(`{"conversation_id":"conv_original","interaction_id":"int_original"}`))
		case strings.HasSuffix(r.URL.Path, "/conversations:ensure-current"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["one_shot"] != true || body["external_conversation_key"] != "action-execution:exec-1" {
				t.Errorf("invalid conversation request: %#v", body)
			}
			_, _ = w.Write([]byte(`{"conversation_id":"conv_action"}`))
		case strings.HasSuffix(r.URL.Path, "/interactions"):
			_, _ = w.Write([]byte(`{"interaction_id":"int_action","lease_token":"lease-1","lease_epoch":1}`))
		case strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			input := body["input"].(map[string]any)["inline"].(map[string]any)
			if input["origin_interaction_id"] != "int_original" || input["origin_operation_id"] != "op_original" ||
				body["parent_operation_id"] != nil {
				t.Errorf("invalid Function origin: %#v", body)
			}
			_, _ = w.Write([]byte(`{"operation":{"operation_id":"op_function","attempt":1},"receipt":{"receipt_id":"receipt-1"},"execute":true}`))
		case strings.HasSuffix(r.URL.Path, "/attempts/1:complete"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["receipt_id"] != "receipt-1" || body["evidence_durability"] != "durable" {
				t.Errorf("invalid receipt finish: %#v", body)
			}
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/finish"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["outcome"] != "handed_off" {
				t.Errorf("action Interaction did not hand off its result: %#v", body)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected Trace Core route: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &actionTraceClient{baseURL: server.URL, http: server.Client()}
	execution := &interfaces.ActionExecution{ID: "exec-1", KNID: "kn-1", ActionTypeID: "at-1",
		Executor: interfaces.AccountInfo{ID: "user-1", Type: "user"}}
	credential := interfaces.CallerRuntimeCredential{
		Authorization: "Bearer caller-token", ApplicationPrincipalID: "app-1",
		ConversationID: "conv_original", InteractionID: "int_original", ParentOperationID: "op_original",
	}
	ctx, finish, err := client.Open(context.Background(), execution, credential)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(withActionInvocation(ctx, 0), "function-1", func(runCtx context.Context) (any, error) {
		traceContext, ok := common.GetTraceContextFromCtx(runCtx)
		if !ok || traceContext.ConversationID != "conv_action" || traceContext.InteractionID != "int_action" ||
			traceContext.OperationID != "op_function" {
			t.Errorf("Function context lacks registered operation: %#v", traceContext)
		}
		caller, ok := interfaces.CallerRuntimeCredentialFromContext(runCtx)
		if !ok || caller.Authorization != "Bearer caller-token" || caller.ParentOperationID != "op_function" ||
			caller.ConversationID != "conv_action" || caller.InteractionID != "int_action" {
			t.Errorf("Function SDK credential is incomplete: %#v", caller)
		}
		return map[string]any{"ok": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["ok"] != true {
		t.Fatalf("result = %#v", result)
	}
	finish("handed_off")
	pathsMu.Lock()
	defer pathsMu.Unlock()
	if len(paths) != 6 {
		t.Fatalf("unexpected Trace Core requests: %#v", paths)
	}
}

func TestManagedActionFunctionRejectsTraceRegistrationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/conversations:ensure-current"):
			_, _ = w.Write([]byte(`{"conversation_id":"conv_action"}`))
		case strings.HasSuffix(r.URL.Path, "/interactions"):
			_, _ = w.Write([]byte(`{"interaction_id":"int_action","lease_token":"lease-1","lease_epoch":1}`))
		case strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"interaction_terminal"}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	client := &actionTraceClient{baseURL: server.URL, http: server.Client()}
	ctx, finish, err := client.Open(context.Background(), &interfaces.ActionExecution{
		ID: "exec-1", Executor: interfaces.AccountInfo{ID: "user-1", Type: "user"},
	}, interfaces.CallerRuntimeCredential{Authorization: "Bearer token", ApplicationPrincipalID: "app-1"})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = client.Run(withActionInvocation(ctx, 0), "function-1", func(context.Context) (any, error) {
		called = true
		return nil, nil
	})
	if err == nil || called {
		t.Fatalf("Function ran despite Trace Core refusal: err=%v called=%v", err, called)
	}
	finish("failed")
}

func TestManagedActionFunctionRunsWhenTraceCaptureDisabled(t *testing.T) {
	ensureCalls := 0
	finishCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/conversations:ensure-current"):
			_, _ = w.Write([]byte(`{"conversation_id":"conv_action"}`))
		case strings.HasSuffix(r.URL.Path, "/interactions"):
			_, _ = w.Write([]byte(`{"interaction_id":"int_action","lease_token":"lease-1","lease_epoch":1}`))
		case strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			ensureCalls++
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"capture_disabled"}}`))
		case strings.HasSuffix(r.URL.Path, "/finish"):
			finishCalls++
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected Trace Core route: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &actionTraceClient{baseURL: server.URL, http: server.Client()}
	ctx, finish, err := client.Open(context.Background(), &interfaces.ActionExecution{
		ID: "exec-1", Executor: interfaces.AccountInfo{ID: "user-1", Type: "user"},
	}, interfaces.CallerRuntimeCredential{Authorization: "Bearer token", ApplicationPrincipalID: "app-1"})
	if err != nil {
		t.Fatal(err)
	}
	for ordinal := range 2 {
		result, err := client.Run(withActionInvocation(ctx, ordinal), "function-1", func(runCtx context.Context) (any, error) {
			traceContext, _ := common.GetTraceContextFromCtx(runCtx)
			if traceContext.ConversationID != "" || traceContext.InteractionID != "" || traceContext.OperationID != "" {
				t.Errorf("unregistered managed Trace context reached Function: %#v", traceContext)
			}
			caller, ok := interfaces.CallerRuntimeCredentialFromContext(runCtx)
			if !ok || caller.Authorization != "Bearer token" || caller.ApplicationPrincipalID != "app-1" ||
				caller.ConversationID != "" || caller.InteractionID != "" || caller.ParentOperationID != "" {
				t.Errorf("Function received incorrect untraced credential: %#v", caller)
			}
			return "ok", nil
		})
		if err != nil || result != "ok" {
			t.Fatalf("Function execution %d: result=%#v err=%v", ordinal, result, err)
		}
	}
	finish("handed_off")
	if ensureCalls != 1 || finishCalls != 1 {
		t.Fatalf("Trace Core requests: ensure=%d finish=%d", ensureCalls, finishCalls)
	}
}

func TestManagedActionFunctionRunsWhenTraceCoreUnavailable(t *testing.T) {
	for _, stage := range []string{"origin", "conversation", "interaction", "operation"} {
		t.Run(stage, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				failed := (stage == "origin" && strings.HasSuffix(r.URL.Path, "/operations/op_original")) ||
					(stage == "conversation" && strings.HasSuffix(r.URL.Path, "/conversations:ensure-current")) ||
					(stage == "interaction" && strings.HasSuffix(r.URL.Path, "/interactions")) ||
					(stage == "operation" && strings.HasSuffix(r.URL.Path, "/operations:ensure"))
				if failed {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":{"code":"trace_core_unavailable"}}`))
					return
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/operations/op_original"):
					_, _ = w.Write([]byte(`{"conversation_id":"conv_original","interaction_id":"int_original"}`))
				case strings.HasSuffix(r.URL.Path, "/conversations:ensure-current"):
					_, _ = w.Write([]byte(`{"conversation_id":"conv_action"}`))
				case strings.HasSuffix(r.URL.Path, "/interactions"):
					_, _ = w.Write([]byte(`{"interaction_id":"int_action","lease_token":"lease-1","lease_epoch":1}`))
				case strings.HasSuffix(r.URL.Path, "/finish"):
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected Trace Core route: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := &actionTraceClient{baseURL: server.URL, http: server.Client()}
			ctx, finish, err := client.Open(context.Background(), &interfaces.ActionExecution{
				ID: "exec-1", Executor: interfaces.AccountInfo{ID: "user-1", Type: "user"},
			}, interfaces.CallerRuntimeCredential{
				Authorization: "Bearer token", ApplicationPrincipalID: "app-1",
				ConversationID: "conv_original", InteractionID: "int_original", ParentOperationID: "op_original",
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Run(withActionInvocation(ctx, 0), "function-1", func(runCtx context.Context) (any, error) {
				traceContext, _ := common.GetTraceContextFromCtx(runCtx)
				caller, ok := interfaces.CallerRuntimeCredentialFromContext(runCtx)
				if traceContext.ConversationID != "" || traceContext.InteractionID != "" || traceContext.OperationID != "" ||
					!ok || caller.Authorization != "Bearer token" || caller.ConversationID != "" ||
					caller.InteractionID != "" || caller.ParentOperationID != "" {
					t.Errorf("unregistered Trace identity reached Function: trace=%#v caller=%#v", traceContext, caller)
				}
				return "business result", nil
			})
			if err != nil || result != "business result" {
				t.Fatalf("business Function was blocked: result=%#v err=%v", result, err)
			}
			finish("handed_off")
		})
	}
}

func TestManagedActionFunctionRunsWhenTraceCoreConnectionFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()
	client := &actionTraceClient{baseURL: address, http: server.Client()}
	ctx, finish, err := client.Open(context.Background(), &interfaces.ActionExecution{
		ID: "exec-1", Executor: interfaces.AccountInfo{ID: "user-1", Type: "user"},
	}, interfaces.CallerRuntimeCredential{Authorization: "Bearer token", ApplicationPrincipalID: "app-1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(ctx, "function-1", func(context.Context) (any, error) {
		return "business result", nil
	})
	if err != nil || result != "business result" {
		t.Fatalf("business Function was blocked: result=%#v err=%v", result, err)
	}
	finish("handed_off")
}

func TestManagedActionFunctionRunsWhenTraceCoreReturnsIncompleteSession(t *testing.T) {
	for _, stage := range []string{"conversation", "interaction", "operation"} {
		t.Run(stage, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/conversations:ensure-current"):
					if stage == "conversation" {
						_, _ = w.Write([]byte(`{}`))
					} else {
						_, _ = w.Write([]byte(`{"conversation_id":"conv_action"}`))
					}
				case strings.HasSuffix(r.URL.Path, "/interactions"):
					if stage == "interaction" {
						_, _ = w.Write([]byte(`{"interaction_id":"int_action"}`))
					} else {
						_, _ = w.Write([]byte(`{"interaction_id":"int_action","lease_token":"lease-1","lease_epoch":1}`))
					}
				case strings.HasSuffix(r.URL.Path, "/operations:ensure"):
					_, _ = w.Write([]byte(`{"execute":true}`))
				case strings.HasSuffix(r.URL.Path, "/finish"):
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected Trace Core route: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := &actionTraceClient{baseURL: server.URL, http: server.Client()}
			ctx, finish, err := client.Open(context.Background(), &interfaces.ActionExecution{
				ID: "exec-1", Executor: interfaces.AccountInfo{ID: "user-1", Type: "user"},
			}, interfaces.CallerRuntimeCredential{Authorization: "Bearer token", ApplicationPrincipalID: "app-1"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Run(ctx, "function-1", func(runCtx context.Context) (any, error) {
				traceContext, _ := common.GetTraceContextFromCtx(runCtx)
				if traceContext.ConversationID != "" || traceContext.InteractionID != "" || traceContext.OperationID != "" {
					t.Errorf("incomplete Trace identity reached Function: %#v", traceContext)
				}
				return "ok", nil
			})
			if err != nil || result != "ok" {
				t.Fatalf("business Function was blocked: result=%#v err=%v", result, err)
			}
			finish("handed_off")
		})
	}
}

func TestManagedActionFunctionDoesNotBypassTraceIdentityRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/operations/op_original") {
			t.Errorf("unexpected Trace Core route: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"permission_denied"}}`))
	}))
	defer server.Close()
	client := &actionTraceClient{baseURL: server.URL, http: server.Client()}
	_, _, err := client.Open(context.Background(), &interfaces.ActionExecution{
		ID: "exec-1", Executor: interfaces.AccountInfo{ID: "user-1", Type: "user"},
	}, interfaces.CallerRuntimeCredential{
		Authorization: "Bearer token", ApplicationPrincipalID: "app-1",
		ConversationID: "conv_original", InteractionID: "int_original", ParentOperationID: "op_original",
	})
	if err == nil || !strings.Contains(err.Error(), "permission_denied") {
		t.Fatalf("Trace identity refusal was bypassed: %v", err)
	}
}

func TestFunctionActionCompletesWhenTraceCoreIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()
	for _, test := range []struct {
		name  string
		trace managedFunctionTrace
	}{
		{name: "service unavailable", trace: &actionTraceClient{baseURL: address, http: server.Client()}},
		{name: "service not configured"},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := gomock.NewController(t)
			logs := omock.NewMockActionLogsService(controller)
			access := omock.NewMockAgentOperatorAccess(controller)
			execution, actionType, req := aggregatedOnceFixture(t, "exec-trace-outage")
			useFunctionTarget(t, execution, actionType)
			service := &actionSchedulerService{
				logsService: logs, aoAccess: access, permissions: &actionPermissionStub{}, functionTrace: test.trace,
			}
			var stored []interfaces.ObjectExecutionResult
			var outcome *interfaces.ExecutionOutcome
			logs.EXPECT().AppendResults(gomock.Any(), execution.KNID, execution.ID, gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _, _ string, _ int, results []interfaces.ObjectExecutionResult) error {
					stored = append(stored, results...)
					return nil
				})
			logs.EXPECT().MarkExecutionRunning(gomock.Any(), execution.KNID, execution.ID).Return(nil)
			logs.EXPECT().GetExecutionStatus(gomock.Any(), execution.KNID, execution.ID).
				Return(interfaces.ExecutionStatusRunning, nil).AnyTimes()
			logs.EXPECT().FinishExecution(gomock.Any(), execution.KNID, execution.ID, gomock.Any()).
				DoAndReturn(func(_ context.Context, _, _ string, value *interfaces.ExecutionOutcome) error {
					outcome = value
					return nil
				})
			access.EXPECT().ExecuteToolAsProxy(gomock.Any(), actionType.ActionSource.BoxID,
				actionType.ActionSource.ToolID, gomock.Any()).DoAndReturn(
				func(ctx context.Context, _, _ string, _ interfaces.ToolExecutionRequest) (any, error) {
					caller, ok := interfaces.CallerRuntimeCredentialFromContext(ctx)
					if !ok || caller.Authorization != "Bearer token" || caller.ConversationID != "" ||
						caller.InteractionID != "" || caller.ParentOperationID != "" {
						t.Errorf("Function lost caller or retained unregistered Trace identity: %#v", caller)
					}
					return map[string]any{"exit_code": 0, "result": "ok"}, nil
				})
			service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{
				Authorization: "Bearer token", ApplicationPrincipalID: "app-1",
				ConversationID: "conv_original", InteractionID: "int_original", ParentOperationID: "op_original",
			})
			if outcome == nil || outcome.Status != interfaces.ExecutionStatusCompleted || outcome.SuccessCount != 1 ||
				len(stored) != 1 || stored[0].Status != interfaces.ObjectStatusSuccess {
				t.Fatalf("Trace outage failed business execution: outcome=%#v results=%#v", outcome, stored)
			}
		})
	}
}

func TestManagedActionFunctionRunsWithoutOriginInteraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/conversations:ensure-current"):
			_, _ = w.Write([]byte(`{"conversation_id":"conv_direct"}`))
		case strings.HasSuffix(r.URL.Path, "/interactions"):
			_, _ = w.Write([]byte(`{"interaction_id":"int_direct","lease_token":"lease-1","lease_epoch":1}`))
		case strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			input := body["input"].(map[string]any)["inline"].(map[string]any)
			if input["origin_interaction_id"] != nil || input["origin_operation_id"] != nil {
				t.Errorf("direct action invented an origin: %#v", input)
			}
			_, _ = w.Write([]byte(`{"operation":{"operation_id":"op_direct","attempt":1},"receipt":{"receipt_id":"receipt-1"},"execute":true}`))
		case strings.HasSuffix(r.URL.Path, "/attempts/1:complete"):
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/finish"):
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected Trace Core route: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &actionTraceClient{baseURL: server.URL, http: server.Client()}
	ctx, finish, err := client.Open(context.Background(), &interfaces.ActionExecution{
		ID: "exec-direct", Executor: interfaces.AccountInfo{ID: "user-1", Type: "user"},
	}, interfaces.CallerRuntimeCredential{Authorization: "Bearer token", ApplicationPrincipalID: "app-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Run(withActionInvocation(ctx, 0), "function-1", func(runCtx context.Context) (any, error) {
		credential, ok := interfaces.CallerRuntimeCredentialFromContext(runCtx)
		if !ok || credential.ConversationID != "conv_direct" || credential.InteractionID != "int_direct" ||
			credential.ParentOperationID != "op_direct" {
			t.Errorf("direct action Function has no managed context: %#v", credential)
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	finish("handed_off")
}
