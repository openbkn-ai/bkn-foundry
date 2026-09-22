// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
)

func TestCapturePolicyHandlerReturnsTruthfulStateModel(t *testing.T) {
	handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{
			Revision:       9,
			DesiredState:   capturepolicysvc.StateEnabled,
			EffectiveState: capturepolicysvc.StateEnabling,
			Operation: capturepolicysvc.Operation{
				ID: "op-9", Phase: capturepolicysvc.PhaseEnabling, RequestedState: capturepolicysvc.StateEnabled,
			},
		}, nil
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/trace-evidence-configuration", nil)
	response := httptest.NewRecorder()
	handler.GetTraceEvidenceConfiguration(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["desired_state"] != string(capturepolicysvc.StateEnabled) || payload["effective_state"] != string(capturepolicysvc.StateEnabling) {
		t.Fatalf("truthful state fields missing: %v", payload)
	}
	operation, ok := payload["operation"].(map[string]any)
	if !ok || operation["phase"] != string(capturepolicysvc.PhaseEnabling) {
		t.Fatalf("operation phase missing: %v", payload["operation"])
	}
}

func TestCapturePolicyHandlerRejectsNonGet(t *testing.T) {
	handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{}, nil
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/trace-evidence-configuration", nil)
	response := httptest.NewRecorder()
	handler.GetTraceEvidenceConfiguration(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", response.Code)
	}
}
