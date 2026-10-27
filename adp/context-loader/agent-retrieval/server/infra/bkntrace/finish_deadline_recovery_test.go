// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestGuardFinishReservesRecoveryAfterAttemptDeadline(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "write not committed", true: "committed acknowledgement lost"}[committed], func(t *testing.T) {
			writes, polls := 0, 0
			var original any
			client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				if r.Method == http.MethodPost {
					writes++
					raw, _ := io.ReadAll(r.Body)
					if writes == 1 {
						if err := json.Unmarshal(raw, &original); err != nil {
							t.Fatal(err)
						}
						<-r.Context().Done()
						return nil, r.Context().Err()
					}
					var repeated any
					if err := json.Unmarshal(raw, &repeated); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(repeated, original) {
						t.Fatal("recovery changed terminal payload")
					}
					return lifecycleJSONResponse(http.StatusOK, OperationResult{Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"}, Receipt: matchingFinishReceipt("completed", "")}), nil
				}
				if r.URL.Path == "/api/agent-observability/v1/receipts/receipt-1" {
					polls++
					status := "pending"
					if committed {
						status = "completed"
					}
					return lifecycleJSONResponse(http.StatusOK, matchingFinishReceipt(status, "")), nil
				}
				return lifecycleJSONResponse(http.StatusOK, Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"}), nil
			})
			ctx := WithTraceAvailability(finishLifecycleContext(false))
			ctx.Value(traceAvailabilityKey{}).(*traceAvailability).post = time.Second
			started := time.Now()
			result, apiErr, err := NewGuard(client).Finish(ctx, pendingGuardState(), "actual outcome", false, false)
			if err != nil || apiErr != nil || result.Receipt.ReceiptStatus != "completed" || polls != 1 {
				t.Fatalf("deadline exhausted recovery: result=%#v api=%#v err=%v writes=%d polls=%d", result, apiErr, err, writes, polls)
			}
			expected := 2
			if committed {
				expected = 1
			}
			if writes != expected {
				t.Fatalf("writes=%d want %d", writes, expected)
			}
			if elapsed := time.Since(started); elapsed > 1300*time.Millisecond {
				t.Fatalf("recovery extended the shared budget: %v", elapsed)
			}
		})
	}
}

func TestGuardFinishPersistentAttemptDeadlinesStayBounded(t *testing.T) {
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	ctx := WithTraceAvailability(finishLifecycleContext(false))
	ctx.Value(traceAvailabilityKey{}).(*traceAvailability).post = 100 * time.Millisecond
	started := time.Now()
	result, apiErr, err := NewGuard(client).Finish(ctx, pendingGuardState(), "actual outcome", false, false)
	if err != nil || apiErr == nil || apiErr.Code != "receipt_pending" || result.Receipt.ReceiptStatus != "pending" {
		t.Fatalf("invented completion: %#v %#v %v", result, apiErr, err)
	}
	if time.Since(started) > 200*time.Millisecond {
		t.Fatal("persistent outage extended the budget")
	}
}

func TestGuardFinishReservesReplayAfterReceiptReadDeadline(t *testing.T) {
	writes, polls := 0, 0
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Context().Err() != nil {
			return nil, r.Context().Err()
		}
		if r.Method == http.MethodPost {
			writes++
			if writes == 1 {
				return lifecycleJSONResponse(http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "trace_core_unavailable"}}), nil
			}
			return lifecycleJSONResponse(http.StatusOK, OperationResult{Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"}, Receipt: matchingFinishReceipt("completed", "")}), nil
		}
		polls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx := WithTraceAvailability(finishLifecycleContext(false))
	ctx.Value(traceAvailabilityKey{}).(*traceAvailability).post = time.Second
	result, apiErr, err := NewGuard(client).Finish(ctx, pendingGuardState(), "actual outcome", false, false)
	if err != nil || apiErr != nil || result.Receipt.ReceiptStatus != "completed" || writes != 2 || polls != 1 {
		t.Fatalf("receipt timeout exhausted replay: result=%#v api=%#v err=%v writes=%d polls=%d", result, apiErr, err, writes, polls)
	}
}
