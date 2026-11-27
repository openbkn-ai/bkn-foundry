// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package rdto

import (
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
)

type ArchiveOverviewResponse struct {
	ArchiveKind    string `json:"archive_kind"`
	RetentionDays  int    `json:"retention_days"`
	CutoffAt       string `json:"cutoff_at"`
	CandidateCount int    `json:"candidate_count"`
	Storage        struct {
		Status string `json:"status"`
	} `json:"storage"`
	// CurrentRun is the latest background run, kept after it ends until the next starts.
	CurrentRun    *ArchiveRunResponse `json:"current_run,omitempty"`
	RetainedCount int                 `json:"retained_count"`
}

type ArchiveRunResponse struct {
	RunID           string  `json:"run_id"`
	ArchiveKind     string  `json:"archive_kind"`
	Trigger         string  `json:"trigger"`
	Status          string  `json:"status"`
	EligibleAtStart int     `json:"eligible_at_start"`
	ProcessedCount  int     `json:"processed_count"`
	RetainedCount   int     `json:"retained_count"`
	BatchCount      int     `json:"batch_count"`
	RetryCount      int     `json:"retry_count"`
	NextAttemptAt   *string `json:"next_attempt_at,omitempty"`
	LastFailureCode string  `json:"last_failure_code,omitempty"`
	CreatedAt       string  `json:"created_at"`
	StartedAt       *string `json:"started_at,omitempty"`
	FinishedAt      *string `json:"finished_at,omitempty"`
}

type RetainedInteractionResponse struct {
	InteractionID string `json:"interaction_id"`
	RunID         string `json:"run_id"`
	Stage         string `json:"stage"`
	Measured      int64  `json:"measured"`
	AtLeast       bool   `json:"at_least,omitempty"`
	Budget        int64  `json:"budget"`
	Unit          string `json:"unit"`
	RetainedAt    string `json:"retained_at"`
}

type ArchiveRunResultResponse struct {
	ArchiveRunResponse
	Retained []RetainedInteractionResponse `json:"retained"`
}

func optionalTime(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339)
	return &formatted
}

func NewArchiveRun(view archivesvc.RunView) ArchiveRunResponse {
	return ArchiveRunResponse{
		RunID: view.ID, ArchiveKind: string(view.Kind), Trigger: string(view.Trigger), Status: string(view.Status),
		EligibleAtStart: view.EligibleAtStart, ProcessedCount: view.ProcessedCount, RetainedCount: view.RetainedCount,
		BatchCount: view.BatchCount, RetryCount: view.RetryCount, NextAttemptAt: optionalTime(view.NextAttemptAt),
		LastFailureCode: view.LastFailureCode, CreatedAt: view.CreatedAt.UTC().Format(time.RFC3339),
		StartedAt: optionalTime(view.StartedAt), FinishedAt: optionalTime(view.FinishedAt),
	}
}

func NewRetainedInteractions(items []archivesvc.RetainedInteraction) []RetainedInteractionResponse {
	response := make([]RetainedInteractionResponse, 0, len(items))
	for _, item := range items {
		response = append(response, RetainedInteractionResponse{
			InteractionID: item.InteractionID, RunID: item.RunID, Stage: item.Stage, Measured: item.Measured,
			AtLeast: item.AtLeast, Budget: item.Budget, Unit: item.Unit, RetainedAt: item.RetainedAt.UTC().Format(time.RFC3339),
		})
	}
	return response
}

func NewArchiveRunResult(result archivesvc.RunResult) ArchiveRunResultResponse {
	return ArchiveRunResultResponse{ArchiveRunResponse: NewArchiveRun(result.RunView), Retained: NewRetainedInteractions(result.Retained)}
}

type ArchiveJobResponse struct {
	BatchProgress *archivesvc.BatchProgress `json:"batch_progress,omitempty"`
	ArchiveJobID  string                    `json:"archive_job_id"`
	ArchiveKind   string                    `json:"archive_kind"`
	Status        string                    `json:"status"`
	Range         struct {
		From *string `json:"from"`
		To   string  `json:"to"`
	} `json:"range"`
	CandidateCount int    `json:"candidate_count"`
	ManifestRef    string `json:"manifest_ref,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
}

func NewArchiveJob(job archivesvc.Job) ArchiveJobResponse {
	response := ArchiveJobResponse{BatchProgress: job.BatchProgress, ArchiveJobID: job.ID, ArchiveKind: string(job.Kind), Status: string(job.Status), CandidateCount: job.CandidateCount, ManifestRef: job.ManifestRef, ErrorMessage: job.ErrorMessage}
	response.Range.To = job.Range.To.Format("2006-01-02T15:04:05Z07:00")
	if !job.Range.From.IsZero() {
		value := job.Range.From.Format("2006-01-02T15:04:05Z07:00")
		response.Range.From = &value
	}
	return response
}
