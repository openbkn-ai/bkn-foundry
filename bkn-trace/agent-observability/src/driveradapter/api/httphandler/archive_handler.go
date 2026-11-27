// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

type ArchiveHandler struct {
	service    *archivesvc.Service
	authorizer *EvidenceHandler
}

func NewArchiveHandler(service *archivesvc.Service, authorizer *EvidenceHandler) *ArchiveHandler {
	return &ArchiveHandler{service: service, authorizer: authorizer}
}

func (handler *ArchiveHandler) Overview(kind observabilityvo.ArchiveKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := handler.authorize(w, r, http.MethodGet)
		if !ok {
			return
		}
		overview, err := handler.service.Overview(r.Context(), kind)
		if err != nil {
			writeObservabilityError(w, r, http.StatusServiceUnavailable, "archive_overview_failed", "archive overview is unavailable")
			return
		}
		response := rdto.ArchiveOverviewResponse{ArchiveKind: string(overview.Kind), RetentionDays: overview.RetentionDays, CutoffAt: overview.Range.To.Format("2006-01-02T15:04:05Z07:00"), CandidateCount: overview.CandidateCount}
		if overview.StorageReady {
			response.Storage.Status = "ready"
		} else {
			response.Storage.Status = "unavailable"
		}
		response.RetainedCount = overview.RetainedCount
		if overview.CurrentRun != nil {
			run := rdto.NewArchiveRun(*overview.CurrentRun)
			response.CurrentRun = &run
		}
		writeJSON(w, r, http.StatusOK, response)
	}
}
func (handler *ArchiveHandler) Create(kind observabilityvo.ArchiveKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := handler.authorize(w, r, http.MethodPost)
		if !ok {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || len(body) != 0 {
			writeObservabilityError(w, r, http.StatusBadRequest, "invalid_archive_request", "archive request body must be an empty object")
			return
		}
		run, err := handler.service.Submit(r.Context(), kind)
		if err != nil {
			writeArchiveSubmitError(w, r, err)
			return
		}
		writeJSON(w, r, http.StatusAccepted, rdto.NewArchiveRun(run))
	}
}

// writeArchiveSubmitError covers only what is known when a run is queued;
// batch outcomes are recorded on the run and read back from the overview.
func writeArchiveSubmitError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, archivesvc.ErrArchiveBusy):
		writeObservabilityError(w, r, http.StatusConflict, "archive_busy", "archive operation is already running")
	case errors.Is(err, archivesvc.ErrNothingToArchive):
		writeObservabilityError(w, r, http.StatusConflict, "archive_nothing_to_do", "no eligible records are available")
	case errors.Is(err, archivesvc.ErrCleanupRequired):
		writeObservabilityError(w, r, http.StatusConflict, "archive_cleanup_required", "previous cleanup must be completed first")
	case errors.Is(err, archivesvc.ErrRunNotFound):
		writeObservabilityError(w, r, http.StatusNotFound, "archive_job_not_found", "archive job was not found")
	default:
		slog.WarnContext(r.Context(), "archive submission failed", "error_type", fmt.Sprintf("%T", err))
		writeObservabilityError(w, r, http.StatusInternalServerError, "archive_failed", "archive did not start")
	}
}

// ListOrCreate exposes recent history and all unfinished cleanup on the same
// resource. Archive creation remains POST-only and never accepts a caller
// supplied cutoff or retention period.
func (handler *ArchiveHandler) ListOrCreate(kind observabilityvo.ArchiveKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handler.Create(kind)(w, r)
			return
		}
		_, ok := handler.authorize(w, r, http.MethodGet)
		if !ok {
			return
		}
		jobs, err := handler.service.ListJobs(r.Context(), kind, 20)
		if err != nil {
			reason := "read_failed"
			if errors.Is(err, context.DeadlineExceeded) {
				reason = "timeout"
			} else if errors.Is(err, context.Canceled) {
				reason = "canceled"
			}
			// Log only the kind and error class: driver errors may carry connection details.
			slog.WarnContext(r.Context(), "archive history unavailable", "archive_kind", kind, "dependency", "mariadb", "reason", reason, "error_type", fmt.Sprintf("%T", err))
			writeObservabilityError(w, r, http.StatusServiceUnavailable, "archive_history_unavailable", "archive history is unavailable; refresh before starting another archive")
			return
		}
		response := make([]rdto.ArchiveJobResponse, 0, len(jobs))
		for _, job := range jobs {
			response = append(response, rdto.NewArchiveJob(job))
		}
		writeJSON(w, r, http.StatusOK, response)
	}
}
func (handler *ArchiveHandler) GetOrRetry(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/observability/v1/archive-jobs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	method := http.MethodGet
	if len(parts) == 2 && parts[1] == "retry-cleanup" {
		method = http.MethodPost
	}
	_, ok := handler.authorize(w, r, method)
	if !ok {
		return
	}
	if method == http.MethodGet {
		if len(parts) == 2 && parts[1] == "download" {
			handler.download(w, r, parts[0])
			return
		}
		job, found := handler.service.Get(parts[0])
		if !found {
			writeObservabilityError(w, r, http.StatusNotFound, "archive_job_not_found", "archive job was not found")
			return
		}
		writeJSON(w, r, http.StatusOK, rdto.NewArchiveJob(job))
		return
	}
	run, err := handler.service.SubmitRetryCleanup(r.Context(), parts[0])
	if err != nil {
		if errors.Is(err, archivesvc.ErrArchiveBusy) || errors.Is(err, archivesvc.ErrRunNotFound) {
			writeArchiveSubmitError(w, r, err)
			return
		}
		writeObservabilityError(w, r, http.StatusBadRequest, "archive_retry_failed", "archive cleanup cannot be retried")
		return
	}
	writeJSON(w, r, http.StatusAccepted, rdto.NewArchiveRun(run))
}

// RunResult returns one background run, or one page of the interactions it
// kept online at /archive-runs/{run_id}/retained.
func (handler *ArchiveHandler) RunResult(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/observability/v1/archive-runs/"), "/"), "/")
	if parts[0] == "" || len(parts) > 2 || len(parts) == 2 && parts[1] != "retained" {
		http.NotFound(w, r)
		return
	}
	if _, ok := handler.authorize(w, r, http.MethodGet); !ok {
		return
	}
	if len(parts) == 2 {
		handler.writeRetainedPage(w, r, parts[0])
		return
	}
	run, err := handler.service.RunResult(r.Context(), parts[0])
	if errors.Is(err, archivesvc.ErrRunNotFound) {
		writeObservabilityError(w, r, http.StatusNotFound, "archive_run_not_found", "archive run was not found")
		return
	}
	if err != nil {
		slog.WarnContext(r.Context(), "archive run result unavailable", "error_type", fmt.Sprintf("%T", err))
		writeObservabilityError(w, r, http.StatusServiceUnavailable, "archive_run_unavailable", "archive run is unavailable")
		return
	}
	writeJSON(w, r, http.StatusOK, rdto.NewArchiveRun(run))
}

// Retained pages Trace interactions kept online under the current budget.
func (handler *ArchiveHandler) Retained(w http.ResponseWriter, r *http.Request) {
	if _, ok := handler.authorize(w, r, http.MethodGet); !ok {
		return
	}
	handler.writeRetainedPage(w, r, "")
}

func (handler *ArchiveHandler) writeRetainedPage(w http.ResponseWriter, r *http.Request, runID string) {
	offset, offsetErr := optionalNonNegativeInt(r.URL.Query().Get("offset"))
	rawLimit := r.URL.Query().Get("limit")
	limit, limitErr := optionalNonNegativeInt(rawLimit)
	// An omitted limit means the default page; an explicit 0 is invalid.
	if offsetErr != nil || limitErr != nil || rawLimit != "" && limit == 0 || limit > archivesvc.MaxRetainedPageSize {
		writeObservabilityError(w, r, http.StatusBadRequest, "invalid_retained_page", "offset must be 0 or more and limit between 1 and 100")
		return
	}
	page, err := handler.service.RetainedInteractions(r.Context(), runID, offset, limit)
	if errors.Is(err, archivesvc.ErrRunNotFound) {
		writeObservabilityError(w, r, http.StatusNotFound, "archive_run_not_found", "archive run was not found")
		return
	}
	if err != nil {
		slog.WarnContext(r.Context(), "retained archive interactions unavailable", "error_type", fmt.Sprintf("%T", err))
		writeObservabilityError(w, r, http.StatusServiceUnavailable, "archive_retained_unavailable", "retained interactions are unavailable")
		return
	}
	writeJSON(w, r, http.StatusOK, rdto.NewRetainedPage(page))
}

func optionalNonNegativeInt(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid non-negative integer")
	}
	return parsed, nil
}

func (handler *ArchiveHandler) download(w http.ResponseWriter, r *http.Request, jobID string) {
	download, err := handler.service.OpenDownload(r.Context(), jobID)
	if err != nil {
		writeObservabilityError(w, r, http.StatusBadRequest, "archive_download_unavailable", "archive download is unavailable")
		return
	}
	defer func() { _ = download.Content.Close() }()
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=archive-%s-%s.jsonl", download.Kind, jobID))
	if _, err := io.Copy(w, download.Content); err != nil {
		return
	}
}
func (handler *ArchiveHandler) authorize(w http.ResponseWriter, r *http.Request, method string) (profile evidencevo.AccessProfile, ok bool) {
	if r.Method != method {
		writeObservabilityError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported method")
		return profile, false
	}
	if handler.service == nil || handler.authorizer == nil {
		writeObservabilityError(w, r, http.StatusServiceUnavailable, "archive_unavailable", "archive service is not configured")
		return profile, false
	}
	if !handler.authorizer.authorizeQueryGateway(w, r) {
		return profile, false
	}
	scope, valid := handler.authorizer.queryScopeFromRequest(w, r, false)
	if !valid || scope.AccessProfile == nil {
		return profile, false
	}
	if !observabilityvo.CapabilitiesFor(*scope.AccessProfile).ObservabilityArchiveManage {
		writeObservabilityError(w, r, http.StatusForbidden, "archive_access_denied", "archive management is not allowed")
		return profile, false
	}
	return *scope.AccessProfile, true
}
