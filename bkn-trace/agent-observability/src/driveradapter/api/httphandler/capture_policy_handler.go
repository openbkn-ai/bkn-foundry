// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

// CapturePolicyHandler exposes the frozen Trace/Evidence control-plane
// read/write model. Bootstrap owns the route and Access Profile middleware.
type CapturePolicyHandler struct {
	service    *capturepolicysvc.Service
	commander  capturepolicysvc.Commander
	reconciler interface {
		Reconcile(context.Context) (bool, error)
	}
}

func NewCapturePolicyHandler(reader capturepolicysvc.Reader, commanders ...capturepolicysvc.Commander) *CapturePolicyHandler {
	var commander capturepolicysvc.Commander
	if len(commanders) > 0 {
		commander = commanders[0]
	}
	return &CapturePolicyHandler{service: capturepolicysvc.New(reader), commander: commander}
}

func NewCapturePolicyHandlerWithReconciler(reader capturepolicysvc.Reader, commander capturepolicysvc.Commander, reconciler interface {
	Reconcile(context.Context) (bool, error)
}) *CapturePolicyHandler {
	return &CapturePolicyHandler{service: capturepolicysvc.New(reader), commander: commander, reconciler: reconciler}
}

// HandleTraceEvidenceConfiguration dispatches the stable configuration
// contract. GET is the truthful read model; PUT creates a guarded operation
// and returns 202 while effective state converges asynchronously.
func (h *CapturePolicyHandler) HandleTraceEvidenceConfiguration(w http.ResponseWriter, r *http.Request) {
	if r != nil && r.Method == http.MethodGet {
		h.GetTraceEvidenceConfiguration(w, r)
		return
	}
	ensureResponseTraceID(w, r)
	if r == nil || r.Method != http.MethodPut {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET and PUT are supported"})
		return
	}
	if h == nil || h.commander == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "POLICY_RECONCILER_UNAVAILABLE", Message: "capture policy command path is not configured"})
		return
	}
	var request capturepolicysvc.ChangeRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedRevision == 0 || (request.DesiredState != capturepolicysvc.StateEnabled && request.DesiredState != capturepolicysvc.StateDisabled) {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_CONFIGURATION_REQUEST", Message: "desired_state and expected_revision are required"})
		return
	}
	snapshot, err := h.commander.Request(contextWithRequest(r), request)
	if err != nil {
		status, code := http.StatusServiceUnavailable, "POLICY_RECONCILER_UNAVAILABLE"
		if errors.Is(err, capturepolicysvc.ErrRevisionConflict) {
			status, code = http.StatusConflict, "CONFIG_REVISION_CONFLICT"
		} else if errors.Is(err, capturepolicysvc.ErrOperationInProgress) {
			status, code = http.StatusConflict, "TRACE_EVIDENCE_OPERATION_IN_PROGRESS"
		}
		writeJSON(w, r, status, rdto.ErrorResponse{Code: code, Message: "capture policy change was not accepted"})
		return
	}
	writeJSON(w, r, http.StatusAccepted, rdto.TraceEvidenceConfigurationResponse(snapshot))
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

func (h *CapturePolicyHandler) GetTraceEvidenceOperation(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	operationID := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api/agent-observability/v1/trace-evidence-operations/")
	if operationID == "" || strings.Contains(operationID, "/") || h == nil || h.service == nil {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "TRACE_EVIDENCE_OPERATION_NOT_FOUND", Message: "capture policy operation was not found"})
		return
	}
	operation, err := h.service.ReadOperation(contextWithRequest(r), operationID)
	if err == nil {
		writeJSON(w, r, http.StatusOK, operation)
		return
	}
	// Memory/contract readers may expose only a current snapshot. Durable
	// MariaDB readers implement OperationReader, so historical operation IDs
	// remain addressable after a newer operation becomes active.
	snapshot, snapshotErr := h.service.Read(contextWithRequest(r))
	if snapshotErr != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_UNAVAILABLE", Message: "capture policy is not available"})
		return
	}
	if snapshot.Operation.ID != operationID {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "TRACE_EVIDENCE_OPERATION_NOT_FOUND", Message: "capture policy operation was not found"})
		return
	}
	writeJSON(w, r, http.StatusOK, snapshot.Operation)
}

func (h *CapturePolicyHandler) ReconcileTraceEvidenceOperation(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only POST is supported"})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	path = strings.TrimSuffix(path, ":reconcile")
	operationID := strings.TrimPrefix(path, "/api/agent-observability/v1/trace-evidence-operations/")
	if operationID == "" || strings.Contains(operationID, "/") || h == nil || h.service == nil {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "TRACE_EVIDENCE_OPERATION_NOT_FOUND", Message: "capture policy operation was not found"})
		return
	}
	if h.reconciler == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "POLICY_RECONCILER_UNAVAILABLE", Message: "capture policy reconciler is not configured"})
		return
	}
	before, err := h.service.Read(contextWithRequest(r))
	if err != nil || before.Operation.ID != operationID {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "TRACE_EVIDENCE_OPERATION_NOT_FOUND", Message: "capture policy operation was not found"})
		return
	}
	if _, err := h.reconciler.Reconcile(contextWithRequest(r)); err != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "POLICY_RECONCILE_FAILED", Message: "capture policy reconciliation failed"})
		return
	}
	after, err := h.service.Read(contextWithRequest(r))
	if err != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_UNAVAILABLE", Message: "capture policy is not available"})
		return
	}
	writeJSON(w, r, http.StatusAccepted, rdto.TraceEvidenceConfigurationResponse(after))
}

func contextWithRequest(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return r.Context()
}
