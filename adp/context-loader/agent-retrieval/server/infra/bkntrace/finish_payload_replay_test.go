// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type onceFinishArtifactWriter struct{ calls int }

func (w *onceFinishArtifactWriter) Put(context.Context, string, []byte) (string, string, error) {
	w.calls++
	if w.calls > 1 {
		return "", "", errors.New("artifact service unavailable after commit")
	}
	return "artifact:original-finish", "sha256:original", nil
}

func TestGuardFinishFreezesLargePayloadBeforeAcknowledgementRecovery(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "fail"}[failed], func(t *testing.T) {
			writer := &onceFinishArtifactWriter{}
			posts := 0
			var original string
			terminal := "completed"
			if failed {
				terminal = "failed"
			}
			client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return lifecycleJSONResponse(http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "trace_core_unavailable"}}), nil
				}
				posts++
				raw, _ := io.ReadAll(r.Body)
				if posts == 1 {
					original = string(raw)
					return nil, errors.New("committed acknowledgement lost")
				}
				if string(raw) != original {
					return lifecycleJSONResponse(http.StatusConflict, map[string]any{"error": map[string]any{"code": "idempotency_conflict"}}), nil
				}
				return lifecycleJSONResponse(http.StatusOK, OperationResult{Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: terminal}, Receipt: matchingFinishReceipt(terminal, "")}), nil
			}).WithPayloadArtifactWriter(writer)
			result, apiErr, err := NewGuard(client).Finish(finishLifecycleContext(false), pendingGuardState(), map[string]any{"rows": strings.Repeat("x", maxInlinePayloadBytes)}, failed, false)
			if err != nil || apiErr != nil || result.Receipt.ReceiptStatus != terminal || writer.calls != 1 || posts != 2 {
				t.Fatalf("large terminal envelope changed during recovery: result=%#v api=%#v err=%v uploads=%d posts=%d", result, apiErr, err, writer.calls, posts)
			}
		})
	}
}
