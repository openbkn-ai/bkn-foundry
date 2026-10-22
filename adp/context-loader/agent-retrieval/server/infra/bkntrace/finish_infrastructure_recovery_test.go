package bkntrace

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestGuardFinishRecoversInfrastructureErrorWithoutRetryableFlag(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "complete"
		terminal := "completed"
		if failed {
			name = "fail"
			terminal = "failed"
		}
		t.Run(name, func(t *testing.T) {
			finishCalls, polls := 0, 0
			client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost {
					finishCalls++
					if finishCalls == 1 {
						// This is the actual Core envelope for a non-domain storage failure.
						return lifecycleJSONResponse(http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "internal_error", "message": "lifecycle request failed", "retryable": false}}), nil
					}
					return lifecycleJSONResponse(http.StatusOK, OperationResult{
						Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: terminal},
						Receipt:   matchingFinishReceipt(terminal, ""),
					}), nil
				}
				polls++
				return lifecycleJSONResponse(http.StatusOK, Receipt{ReceiptID: "receipt-1", ReceiptStatus: "pending"}), nil
			})
			result, apiErr, err := NewGuard(client).Finish(finishLifecycleContext(false), pendingGuardState(), "actual outcome", failed, false)
			if err != nil || apiErr != nil || result.Receipt.ReceiptStatus != terminal || finishCalls != 2 || polls != 1 {
				t.Fatalf("transient Core 500 abandoned terminal write: result=%#v api=%#v err=%v writes=%d polls=%d", result, apiErr, err, finishCalls, polls)
			}
		})
	}
}

func TestGuardFinishDoesNotRetryDomainRefusals(t *testing.T) {
	for _, code := range []string{"permission_denied", "resource_not_disclosed", "terminal_conflict", "attempt_conflict"} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return lifecycleJSONResponse(http.StatusForbidden, map[string]any{"error": map[string]any{"code": code, "retryable": false}}), nil
			})
			_, apiErr, err := NewGuard(client).Finish(finishLifecycleContext(false), pendingGuardState(), "actual outcome", false, false)
			if err != nil || apiErr == nil || apiErr.Code != code || calls != 1 {
				t.Fatalf("domain refusal retried or lost: api=%#v err=%v calls=%d", apiErr, err, calls)
			}
		})
	}
}

func TestGuardFinishPersistentInfrastructureFailureStaysPending(t *testing.T) {
	writes := 0
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			writes++
			return lifecycleJSONResponse(http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "internal_error", "retryable": false}}), nil
		}
		return lifecycleJSONResponse(http.StatusOK, Receipt{ReceiptID: "receipt-1", ReceiptStatus: "pending"}), nil
	})
	result, apiErr, err := NewGuard(client).Finish(finishLifecycleContext(false), pendingGuardState(), "actual outcome", false, false)
	if err != nil || apiErr == nil || apiErr.Code != "receipt_pending" || result.Receipt.ReceiptID != "receipt-1" || writes != finishRetryAttempts {
		t.Fatalf("persistent outage invented terminal state or lost stable receipt: result=%#v api=%#v err=%v writes=%d", result, apiErr, err, writes)
	}
}

func TestGuardFinishRecoversTransientOperationReadFailure(t *testing.T) {
	writes, operationReads := 0, 0
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			writes++
			if writes == 1 {
				return nil, errors.New("acknowledgement lost")
			}
			return lifecycleJSONResponse(http.StatusOK, OperationResult{Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"}, Receipt: matchingFinishReceipt("completed", "")}), nil
		}
		if strings.Contains(r.URL.Path, "/receipts/") {
			return lifecycleJSONResponse(http.StatusOK, matchingFinishReceipt("completed", "")), nil
		}
		operationReads++
		return lifecycleJSONResponse(http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "internal_error", "retryable": false}}), nil
	})
	result, apiErr, err := NewGuard(client).Finish(finishLifecycleContext(false), pendingGuardState(), "actual outcome", false, false)
	if err != nil || apiErr != nil || result.Receipt.ReceiptStatus != "completed" || writes != 2 || operationReads != 1 {
		t.Fatalf("transient recovery-read failure abandoned same finish: result=%#v api=%#v err=%v writes=%d reads=%d", result, apiErr, err, writes, operationReads)
	}
}
