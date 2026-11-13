// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build integration

package action_scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// This exercises the real generated Sandbox SDK over MCP, with strict protocol
// fixtures for Core and business data. Deployed platform acceptance is separate.
func TestManagedActionFunctionRealSandboxSDK(t *testing.T) {
	if os.Getenv("BKN_ACTION_SDK_TEST") != "true" {
		t.Skip("requires Python 3 and a writable sandbox /workspace")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := filepath.Abs("../../../../../../infra/sandbox/runtime")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runtime, "sandbox_sdk", "bkn.py")); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"probe", "capacity", "denied", "failure"} {
		t.Run(mode, func(t *testing.T) {
			var reads, registrations, finishes atomic.Int32
			wantFailure := mode == "denied" || mode == "failure"
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-BKN-Effective-Subject-ID") != "user-sdk" ||
					r.Header.Get("X-BKN-Application-Principal-ID") != "app-sdk" {
					t.Error("managed lifecycle changed the authenticated caller")
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/conversations:ensure-current"):
					_, _ = w.Write([]byte(`{"conversation_id":"conv_sdk"}`))
				case strings.HasSuffix(r.URL.Path, "/interactions"):
					_, _ = w.Write([]byte(`{"interaction_id":"int_sdk","lease_token":"lease-sdk","lease_epoch":1}`))
				case strings.HasSuffix(r.URL.Path, "/operations:ensure"):
					registrations.Add(1)
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"error":{"code":"capture_disabled"}}`))
				case strings.HasSuffix(r.URL.Path, "/finish"):
					var body struct {
						Outcome string `json:"outcome"`
					}
					want := "handed_off"
					if wantFailure {
						want = "failed"
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Outcome != want {
						t.Errorf("lifecycle finish = %+v, %v; want %s", body, err, want)
					}
					finishes.Add(1)
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected recording request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer core.Close()
			mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer caller-sdk-token" {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				var rpc struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
					Params struct {
						Name      string         `json:"name"`
						Arguments map[string]any `json:"arguments"`
					} `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
					t.Error(err)
					http.Error(w, "bad JSON", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if rpc.Method == "notifications/initialized" {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				var result any = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "sdk-contract-fixture", "version": "1"}}
				if rpc.Method == "tools/call" {
					args := rpc.Params.Arguments
					bkn, _ := args["bkn_context"].(map[string]any)
					code := ""
					if bkn["conversation_id"] != "conv_sdk" || bkn["interaction_id"] != "int_sdk" {
						code = "conversation_required"
					} else if bkn["parent_operation_id"] != nil && bkn["parent_operation_id"] != "" {
						code = "parent_operation_not_found"
					} else if args["kn_id"] == "kn_denied" {
						code = "permission_denied"
					}
					var value any
					if code != "" {
						value = map[string]any{"error": map[string]any{"code": code}}
					} else {
						if rpc.Params.Name != "query_object_instance" || args["kn_id"] != "kn_sdk" {
							t.Errorf("unexpected business request: %s", rpc.Params.Name)
						}
						reads.Add(1)
						row := map[string]any{"product": "SKU-3163", "capacity": 10}
						if args["ot_id"] == "ot_demand" {
							row = map[string]any{"demand": 4}
						}
						value = map[string]any{"datas": []any{row}}
					}
					text, _ := json.Marshal(value)
					result = map[string]any{"isError": code != "", "content": []any{map[string]any{"type": "text", "text": string(text)}}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
			}))
			defer mcp.Close()
			client := &actionTraceClient{baseURL: core.URL, http: core.Client()}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			ctx, finish, err := client.Open(ctx, &interfaces.ActionExecution{
				ID: "exec-sdk", Executor: interfaces.AccountInfo{ID: "user-sdk", Type: "user"},
			}, interfaces.CallerRuntimeCredential{Authorization: "Bearer caller-sdk-token", ApplicationPrincipalID: "app-sdk"})
			if err != nil {
				t.Fatal(err)
			}
			scope := fmt.Sprintf("trace3163-sdk-%d", time.Now().UnixNano())
			t.Cleanup(func() { _ = os.RemoveAll(filepath.Join("/workspace", scope)) })
			result, runErr := client.Run(ctx, "function-sdk", func(runCtx context.Context) (any, error) {
				credential, _ := interfaces.CallerRuntimeCredentialFromContext(runCtx)
				event, _ := json.Marshal(map[string]any{
					"mcp": mcp.URL, "token": strings.TrimPrefix(credential.Authorization, "Bearer "),
					"bkn":             map[string]any{"conversation_id": credential.ConversationID, "interaction_id": credential.InteractionID, "parent_operation_id": credential.ParentOperationID},
					"workspace_scope": scope, "mode": mode,
				})
				command := exec.CommandContext(runCtx, python, "-c", realSDKFunction)
				command.Env = append(os.Environ(), "PYTHONPATH="+runtime)
				command.Stdin = bytes.NewReader(event)
				var stderr bytes.Buffer
				command.Stderr = &stderr
				output, err := command.Output()
				if err != nil {
					// The fixture reports only error classes, never credentials.
					for _, code := range []string{"conversation_required", "parent_operation_not_found", "permission_denied", "expected business failure"} {
						if strings.Contains(stderr.String(), code) {
							return nil, errors.New(code)
						}
					}
					return nil, fmt.Errorf("SDK process failed: %w", err)
				}
				var value map[string]any
				if err := json.Unmarshal(output, &value); err != nil {
					return nil, err
				}
				return value, nil
			})
			outcome := "handed_off"
			if runErr != nil {
				outcome = "failed"
			}
			finish(outcome)
			if wantFailure {
				want := "expected business failure"
				if mode == "denied" {
					want = "permission_denied"
				}
				if runErr == nil || runErr.Error() != want {
					t.Fatalf("SDK failure = %v; want %s", runErr, want)
				}
			} else if runErr != nil {
				t.Fatalf("real SDK query failed: %v", runErr)
			} else if value := result.(map[string]any); value["product"] != "SKU-3163" || mode == "capacity" && value["remaining"] != float64(6) {
				t.Fatalf("unexpected SDK business result: %v", value)
			}
			wantReads := int32(1)
			if mode == "capacity" {
				wantReads = 2
			} else if mode == "denied" {
				wantReads = 0
			}
			if reads.Load() != wantReads || registrations.Load() != 1 || finishes.Load() != 1 {
				t.Fatalf("reads=%d registration refusals=%d finishes=%d", reads.Load(), registrations.Load(), finishes.Load())
			}
		})
	}
}

const realSDKFunction = `
import json, sys
from sandbox_sdk import bkn
event = json.load(sys.stdin)
bkn.configure_runtime(event)
kn = 'kn_denied' if event['mode'] == 'denied' else 'kn_sdk'
row = bkn.query_object_instance(kn_id=kn, ot_id='ot_capacity', limit=1)['datas'][0]
result = {'product': row['product']}
if event['mode'] == 'capacity':
    demand = bkn.query_object_instance(kn_id=kn, ot_id='ot_demand', limit=1)['datas'][0]
    result['remaining'] = row['capacity'] - demand['demand']
if event['mode'] == 'failure':
    raise RuntimeError('expected business failure')
print(json.dumps(result))
`
