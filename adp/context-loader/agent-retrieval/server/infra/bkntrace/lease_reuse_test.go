// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLifecycleClientReusesLeaseAfterStart(t *testing.T) {
	gets, posts := 0, 0
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			return lifecycleJSONResponse(200, activeLease()), nil
		}
		if r.URL.Path == coreAPIPath+"/conversations/conv-1/interactions" {
			return lifecycleJSONResponse(201, activeLease()), nil
		}
		posts++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["lease_token"] != "lease-1" || body["lease_epoch"] != float64(7) {
			t.Errorf("wrong lease: %#v", body)
		}
		return lifecycleJSONResponse(201, OperationResult{Execute: true}), nil
	})
	ctx := testTraceContext()
	_, apiErr, err := client.StartInteraction(ctx, "conv-1", "start-1")
	if apiErr != nil || err != nil {
		t.Fatalf("start: %v %v", apiErr, err)
	}
	for i := 0; i < 2; i++ {
		ensureLeaseOperation(t, client, ctx)
	}
	if gets != 0 || posts != 2 {
		t.Fatalf("start followed by two ensures: GET=%d ensure=%d, want 0/2", gets, posts)
	}
}

func activeLease() Interaction {
	return Interaction{InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 7, LeaseExpiresAt: time.Now().Add(5 * time.Minute)}
}

func ensureLeaseOperation(t *testing.T, client *LifecycleClient, ctx context.Context) {
	t.Helper()
	_, apiErr, err := client.EnsureOperation(ctx, EnsureOperationInput{ConversationID: "conv-1", InteractionID: "int-1", OperationKey: "op-1", ToolName: "search_schema", Protocol: "mcp", SourceModule: "context-loader", Input: json.RawMessage(`{"query":"material"}`)})
	if apiErr != nil || err != nil {
		t.Fatalf("ensure: %v %v", apiErr, err)
	}
}

func TestLifecycleClientReusesLeaseAcrossEnsures(t *testing.T) {
	gets := 0
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			return lifecycleJSONResponse(200, activeLease()), nil
		}
		return lifecycleJSONResponse(200, OperationResult{Execute: false, Receipt: Receipt{ReceiptStatus: "completed"}}), nil
	})
	for i := 0; i < 3; i++ {
		ensureLeaseOperation(t, client, testTraceContext())
	}
	if gets != 1 {
		t.Fatalf("three ensures read interaction %d times, want 1", gets)
	}
}

func TestLifecycleClientLeaseIsolatedByTrustedOwner(t *testing.T) {
	gets := 0
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			return lifecycleJSONResponse(200, activeLease()), nil
		}
		return lifecycleJSONResponse(200, OperationResult{}), nil
	})
	ctx := testTraceContext()
	ensureLeaseOperation(t, client, ctx)
	for _, auth := range []*interfaces.AccountAuthContext{
		{AccountID: "another-user", AccountType: interfaces.AccessorTypeUser},
		{AccountID: "acct_demo", AccountType: interfaces.AccessorTypeApp},
		{AccountID: "acct_demo", AccountType: interfaces.AccessorTypeUser, TokenInfo: &interfaces.TokenInfo{ClientID: "another-app"}},
	} {
		ensureLeaseOperation(t, client, common.SetAccountAuthContextToCtx(ctx, auth))
	}
	if gets != 4 {
		t.Fatalf("different owners shared lease: GET=%d, want 4", gets)
	}
	_, apiErr, err := client.EnsureOperation(context.Background(), EnsureOperationInput{ConversationID: "conv-1", InteractionID: "int-1"})
	if err != nil || apiErr == nil || apiErr.Code != "permission_denied" {
		t.Fatalf("missing identity: %v %v", apiErr, err)
	}
}

func TestLifecycleClientExpiredLeaseIsNotReused(t *testing.T) {
	gets := 0
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			interaction := activeLease()
			interaction.LeaseExpiresAt = time.Now().Add(-time.Second)
			return lifecycleJSONResponse(200, interaction), nil
		}
		return lifecycleJSONResponse(200, OperationResult{}), nil
	})
	ensureLeaseOperation(t, client, testTraceContext())
	ensureLeaseOperation(t, client, testTraceContext())
	if gets != 2 {
		t.Fatalf("expired lease reused: GET=%d", gets)
	}
}

func TestLifecycleClientRefreshesFencedLeaseOnce(t *testing.T) {
	gets, posts := 0, 0
	var inputs []map[string]any
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			interaction := activeLease()
			if gets > 1 {
				interaction.LeaseToken = "lease-2"
				interaction.LeaseEpoch = 8
			}
			return lifecycleJSONResponse(200, interaction), nil
		}
		posts++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		inputs = append(inputs, body)
		if posts == 2 {
			return lifecycleJSONResponse(409, map[string]any{"error": APIError{Code: "terminal_conflict"}}), nil
		}
		return lifecycleJSONResponse(200, OperationResult{}), nil
	})
	ensureLeaseOperation(t, client, testTraceContext())
	ensureLeaseOperation(t, client, testTraceContext())
	if gets != 2 || posts != 3 {
		t.Fatalf("refresh GET=%d POST=%d, want 2/3", gets, posts)
	}
	if inputs[2]["lease_token"] != "lease-2" || inputs[2]["lease_epoch"] != float64(8) {
		t.Fatalf("did not use authoritative refresh: %#v", inputs[2])
	}
	for _, field := range []string{"operation_key", "input", "tool_name", "required"} {
		if !reflect.DeepEqual(inputs[1][field], inputs[2][field]) {
			t.Errorf("retry changed %s", field)
		}
	}
}

func TestLifecycleClientDoesNotRetryTerminalOrUnauthorizedEnsure(t *testing.T) {
	for _, code := range []string{"interaction_terminal", "resource_not_disclosed", "permission_denied"} {
		t.Run(code, func(t *testing.T) {
			gets, posts := 0, 0
			client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					gets++
					return lifecycleJSONResponse(200, activeLease()), nil
				}
				posts++
				if posts > 1 {
					return lifecycleJSONResponse(409, map[string]any{"error": APIError{Code: code}}), nil
				}
				return lifecycleJSONResponse(200, OperationResult{}), nil
			})
			ctx := testTraceContext()
			ensureLeaseOperation(t, client, ctx)
			_, _, disposition, apiErr, err := NewGuard(client).Begin(ctx, GuardIntent{Context: BusinessContext{ConversationID: "conv-1", InteractionID: "int-1", OperationKey: "op-2"}, ToolName: "search_schema", Input: json.RawMessage(`{}`)})
			if err != nil || apiErr == nil || apiErr.Code != code || disposition == GuardExecute {
				t.Fatalf("rejected admission authorized execution: %s %v %v", disposition, apiErr, err)
			}
			if gets != 1 || posts != 2 {
				t.Fatalf("non-fencing error was retried: GET=%d POST=%d", gets, posts)
			}
			if _, found := client.cachedLease(ctx, "conv-1", "int-1"); found {
				t.Fatal("rejected lease retained")
			}
		})
	}
}

func TestLifecycleClientTerminalResponseInvalidatesLease(t *testing.T) {
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		value := activeLease()
		if strings.HasSuffix(r.URL.Path, "finish") {
			value.ExecutionStatus = "completed"
		}
		return lifecycleJSONResponse(200, value), nil
	})
	ctx := testTraceContext()
	var interaction Interaction
	apiErr, err := client.Call(ctx, http.MethodPost, "/start", map[string]any{}, &interaction)
	if err != nil || apiErr != nil {
		t.Fatalf("start: %v %v", apiErr, err)
	}
	if _, found := client.cachedLease(ctx, "conv-1", "int-1"); !found {
		t.Fatal("Call start did not seed lease")
	}
	apiErr, err = client.Call(ctx, http.MethodPost, "/finish", map[string]any{}, &interaction)
	if err != nil || apiErr != nil {
		t.Fatalf("finish: %v %v", apiErr, err)
	}
	if _, found := client.cachedLease(ctx, "conv-1", "int-1"); found {
		t.Fatal("terminal lease retained")
	}
}

func TestLifecycleClientLeaseCacheBoundedAndConcurrent(t *testing.T) {
	client := NewLifecycleClient("http://core.test", nil)
	ctx := testTraceContext()
	var wg sync.WaitGroup
	for i := 0; i < 512; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := activeLease()
			value.InteractionID = fmt.Sprintf("int-%d", i)
			client.rememberLease(ctx, value)
			client.cachedLease(ctx, value.ConversationID, value.InteractionID)
		}(i)
	}
	wg.Wait()
	if len(client.leases) > maxCachedInteractionLeases {
		t.Fatalf("unbounded cache: %d", len(client.leases))
	}
	for _, entry := range client.leases {
		if entry.expiresAt.After(time.Now().Add(time.Minute)) {
			t.Fatal("cache exceeded TTL")
		}
	}
}

func TestLifecycleClientFencedRefreshStopsOnFailure(t *testing.T) {
	for _, stage := range []string{"read_unavailable", "read_terminal", "ensure_fenced"} {
		t.Run(stage, func(t *testing.T) {
			gets, posts := 0, 0
			client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					gets++
					if gets > 1 && stage == "read_unavailable" {
						return nil, errors.New("Core unavailable")
					}
					value := activeLease()
					if gets > 1 && stage == "read_terminal" {
						value.ExecutionStatus = "completed"
					}
					return lifecycleJSONResponse(200, value), nil
				}
				posts++
				if posts > 1 {
					return lifecycleJSONResponse(409, map[string]any{"error": APIError{Code: "terminal_conflict"}}), nil
				}
				return lifecycleJSONResponse(200, OperationResult{}), nil
			})
			ctx := testTraceContext()
			ensureLeaseOperation(t, client, ctx)
			_, _, disposition, apiErr, err := NewGuard(client).Begin(ctx, GuardIntent{Context: BusinessContext{ConversationID: "conv-1", InteractionID: "int-1", OperationKey: "op-2"}, ToolName: "search_schema", Input: json.RawMessage(`{}`)})
			if (apiErr == nil && err == nil) || disposition == GuardExecute {
				t.Fatalf("refresh failure authorized execution: %s %v %v", disposition, apiErr, err)
			}
			switch stage {
			case "read_unavailable":
				if err == nil || !strings.Contains(err.Error(), "Core unavailable") {
					t.Fatalf("lost refresh transport error: %v", err)
				}
			case "read_terminal":
				if apiErr == nil || apiErr.Code != "interaction_terminal" {
					t.Fatalf("lost terminal error: %v", apiErr)
				}
			case "ensure_fenced":
				if apiErr == nil || apiErr.Code != "terminal_conflict" {
					t.Fatalf("lost fencing error: %v", apiErr)
				}
			}
			expectedPosts := 2
			if stage == "ensure_fenced" {
				expectedPosts = 3
			}
			if gets != 2 || posts != expectedPosts {
				t.Fatalf("unbounded refresh: GET=%d POST=%d", gets, posts)
			}
		})
	}
}

func TestLifecycleClientExpiredCacheEntryReadsCoreAgain(t *testing.T) {
	gets := 0
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			return lifecycleJSONResponse(200, activeLease()), nil
		}
		return lifecycleJSONResponse(200, OperationResult{}), nil
	})
	ctx := testTraceContext()
	ensureLeaseOperation(t, client, ctx)
	key, _ := leaseKey(ctx, "conv-1", "int-1")
	client.leaseMu.Lock()
	entry := client.leases[key]
	entry.expiresAt = time.Now().Add(-time.Second)
	client.leases[key] = entry
	client.leaseMu.Unlock()
	ensureLeaseOperation(t, client, ctx)
	if gets != 2 {
		t.Fatalf("expired cache entry avoided Core read: %d", gets)
	}
}

func TestManagedGuardKeepsAdmissionAndSettlementWithReusedLease(t *testing.T) {
	gets, ensures, finishes := 0, 0, 0
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			return lifecycleJSONResponse(200, activeLease()), nil
		}
		if strings.HasSuffix(r.URL.Path, "/interactions") {
			return lifecycleJSONResponse(201, activeLease()), nil
		}
		result := OperationResult{Operation: Operation{OperationID: "op-1", Attempt: 1}, Receipt: Receipt{ReceiptID: "rcpt-1", ReceiptStatus: "pending"}, Created: true, Execute: true}
		if strings.HasSuffix(r.URL.Path, "/operations:ensure") {
			ensures++
		} else {
			if !strings.HasSuffix(r.URL.Path, "/attempts/1:complete") {
				t.Fatalf("unexpected request %s", r.URL.Path)
			}
			finishes++
			result.Receipt.ReceiptStatus = "completed"
		}
		return lifecycleJSONResponse(200, result), nil
	})
	ctx := testTraceContext()
	_, apiErr, err := client.StartInteraction(ctx, "conv-1", "start-1")
	if apiErr != nil || err != nil {
		t.Fatalf("start: %v %v", apiErr, err)
	}
	guard := NewGuard(client)
	for i := 0; i < 2; i++ {
		callCtx, state, disposition, apiErr, err := guard.Begin(ctx, GuardIntent{Context: BusinessContext{ConversationID: "conv-1", InteractionID: "int-1", OperationKey: fmt.Sprintf("op-%d", i)}, ToolName: "search_schema", Protocol: "mcp", SourceModule: "context-loader", Input: json.RawMessage(`{}`)})
		if err != nil || apiErr != nil || disposition != GuardExecute {
			t.Fatalf("admission: %s %v %v", disposition, apiErr, err)
		}
		result, apiErr, err := guard.Finish(callCtx, state, map[string]any{"result": "ok"}, false, false)
		if err != nil || apiErr != nil || result.Receipt.ReceiptStatus != "completed" {
			t.Fatalf("settlement: %+v %v %v", result, apiErr, err)
		}
	}
	if gets != 0 || ensures != 2 || finishes != 2 {
		t.Fatalf("GET=%d admission=%d settlement=%d, want 0/2/2", gets, ensures, finishes)
	}
}

func TestLifecycleClientNilStillReportsNotInstalled(t *testing.T) {
	var client *LifecycleClient
	_, apiErr, err := client.EnsureOperation(testTraceContext(), EnsureOperationInput{ConversationID: "conv-1", InteractionID: "int-1"})
	if apiErr != nil || !errors.Is(err, ErrFeatureNotInstalled) {
		t.Fatalf("disabled client: %v %v", apiErr, err)
	}
}
