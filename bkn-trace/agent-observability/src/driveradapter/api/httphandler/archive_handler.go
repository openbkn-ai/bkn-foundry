// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
		job, err := handler.service.Create(r.Context(), kind)
		if err != nil && writeLogArchiveFailure(w, r, job, err) {
			return
		}
		if err != nil {
			switch {
			case errors.Is(err, archivesvc.ErrArchiveBusy):
				writeObservabilityError(w, r, http.StatusConflict, "archive_busy", "archive operation is already running")
			case errors.Is(err, archivesvc.ErrArchivePayloadTooLarge):
				if writeArchiveBlockage(w, r, job, err) {
					return
				}
				writeObservabilityError(w, r, http.StatusConflict, "archive_incomplete_candidates", "trace archive exceeds its payload budget (100 Interactions, 8 MiB each, 10 MiB per batch); this batch was not started; earlier verified batches remain recorded")
			case errors.Is(err, archivesvc.ErrIncompleteCandidates):
				writeObservabilityError(w, r, http.StatusConflict, "archive_incomplete_candidates", "archive candidate batch is incomplete or unverifiable; this batch was not started; earlier batches remain recorded")
			case errors.Is(err, archivesvc.ErrNothingToArchive):
				writeObservabilityError(w, r, http.StatusConflict, "archive_nothing_to_do", "no eligible records are available")
			case errors.Is(err, archivesvc.ErrCleanupRequired):
				writeObservabilityError(w, r, http.StatusConflict, "archive_cleanup_required", "previous cleanup must be completed first")
			default:
				writeObservabilityError(w, r, http.StatusInternalServerError, "archive_failed", "archive did not complete")
			}
			return
		}
		writeJSON(w, r, http.StatusCreated, rdto.NewArchiveJob(job))
	}
}

// ListOrCreate deliberately exposes a short bounded history on the same
// resource.  Archive creation remains POST-only and never accepts a caller
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
		jobs := handler.service.List(kind, 20)
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
	job, err := handler.service.RetryCleanup(r.Context(), parts[0])
	if err != nil {
		if writeLogArchiveFailure(w, r, job, err) {
			return
		}
		if writeArchiveBlockage(w, r, job, err) {
			return
		}
		writeObservabilityError(w, r, http.StatusBadRequest, "archive_retry_failed", "archive cleanup cannot be retried")
		return
	}
	writeJSON(w, r, http.StatusOK, rdto.NewArchiveJob(job))
}

func writeLogArchiveFailure(w http.ResponseWriter, r *http.Request, job archivesvc.Job, err error) bool {
	if errors.Is(err, archivesvc.ErrInvalidLogLimits) {
		writeJSON(w, r, http.StatusServiceUnavailable, observabilityErrorEnvelope{Error: observabilityError{Code: "archive_configuration_invalid", Message: "log archive limits are invalid; ask the deployment owner to fix the configuration", RequiredAction: "fix_log_archive_configuration", RequestID: requestIDFromRequest(r), Details: struct {
			JobID   string              `json:"archive_job_id,omitempty"`
			Failure *archivesvc.Failure `json:"failure"`
		}{job.ID, &archivesvc.Failure{Stage: "configuration"}}}})
		return true
	}
	for _, known := range []error{archivesvc.ErrArchiveBusy, archivesvc.ErrArchivePayloadTooLarge, archivesvc.ErrIncompleteCandidates, archivesvc.ErrNothingToArchive, archivesvc.ErrCleanupRequired} {
		if errors.Is(err, known) {
			return false
		}
	}
	var capacity *archivesvc.Failure
	status := http.StatusInternalServerError
	code := "archive_failed"
	retryable := true
	failure := capacity
	var operation *archivesvc.OperationError
	if !errors.Is(err, archivesvc.ErrIncompleteCandidates) && errors.As(err, &operation) {
		failure = &archivesvc.Failure{Stage: operation.Stage}
	}
	if errors.As(err, &capacity) {
		status = http.StatusConflict
		code = "archive_capacity_exceeded"
		retryable = false
		failure = capacity
	} else if job.Kind == observabilityvo.ArchiveKindLog && job.BatchProgress != nil && job.BatchProgress.Failure != nil {
		failure = job.BatchProgress.Failure
	}
	if failure == nil {
		return false
	}
	writeJSON(w, r, status, observabilityErrorEnvelope{Error: observabilityError{Code: code, Message: "log archive stopped; inspect the recorded stage and job before retrying", Retryable: retryable, RequiredAction: "inspect_log_archive", RequestID: requestIDFromRequest(r), Details: struct {
		JobID   string              `json:"archive_job_id,omitempty"`
		Failure *archivesvc.Failure `json:"failure"`
	}{job.ID, failure}}})
	return true
}

// Only advertise a persistent blocker after its failed job was saved. A failed
// checkpoint write must retain the ordinary server error, without a phantom ID.
func writeArchiveBlockage(w http.ResponseWriter, r *http.Request, job archivesvc.Job, err error) bool {
	var blocked *archivesvc.PayloadBudgetError
	if !errors.As(err, &blocked) || job.ID == "" || job.BatchProgress == nil || job.BatchProgress.Blocked == nil {
		return false
	}
	writeJSON(w, r, http.StatusConflict, observabilityErrorEnvelope{Error: observabilityError{
		Code: "archive_incomplete_candidates", Message: "trace archive is blocked by an oversized interaction; source retained; earlier verified batches remain recorded", RequestID: requestIDFromRequest(r), RequiredAction: "review_oversized_interaction",
		Details: struct {
			JobID   string                         `json:"archive_job_id"`
			Blocked *archivesvc.PayloadBudgetError `json:"blocked"`
		}{job.ID, job.BatchProgress.Blocked},
	}})
	return true
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
