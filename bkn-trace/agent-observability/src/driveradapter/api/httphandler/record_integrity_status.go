// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

// GetRecordIntegrityRetryStatus godoc
// @Summary Get recorded integrity retry diagnostics for an authorized interaction
// @Description Read-only status for the interaction's current integrity source version. Uses the same canonical authorization as persisted integrity reads; shared-network candidates do not widen that authorization; unauthorized and missing interactions both return 404. Never performs live inspection, claims work, or requeues a retry. No query parameters are accepted. At most 8 attempts over 30 minutes with backoff from 30 seconds to 5 minutes; a new source version schedules a new generation while retaining old retry history. Fixed inspection limits stop the current generation immediately with exhausted/inspection_budget_exceeded (8 MiB per artifact response, 32 MiB or 128 reads per interaction); this is an unknown verdict, not evidence loss. Existing exhausted generations are not requeued or rewritten on upgrade. A succeeded job means a verdict was saved, not that records or business conclusions are complete. Reason may describe exhaustion or source replacement; last_failure_reason retains the last actual failure. Zero timestamps mean no attempt/lease has been recorded.
// @Tags evidence
// @Produce json
// @Security BearerAuth
// @Param interaction_id path string true "Canonical interaction ID"
// @Success 200 {object} rdto.RecordIntegrityRetryStatusResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /interactions/{interaction_id}/record-integrity-status [get]
func (h *EvidenceHandler) GetRecordIntegrityRetryStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	scope, ok := h.queryScopeFromRequest(w, r, false)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "record integrity status does not accept query parameters"})
		return
	}
	id := strings.TrimSpace(r.PathValue("interaction_id"))
	if id == "" {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "interaction_id is required"})
		return
	}
	status, found, err := h.evidenceService.GetRecordIntegrityRetryStatus(r.Context(), id, scope)
	if err != nil {
		writeJSON(w, r, http.StatusInternalServerError, rdto.ErrorResponse{Code: "QUERY_FAILED", Message: "failed to read record integrity retry status"})
		return
	}
	if !found {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "NOT_FOUND", Message: "interaction not found"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, http.StatusOK, rdto.RecordIntegrityRetryStatusResponse{
		InteractionID: status.InteractionID, SourceVersion: status.SourceVersion, Status: status.Status, Attempts: status.Attempts,
		FirstAttemptAt: status.FirstAttemptAt, LastAttemptAt: status.LastAttemptAt, NextAttemptAt: status.NextAttemptAt, LeaseUntil: status.LeaseUntil, LastFailureReason: status.LastFailureReason, Reason: status.Reason,
	})
}
