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
		profile, ok := handler.authorize(w, r, http.MethodGet)
		if !ok {
			return
		}
		overview, err := handler.service.Overview(r.Context(), kind, profile.TenantID)
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
		profile, ok := handler.authorize(w, r, http.MethodPost)
		if !ok {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || len(body) != 0 {
			writeObservabilityError(w, r, http.StatusBadRequest, "invalid_archive_request", "archive request body must be an empty object")
			return
		}
		job, err := handler.service.Create(r.Context(), kind, profile.TenantID)
		if err != nil {
			switch {
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
		profile, ok := handler.authorize(w, r, http.MethodGet)
		if !ok {
			return
		}
		jobs := handler.service.List(profile.TenantID, kind, 20)
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
	profile, ok := handler.authorize(w, r, method)
	if !ok {
		return
	}
	if method == http.MethodGet {
		if len(parts) == 2 && parts[1] == "download" {
			handler.download(w, r, parts[0], profile.TenantID)
			return
		}
		job, found := handler.service.Get(parts[0], profile.TenantID)
		if !found {
			writeObservabilityError(w, r, http.StatusNotFound, "archive_job_not_found", "archive job was not found")
			return
		}
		writeJSON(w, r, http.StatusOK, rdto.NewArchiveJob(job))
		return
	}
	job, err := handler.service.RetryCleanup(r.Context(), parts[0], profile.TenantID)
	if err != nil {
		writeObservabilityError(w, r, http.StatusBadRequest, "archive_retry_failed", "archive cleanup cannot be retried")
		return
	}
	writeJSON(w, r, http.StatusOK, rdto.NewArchiveJob(job))
}

func (handler *ArchiveHandler) download(w http.ResponseWriter, r *http.Request, jobID, tenantID string) {
	download, err := handler.service.OpenDownload(r.Context(), jobID, tenantID)
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
