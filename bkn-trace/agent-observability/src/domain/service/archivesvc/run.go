// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

// A Run is one administrator request to archive a kind, executed in the
// background. Its batches stay ordinary jobs linked by BatchRunID, so the
// existing verification and cleanup safeguards apply unchanged.
type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunRetryWait RunStatus = "retry_wait"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
)

type RunTrigger string

const (
	RunTriggerArchive      RunTrigger = "archive"
	RunTriggerRetryCleanup RunTrigger = "retry_cleanup"
)

// Failure codes are business outcomes shown to administrators, never raw errors.
const (
	RunFailureStorageUnavailable    = "storage_unavailable"
	RunFailureRecordsNotReady       = "records_not_ready"
	RunFailureCleanupPending        = "cleanup_pending"
	RunFailureCleanupRequired       = "cleanup_required"
	RunFailureCleanupVerification   = "cleanup_verification_failed"
	RunFailureCapacityExceeded      = "capacity_exceeded"
	RunFailureConfigurationInvalid  = "configuration_invalid"
	RunFailureOversizedInteraction  = "oversized_interaction"
	MaxRunRetries                   = 3
	defaultRunWorkerInterval        = 5 * time.Second
	maxRetainedListed               = 500
	traceArchivePolicyVersionFormat = "trace-v1-%d-%d-%d"
)

var (
	ErrRunNotFound               = errors.New("archive run was not found")
	ErrCleanupVerificationFailed = errors.New("archive cleanup verification failed")
	errRunStopped                = errors.New("archive run stopped for shutdown")
	runRetryDelays               = [MaxRunRetries]time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
)

// TraceArchivePolicyVersion changes whenever a Trace budget changes, so
// interactions retained under an older budget are evaluated again.
func TraceArchivePolicyVersion() string {
	return fmt.Sprintf(traceArchivePolicyVersionFormat, MaxTraceArchiveCandidateBytes, MaxTraceArchiveBatchBytes, MaxTraceArchiveResponseBytes)
}

type Run struct {
	ID              string
	Kind            observabilityvo.ArchiveKind
	Trigger         RunTrigger
	SourceJobID     string
	BatchRunID      string
	Status          RunStatus
	RangeTo         time.Time
	EligibleAtStart int
	RetryCount      int
	NextAttemptAt   time.Time
	LastFailureCode string
	CreatedAt       time.Time
	StartedAt       time.Time
	UpdatedAt       time.Time
	FinishedAt      time.Time
}

func (run Run) Active() bool {
	return run.Status == RunQueued || run.Status == RunRunning || run.Status == RunRetryWait
}

// RetainedInteraction is a Trace interaction over the per-interaction budget.
// It stays in hot storage and is excluded from selection under its policy.
type RetainedInteraction struct {
	InteractionID string
	RunID         string
	PolicyVersion string
	Stage         string
	Measured      int64
	AtLeast       bool
	Budget        int64
	Unit          string
	RetainedAt    time.Time
}

type RunStore interface {
	CreateRun(context.Context, Run) error
	UpdateRun(context.Context, Run) error
	GetRun(context.Context, string) (Run, bool, error)
	// ActiveRun returns the oldest queued, running or waiting run of a kind.
	ActiveRun(context.Context, observabilityvo.ArchiveKind) (Run, bool, error)
	LatestRun(context.Context, observabilityvo.ArchiveKind) (Run, bool, error)
	// LatestBatch returns the newest job of a batch run, with frozen candidates.
	LatestBatch(context.Context, string) (Job, bool, error)
	RetainInteraction(context.Context, RetainedInteraction) error
	RetainedIDs(context.Context, string) ([]string, error)
	// ListRetained filters by run and/or policy; empty filters match all.
	ListRetained(ctx context.Context, runID, policyVersion string, limit int) ([]RetainedInteraction, error)
}

// RunView is a run with progress derived from its latest batch.
type RunView struct {
	Run
	ProcessedCount int
	RetainedCount  int
	BatchCount     int
}

type RunResult struct {
	RunView
	Retained []RetainedInteraction
}

type excludedCandidatesKey struct{}
type runRowKey struct{}
type stopSignalKey struct{}

// WithExcludedCandidates asks batch sources to leave these IDs out of both
// selection and counting, so the eligible count stays consistent.
func WithExcludedCandidates(ctx context.Context, ids []string) context.Context {
	return context.WithValue(ctx, excludedCandidatesKey{}, slices.Clone(ids))
}

func ExcludedCandidates(ctx context.Context) []string {
	ids, _ := ctx.Value(excludedCandidatesKey{}).([]string)
	return ids
}

func stopRequested(ctx context.Context) bool {
	stop, ok := ctx.Value(stopSignalKey{}).(<-chan struct{})
	if !ok {
		return false
	}
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

func (service *Service) runStore() (RunStore, bool) {
	runs, ok := service.store.(RunStore)
	return runs, ok
}

func (service *Service) withRetainedExcluded(ctx context.Context, kind observabilityvo.ArchiveKind) (context.Context, int, error) {
	runs, ok := service.runStore()
	if !ok || kind != observabilityvo.ArchiveKindTrace {
		return ctx, 0, nil
	}
	ids, err := runs.RetainedIDs(ctx, TraceArchivePolicyVersion())
	if err != nil {
		return ctx, 0, err
	}
	return WithExcludedCandidates(ctx, ids), len(ids), nil
}

func (service *Service) count(ctx context.Context, kind observabilityvo.ArchiveKind, window observabilityvo.ArchiveRange) (int, error) {
	if counter, ok := service.source.(CandidateCounter); ok {
		return counter.Count(ctx, kind, window)
	}
	candidates, err := service.source.Freeze(ctx, kind, window)
	return len(candidates), err
}

// Submit records an archive run and returns at once; the worker executes it.
func (service *Service) Submit(ctx context.Context, kind observabilityvo.ArchiveKind) (RunView, error) {
	if !kind.Valid() {
		return RunView{}, fmt.Errorf("invalid archive request")
	}
	return service.submit(ctx, kind, func(run *Run) error {
		if pending, err := service.hasIncompleteCleanup(ctx, kind); err != nil {
			return err
		} else if pending {
			return ErrCleanupRequired
		}
		window := observabilityvo.NewArchiveRange(kind, service.now().UTC(), service.location)
		countCtx, _, err := service.withRetainedExcluded(ctx, kind)
		if err != nil {
			return err
		}
		eligible, err := service.count(countCtx, kind, window)
		if err != nil {
			return err
		}
		if eligible == 0 {
			return ErrNothingToArchive
		}
		run.Trigger, run.RangeTo, run.EligibleAtStart, run.BatchRunID = RunTriggerArchive, window.To, eligible, run.ID
		return nil
	})
}

// SubmitRetryCleanup queues verified cleanup of one job, then the rest of its run.
func (service *Service) SubmitRetryCleanup(ctx context.Context, jobID string) (RunView, error) {
	original, ok := service.store.Get(jobID)
	if !ok {
		return RunView{}, ErrRunNotFound
	}
	return service.submit(ctx, original.Kind, func(run *Run) error {
		current, ok := service.store.Get(jobID)
		if !ok || current.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
			return fmt.Errorf("archive cleanup cannot be retried")
		}
		run.Trigger, run.SourceJobID, run.RangeTo, run.EligibleAtStart = RunTriggerRetryCleanup, jobID, current.Range.To, current.CandidateCount
		if p := current.BatchProgress; p != nil {
			run.BatchRunID = p.RunID
			run.EligibleAtStart = p.CleanedInRun + current.CandidateCount + p.RemainingAtSelection
		}
		return nil
	})
}

func (service *Service) submit(ctx context.Context, kind observabilityvo.ArchiveKind, prepare func(*Run) error) (RunView, error) {
	runs, ok := service.runStore()
	if !ok {
		return RunView{}, fmt.Errorf("background archive is not configured")
	}
	var run Run
	err := service.withKindLock(ctx, kind, func() error {
		if _, found, err := runs.ActiveRun(ctx, kind); err != nil {
			return err
		} else if found {
			return ErrArchiveBusy
		}
		now := service.now().UTC()
		run = Run{ID: fmt.Sprintf("arc_%s_%d_%s", kind, now.UnixNano(), rand.Text()), Kind: kind, Status: RunQueued, CreatedAt: now, UpdatedAt: now}
		if err := prepare(&run); err != nil {
			return err
		}
		return runs.CreateRun(ctx, run)
	})
	if err != nil {
		return RunView{}, err
	}
	service.wakeWorker(kind)
	return RunView{Run: run}, nil
}

func (service *Service) hasIncompleteCleanup(ctx context.Context, kind observabilityvo.ArchiveKind) (bool, error) {
	if locks, ok := service.store.(KindLockStore); ok {
		return locks.HasIncompleteCleanup(ctx, kind)
	}
	previous, ok := service.store.Latest(kind)
	return ok && previous.Status == observabilityvo.ArchiveStatusCleanupIncomplete, nil
}

func (service *Service) wakeWorker(kind observabilityvo.ArchiveKind) {
	if wake, ok := service.wake[kind]; ok {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// RunWorker executes due runs until ctx ends. Each kind has its own loop, so
// a long log run never delays Trace. Stopping lets the current batch finish;
// the run stays running and any instance resumes it after the lock is free.
func (service *Service) RunWorker(ctx context.Context, interval time.Duration) {
	if _, ok := service.runStore(); !ok {
		return
	}
	if interval <= 0 {
		interval = defaultRunWorkerInterval
	}
	done := make(chan struct{})
	kinds := []observabilityvo.ArchiveKind{observabilityvo.ArchiveKindLog, observabilityvo.ArchiveKindTrace}
	for _, kind := range kinds {
		go func() {
			defer func() { done <- struct{}{} }()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				service.processDue(ctx, kind)
				select {
				case <-ctx.Done():
					return
				case <-service.wake[kind]:
				case <-ticker.C:
				}
			}
		}()
	}
	for range kinds {
		<-done
	}
}

func (service *Service) processDue(ctx context.Context, kind observabilityvo.ArchiveKind) {
	if ctx.Err() != nil {
		return
	}
	runs, _ := service.runStore()
	run, found, err := runs.ActiveRun(ctx, kind)
	if err != nil || !found || run.Status == RunRetryWait && run.NextAttemptAt.After(service.now()) {
		return
	}
	// Work continues past a stop request until the current batch ends.
	work := context.WithValue(context.WithoutCancel(ctx), stopSignalKey{}, ctx.Done())
	err = service.withKindLock(work, kind, func() error { return service.execute(work, runs, run.ID) })
	if err != nil && !errors.Is(err, ErrArchiveBusy) {
		slog.WarnContext(ctx, "archive run could not start", "archive_kind", kind, "error_type", fmt.Sprintf("%T", err))
	}
}

func (service *Service) execute(ctx context.Context, runs RunStore, runID string) error {
	run, found, err := runs.GetRun(ctx, runID)
	if err != nil || !found || !run.Active() || run.Status == RunRetryWait && run.NextAttemptAt.After(service.now()) {
		return err
	}
	now := service.now().UTC()
	run.Status, run.UpdatedAt = RunRunning, now
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	if err := runs.UpdateRun(ctx, run); err != nil {
		return err
	}
	last, err := service.continueRun(context.WithValue(ctx, runRowKey{}, run.ID), runs, run)
	if errors.Is(err, errRunStopped) || err != nil && stopRequested(ctx) && errors.Is(err, context.Canceled) {
		return nil
	}
	code, retry := classifyRunOutcome(last, err)
	now = service.now().UTC()
	run.UpdatedAt = now
	switch {
	case code == "":
		run.Status, run.FinishedAt, run.NextAttemptAt = RunCompleted, now, time.Time{}
	case retry && run.RetryCount < MaxRunRetries:
		run.Status, run.LastFailureCode = RunRetryWait, code
		run.NextAttemptAt = now.Add(runRetryDelays[run.RetryCount])
		run.RetryCount++
	default:
		run.Status, run.LastFailureCode, run.FinishedAt, run.NextAttemptAt = RunFailed, code, now, time.Time{}
	}
	if err != nil {
		slog.WarnContext(ctx, "archive run attempt ended", "archive_kind", run.Kind, "run_status", run.Status, "failure_code", code, "error_type", fmt.Sprintf("%T", err))
	}
	return runs.UpdateRun(ctx, run)
}

// classifyRunOutcome maps a run attempt to a business failure code ("" means
// done) and whether waiting can fix it.
func classifyRunOutcome(last Job, err error) (string, bool) {
	var capacity *Failure
	var blocked *PayloadBudgetError
	switch {
	case err == nil && last.Status == observabilityvo.ArchiveStatusCleanupIncomplete:
		return RunFailureCleanupPending, true
	case err == nil, errors.Is(err, ErrNothingToArchive):
		return "", false
	case errors.Is(err, ErrInvalidLogLimits):
		return RunFailureConfigurationInvalid, false
	case errors.As(err, &capacity):
		return RunFailureCapacityExceeded, false
	case errors.As(err, &blocked):
		return RunFailureOversizedInteraction, false
	case errors.Is(err, ErrCleanupVerificationFailed):
		return RunFailureCleanupVerification, false
	case errors.Is(err, ErrCleanupRequired):
		return RunFailureCleanupRequired, false
	case errors.Is(err, ErrIncompleteCandidates):
		return RunFailureRecordsNotReady, true
	default:
		return RunFailureStorageUnavailable, true
	}
}

func (service *Service) continueRun(ctx context.Context, runs RunStore, run Run) (Job, error) {
	if run.Trigger == RunTriggerRetryCleanup && run.SourceJobID != "" {
		if job, ok := service.store.Get(run.SourceJobID); ok && job.Status == observabilityvo.ArchiveStatusCleanupIncomplete {
			if cleaned, err := service.reverifyAndClean(ctx, job); err != nil || cleaned.Status != observabilityvo.ArchiveStatusCompleted {
				return cleaned, err
			}
		}
	}
	if run.BatchRunID == "" {
		return Job{}, nil
	}
	last, found, err := runs.LatestBatch(ctx, run.BatchRunID)
	if err != nil {
		return Job{}, err
	}
	number, cleaned := 0, 0
	if found {
		if last.Status == observabilityvo.ArchiveStatusCleanupIncomplete {
			if last, err = service.reverifyAndClean(ctx, last); err != nil || last.Status != observabilityvo.ArchiveStatusCompleted {
				return last, err
			}
		}
		if p := last.BatchProgress; p != nil {
			if last.Status == observabilityvo.ArchiveStatusCompleted && p.RunComplete {
				return last, nil
			}
			number, cleaned = p.BatchNumber, p.CleanedInRun
		}
	}
	if pending, err := service.hasIncompleteCleanup(ctx, run.Kind); err != nil {
		return last, err
	} else if pending {
		return last, ErrCleanupRequired
	}
	ctx, _, err = service.withRetainedExcluded(ctx, run.Kind)
	if err != nil {
		return last, err
	}
	next, err := service.createBatches(ctx, run.Kind, observabilityvo.ArchiveRange{To: run.RangeTo}, run.BatchRunID, number, cleaned)
	if errors.Is(err, ErrNothingToArchive) && found && last.BatchProgress != nil && !last.BatchProgress.RunComplete && last.Status == observabilityvo.ArchiveStatusCompleted {
		last.BatchProgress.RunComplete = true
		return last, service.store.Update(last)
	}
	if next.ID != "" {
		last = next
	}
	return last, err
}

// retainOversized records a Trace interaction over its budget so the run can
// continue past it. It reports false when the caller must keep the old
// blocking behavior (no run store, no identity, or already excluded).
func (service *Service) retainOversized(ctx context.Context, blocked *PayloadBudgetError) (bool, error) {
	runs, ok := service.runStore()
	runID, _ := ctx.Value(runRowKey{}).(string)
	if !ok || runID == "" || blocked.InteractionID == "" || slices.Contains(ExcludedCandidates(ctx), blocked.InteractionID) {
		return false, nil
	}
	err := runs.RetainInteraction(ctx, RetainedInteraction{
		InteractionID: blocked.InteractionID, RunID: runID, PolicyVersion: TraceArchivePolicyVersion(), Stage: blocked.Stage,
		Measured: blocked.Actual, AtLeast: blocked.AtLeast, Budget: blocked.Limit, Unit: blocked.Unit, RetainedAt: service.now().UTC(),
	})
	return err == nil, err
}

func (service *Service) reverifyAndClean(ctx context.Context, current Job) (Job, error) {
	reader, _ := service.objectStore.(ReadStore)
	if err := verifyCleanupArchive(ctx, reader, current); err != nil {
		// Read errors can contain signed object URLs; expose only a safe hint.
		current.ErrorMessage = cleanupVerificationHint(err)
		current.UpdatedAt = service.now().UTC()
		return current, errors.Join(fmt.Errorf("%w: %w", ErrCleanupVerificationFailed, err), service.store.Update(current))
	}
	return service.cleanBatch(ctx, current)
}

func (service *Service) view(ctx context.Context, runs RunStore, run Run) (RunView, error) {
	view := RunView{Run: run}
	if run.BatchRunID != "" {
		last, found, err := runs.LatestBatch(ctx, run.BatchRunID)
		if err != nil {
			return view, err
		}
		if found && last.BatchProgress != nil {
			view.ProcessedCount, view.BatchCount = last.BatchProgress.CleanedInRun, last.BatchProgress.BatchNumber
		}
	}
	if run.Kind == observabilityvo.ArchiveKindTrace {
		retained, err := runs.ListRetained(ctx, run.ID, "", maxRetainedListed)
		if err != nil {
			return view, err
		}
		view.RetainedCount = len(retained)
	}
	return view, nil
}

// RunResult returns one run with its retained interactions.
func (service *Service) RunResult(ctx context.Context, runID string) (RunResult, error) {
	runs, ok := service.runStore()
	if !ok {
		return RunResult{}, ErrRunNotFound
	}
	run, found, err := runs.GetRun(ctx, runID)
	if err != nil {
		return RunResult{}, err
	}
	if !found {
		return RunResult{}, ErrRunNotFound
	}
	view, err := service.view(ctx, runs, run)
	if err != nil {
		return RunResult{}, err
	}
	result := RunResult{RunView: view}
	if run.Kind == observabilityvo.ArchiveKindTrace {
		result.Retained, err = runs.ListRetained(ctx, run.ID, "", maxRetainedListed)
	}
	return result, err
}

// RetainedInteractions lists every interaction excluded under the current policy.
func (service *Service) RetainedInteractions(ctx context.Context) ([]RetainedInteraction, error) {
	runs, ok := service.runStore()
	if !ok {
		return []RetainedInteraction{}, nil
	}
	return runs.ListRetained(ctx, "", TraceArchivePolicyVersion(), maxRetainedListed)
}
