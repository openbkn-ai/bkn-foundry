// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package rdto

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"

type ArchiveOverviewResponse struct {
	ArchiveKind    string `json:"archive_kind"`
	RetentionDays  int    `json:"retention_days"`
	CutoffAt       string `json:"cutoff_at"`
	CandidateCount int    `json:"candidate_count"`
	Storage        struct {
		Status string `json:"status"`
	} `json:"storage"`
}

type ArchiveJobResponse struct {
	ArchiveJobID string `json:"archive_job_id"`
	ArchiveKind  string `json:"archive_kind"`
	Status       string `json:"status"`
	Range        struct {
		From *string `json:"from"`
		To   string  `json:"to"`
	} `json:"range"`
	CandidateCount int    `json:"candidate_count"`
	ManifestRef    string `json:"manifest_ref,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
}

func NewArchiveJob(job archivesvc.Job) ArchiveJobResponse {
	response := ArchiveJobResponse{ArchiveJobID: job.ID, ArchiveKind: string(job.Kind), Status: string(job.Status), CandidateCount: job.CandidateCount, ManifestRef: job.ManifestRef, ErrorMessage: job.ErrorMessage}
	response.Range.To = job.Range.To.Format("2006-01-02T15:04:05Z07:00")
	if !job.Range.From.IsZero() {
		value := job.Range.From.Format("2006-01-02T15:04:05Z07:00")
		response.Range.From = &value
	}
	return response
}
