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
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

func TestStartInteractionRecoversCommittedAcknowledgementDeadline(t *testing.T) {
	calls := 0
	original := ""
	client := bkntrace.NewLifecycleClient("http://start.test", &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if calls == 1 {
			original = string(raw)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		if string(raw) != original {
			t.Fatal("retry changed start identity or payload")
		}
		return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Interaction{InteractionID: "int-committed", ConversationID: "conv-committed", ExecutionStatus: "active", LeaseToken: "lease-committed", LeaseEpoch: 1}), nil
	})})
	ctx, cancel := context.WithTimeout(startArtifactTestContext(), time.Second)
	defer cancel()
	started := time.Now()
	result, err := handleLifecycleTool(client, "bkn_start_interaction")(ctx, startArtifactTestRequest())
	if err != nil || result == nil || result.IsError || calls != 2 {
		t.Fatalf("committed interaction hidden after timeout: result=%#v err=%v calls=%d", result, err, calls)
	}
	value, ok := result.StructuredContent.(map[string]any)
	if !ok || value["interaction_id"] != "int-committed" || value["conversation_id"] != "conv-committed" {
		t.Fatalf("lost authoritative identity: %#v", result.StructuredContent)
	}
	if time.Since(started) > 1300*time.Millisecond {
		t.Fatal("start recovery extended caller budget")
	}
}

// Exercise the real HTTP client: Core commits once, the first response never
// arrives, and the retry receives the committed identity from the same key.
func TestStartInteractionLostHTTPResponseCreatesOnlyOneInteraction(t *testing.T) {
	var requests, commits atomic.Int32
	var mu sync.Mutex
	var original map[string]any
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("invalid start body")
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("X-BKN-Application-Principal-ID") != "cursor-app" || r.Header.Get("X-BKN-Effective-Subject-ID") != "user-1" {
			t.Error("start replay lost authenticated owner")
		}
		mu.Lock()
		if original == nil {
			original = input
			commits.Add(1)
		} else if !reflect.DeepEqual(original, input) {
			t.Error("start retry changed identity or question")
		}
		mu.Unlock()
		if input["idempotency_key"] == "" || input["idempotency_key"] == nil {
			t.Error("missing start key")
		}
		if requests.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		_ = json.NewEncoder(w).Encode(bkntrace.Interaction{InteractionID: "int-committed", ConversationID: "conv-committed", ExecutionStatus: "active", LeaseToken: "lease-committed", LeaseEpoch: 1})
	}))
	defer core.Close()
	ctx, cancel := context.WithTimeout(startArtifactTestContext(), time.Second)
	defer cancel()
	result, err := handleLifecycleTool(bkntrace.NewLifecycleClient(core.URL, core.Client()), "bkn_start_interaction")(ctx, startArtifactTestRequest())
	if err != nil || result == nil || result.IsError || commits.Load() != 1 || requests.Load() != 2 {
		t.Fatalf("lost HTTP ACK duplicated or hid committed interaction: result=%#v err=%v commits=%d requests=%d", result, err, commits.Load(), requests.Load())
	}
}
