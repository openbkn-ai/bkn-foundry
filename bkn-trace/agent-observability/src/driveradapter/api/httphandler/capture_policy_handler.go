// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

// CapturePolicyHandler is deliberately not registered by bootstrap in this
// batch. Session 0 owns the shared route, OpenAPI and authorization wiring.
type CapturePolicyHandler struct {
	service *capturepolicysvc.Service
}

func NewCapturePolicyHandler(reader capturepolicysvc.Reader) *CapturePolicyHandler {
	return &CapturePolicyHandler{service: capturepolicysvc.New(reader)}
}

// GetTraceEvidenceConfiguration returns desired/effective state separately;
// clients must not infer rollout progress from a boolean enabled field.
func (h *CapturePolicyHandler) GetTraceEvidenceConfiguration(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	if h == nil || h.service == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_UNAVAILABLE", Message: "capture policy is not configured"})
		return
	}
	snapshot, err := h.service.Read(contextWithRequest(r))
	if err != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_UNAVAILABLE", Message: "capture policy is not available"})
		return
	}
	writeJSON(w, r, http.StatusOK, rdto.TraceEvidenceConfigurationResponse(snapshot))
}

func contextWithRequest(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return r.Context()
}
