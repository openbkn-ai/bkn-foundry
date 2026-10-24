// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package archivesvc coordinates the irreversible final step of a manual
// archive. Its ports deliberately work on a frozen candidate list: cleanup
// must never recalculate a moving time range.
package archivesvc

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

var (
	ErrArchiveBusy          = errors.New("archive operation is already running")
	ErrIncompleteCandidates = errors.New("archive candidates are incomplete or unverifiable")
	ErrNothingToArchive     = errors.New("archive has no eligible records")
	ErrCleanupRequired      = errors.New("archive cleanup must be completed before another archive of this kind")
)

type Candidate struct {
	ID         string    `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Payload    []byte    `json:"payload"`
}

// BatchProgress records a completed or interrupted batch in one synchronous run.
// RemainingAtSelection excludes this batch and is not a live collection count.
// Cleaned counts include only batches whose frozen IDs were confirmed absent.
type BatchProgress struct {
	RunID                string `json:"run_id"`
	BatchNumber          int    `json:"batch_number"`
	EligibleAtSelection  int    `json:"eligible_at_selection"`
	VerifiedCount        int    `json:"verified_count"`
	CleanedCount         int    `json:"cleaned_count"`
	CleanedInRun         int    `json:"cleaned_in_run"`
	RemainingAtSelection int    `json:"remaining_at_selection"`
	RunComplete          bool   `json:"run_complete"`
}

type Batch struct {
	Candidates    []Candidate
	EligibleCount int
	Batched       bool
}

type BatchSource interface {
	FreezeBatch(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) (Batch, error)
}

type KindLockStore interface {
	WithKindLock(context.Context, observabilityvo.ArchiveKind, func() error) error
	HasIncompleteCleanup(context.Context, observabilityvo.ArchiveKind) (bool, error)
}

type Job struct {
	BatchProgress  *BatchProgress                `json:"batch_progress,omitempty"`
	ID             string                        `json:"archive_job_id"`
	Kind           observabilityvo.ArchiveKind   `json:"archive_kind"`
	Range          observabilityvo.ArchiveRange  `json:"range"`
	Status         observabilityvo.ArchiveStatus `json:"status"`
	CandidateIDs   []string                      `json:"-"`
	Candidates     []Candidate                   `json:"-"`
	CandidateCount int                           `json:"candidate_count"`
	ManifestRef    string                        `json:"manifest_ref,omitempty"`
	ErrorMessage   string                        `json:"error_message,omitempty"`
	CreatedAt      time.Time                     `json:"created_at"`
	UpdatedAt      time.Time                     `json:"updated_at"`
}

type Source interface {
	Freeze(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) ([]Candidate, error)
	Purge(context.Context, observabilityvo.ArchiveKind, []Candidate) error
}

// CandidateCounter lets read-only overviews avoid materializing archive bundles.
// Create freezes each supported batch before writing or purging.
type CandidateCounter interface {
	Count(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) (int, error)
}

type ObjectStore interface {
	WriteAndVerify(context.Context, Job, []Candidate) (string, error)
}

type ReadStore interface {
	Read(context.Context, string) (io.ReadCloser, error)
}

type Download struct {
	Content io.ReadCloser
	Kind    observabilityvo.ArchiveKind
}

type Store interface {
	Create(Job) error
	Update(Job) error
	Latest(observabilityvo.ArchiveKind) (Job, bool)
	Get(string) (Job, bool)
	List(observabilityvo.ArchiveKind, int) []Job
}

type Options struct {
	Now      func() time.Time
	Location *time.Location
}

type Service struct {
	mu          sync.Mutex
	store       Store
	source      Source
	objectStore ObjectStore
	now         func() time.Time
	location    *time.Location
}

func New(store Store, source Source, objectStore ObjectStore, options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Location == nil {
		options.Location = time.Local
	}
	return &Service{store: store, source: source, objectStore: objectStore, now: options.Now, location: options.Location}
}

func (service *Service) Create(ctx context.Context, kind observabilityvo.ArchiveKind) (job Job, err error) {
	if !kind.Valid() {
		return Job{}, fmt.Errorf("invalid archive request")
	}
	err = service.withKindLock(ctx, kind, func() error {
		if locks, ok := service.store.(KindLockStore); ok {
			pending, e := locks.HasIncompleteCleanup(ctx, kind)
			if e != nil {
				return e
			}
			if pending {
				return ErrCleanupRequired
			}
		} else if previous, ok := service.store.Latest(kind); ok && previous.Status == observabilityvo.ArchiveStatusCleanupIncomplete {
			return ErrCleanupRequired
		}
		now := service.now().UTC()
		var e error
		job, e = service.createBatches(ctx, kind, observabilityvo.NewArchiveRange(kind, now, service.location), "", 0, 0)
		return e
	})
	return job, err
}

func (service *Service) withKindLock(ctx context.Context, kind observabilityvo.ArchiveKind, fn func() error) error {
	if locks, ok := service.store.(KindLockStore); ok {
		return locks.WithKindLock(ctx, kind, fn)
	}
	// Compatibility for custom stores; production stores lock across services.
	if !service.mu.TryLock() {
		return ErrArchiveBusy
	}
	defer service.mu.Unlock()
	return fn()
}

func (service *Service) createBatches(ctx context.Context, kind observabilityvo.ArchiveKind, window observabilityvo.ArchiveRange, runID string, number, cleaned int) (last Job, err error) {
	for {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		var batch Batch
		if source, ok := service.source.(BatchSource); ok {
			batch, err = source.FreezeBatch(ctx, kind, window)
		} else {
			batch.Candidates, err = service.source.Freeze(ctx, kind, window)
			batch.EligibleCount = len(batch.Candidates)
		}
		if err != nil {
			return last, fmt.Errorf("freeze archive candidates: %w", err)
		}
		if batch.EligibleCount < len(batch.Candidates) || batch.EligibleCount > 0 && len(batch.Candidates) == 0 {
			return last, ErrIncompleteCandidates
		}
		if len(batch.Candidates) == 0 {
			if last.ID == "" {
				return last, ErrNothingToArchive
			}
			last.BatchProgress.RunComplete = true
			return last, service.store.Update(last)
		}
		now := service.now().UTC()
		job := newJob(kind, window, batch.Candidates, now)
		if batch.Batched {
			if runID == "" {
				runID = fmt.Sprintf("arc_%s_%d_%s", kind, now.UnixNano(), rand.Text())
			}
			number++
			job.ID = fmt.Sprintf("%s_%06d", runID, number)
			job.BatchProgress = &BatchProgress{RunID: runID, BatchNumber: number, EligibleAtSelection: batch.EligibleCount, CleanedInRun: cleaned, RemainingAtSelection: batch.EligibleCount - len(batch.Candidates)}
		}
		last, err = service.writeAndCleanBatch(ctx, job, batch.Candidates)
		if err != nil || last.Status != observabilityvo.ArchiveStatusCompleted {
			return last, err
		}
		if !batch.Batched || last.BatchProgress.RunComplete {
			return last, nil
		}
		cleaned = last.BatchProgress.CleanedInRun
	}
}

func (service *Service) writeAndCleanBatch(ctx context.Context, job Job, candidates []Candidate) (Job, error) {
	if err := service.store.Create(job); err != nil {
		return Job{}, err
	}
	manifestRef, err := service.objectStore.WriteAndVerify(ctx, job, candidates)
	if err == nil && strings.TrimSpace(manifestRef) == "" {
		err = errors.New("archive verification returned no manifest")
	}
	if err != nil {
		job.Status, job.ErrorMessage, job.UpdatedAt = observabilityvo.ArchiveStatusFailed, err.Error(), service.now().UTC()
		_ = service.store.Update(job)
		return job, err
	}
	// The verified manifest and frozen IDs must be durable before cleanup.
	job.ManifestRef, job.Status, job.ErrorMessage, job.UpdatedAt = manifestRef, observabilityvo.ArchiveStatusCleanupIncomplete, "", service.now().UTC()
	if job.BatchProgress != nil {
		job.BatchProgress.VerifiedCount = job.CandidateCount
	}
	if err := service.store.Update(job); err != nil {
		return Job{}, err
	}
	return service.cleanBatch(ctx, job)
}

func (service *Service) cleanBatch(ctx context.Context, job Job) (Job, error) {
	if err := service.source.Purge(ctx, job.Kind, job.Candidates); err != nil {
		job.Status, job.ErrorMessage, job.UpdatedAt = observabilityvo.ArchiveStatusCleanupIncomplete, err.Error(), service.now().UTC()
		if saveErr := service.store.Update(job); saveErr != nil {
			return job, errors.Join(err, saveErr)
		}
		return job, nil
	}
	job.Status, job.ErrorMessage, job.UpdatedAt = observabilityvo.ArchiveStatusCompleted, "", service.now().UTC()
	if job.BatchProgress != nil {
		job.BatchProgress.CleanedCount = job.CandidateCount
		job.BatchProgress.CleanedInRun += job.CandidateCount
		job.BatchProgress.RunComplete = job.BatchProgress.RemainingAtSelection == 0
	}
	if err := service.store.Update(job); err != nil {
		return Job{}, err
	}
	return job, nil
}

type Overview struct {
	Kind           observabilityvo.ArchiveKind
	RetentionDays  int
	Range          observabilityvo.ArchiveRange
	CandidateCount int
	StorageReady   bool
}

func (service *Service) Overview(ctx context.Context, kind observabilityvo.ArchiveKind) (Overview, error) {
	if !kind.Valid() {
		return Overview{}, fmt.Errorf("invalid archive overview")
	}
	archiveRange := observabilityvo.NewArchiveRange(kind, service.now().UTC(), service.location)
	var count int
	var err error
	if counter, ok := service.source.(CandidateCounter); ok {
		count, err = counter.Count(ctx, kind, archiveRange)
	} else {
		var candidates []Candidate
		candidates, err = service.source.Freeze(ctx, kind, archiveRange)
		count = len(candidates)
	}
	if err != nil {
		return Overview{}, err
	}
	ready := true
	if store, ok := service.objectStore.(interface {
		Availability(context.Context) (bool, error)
	}); ok {
		ready, _ = store.Availability(ctx)
	} else if store, ok := service.objectStore.(interface{ Ready() bool }); ok {
		ready = store.Ready()
	}
	return Overview{Kind: kind, RetentionDays: kind.RetentionDays(), Range: archiveRange, CandidateCount: count, StorageReady: ready}, nil
}

func (service *Service) RetryCleanup(ctx context.Context, jobID string) (job Job, err error) {
	original, ok := service.store.Get(jobID)
	if !ok {
		return Job{}, fmt.Errorf("archive job not found")
	}
	err = service.withKindLock(ctx, original.Kind, func() error {
		current, ok := service.store.Get(jobID)
		if !ok || current.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
			return fmt.Errorf("archive cleanup cannot be retried")
		}
		if len(current.Candidates) != len(current.CandidateIDs) {
			return fmt.Errorf("archive cleanup candidates are unavailable")
		}
		var e error
		job, e = service.cleanBatch(ctx, current)
		if e != nil || job.Status != observabilityvo.ArchiveStatusCompleted || job.BatchProgress == nil || job.BatchProgress.RunComplete {
			return e
		}
		if locks, ok := service.store.(KindLockStore); ok {
			pending, e := locks.HasIncompleteCleanup(ctx, job.Kind)
			if e != nil {
				return e
			}
			if pending {
				return nil
			}
		}
		next, e := service.createBatches(ctx, job.Kind, job.Range, job.BatchProgress.RunID, job.BatchProgress.BatchNumber, job.BatchProgress.CleanedInRun)
		if errors.Is(e, ErrNothingToArchive) {
			job.BatchProgress.RunComplete = true
			return service.store.Update(job)
		}
		if next.ID != "" {
			job = next
		}
		return e
	})
	return job, err
}

func (service *Service) Get(jobID string) (Job, bool) {
	return service.store.Get(jobID)
}

// OpenDownload returns the verified archive data through the application
// boundary.  The browser must never receive an internal object-store URL.
func (service *Service) OpenDownload(ctx context.Context, jobID string) (Download, error) {
	job, ok := service.Get(jobID)
	if !ok || job.ManifestRef == "" {
		return Download{}, fmt.Errorf("archive download is unavailable")
	}
	store, ok := service.objectStore.(ReadStore)
	if !ok {
		return Download{}, fmt.Errorf("archive download is unavailable")
	}
	manifest, err := store.Read(ctx, job.ManifestRef)
	if err != nil {
		return Download{}, fmt.Errorf("read archive manifest: %w", err)
	}
	defer func() { _ = manifest.Close() }()
	var record struct {
		DataKey string `json:"data_key"`
	}
	if err := json.NewDecoder(manifest).Decode(&record); err != nil || record.DataKey == "" {
		return Download{}, fmt.Errorf("archive manifest is invalid")
	}
	content, err := store.Read(ctx, record.DataKey)
	if err != nil {
		return Download{}, fmt.Errorf("read archive data: %w", err)
	}
	return Download{Content: content, Kind: job.Kind}, nil
}

// List returns the newest jobs for one archive kind.  The archive kinds stay
// isolated so a failed cleanup of one never obscures the other.
func (service *Service) List(kind observabilityvo.ArchiveKind, limit int) []Job {
	if !kind.Valid() || limit <= 0 {
		return nil
	}
	return service.store.List(kind, limit)
}

func newJob(kind observabilityvo.ArchiveKind, archiveRange observabilityvo.ArchiveRange, candidates []Candidate, now time.Time) Job {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	sort.Strings(ids)
	return Job{ID: fmt.Sprintf("arc_%s_%d", kind, now.UnixNano()), Kind: kind, Range: archiveRange, Status: observabilityvo.ArchiveStatusFailed, CandidateIDs: ids, Candidates: append([]Candidate(nil), candidates...), CandidateCount: len(candidates), CreatedAt: now, UpdatedAt: now}
}

type memoryStore struct {
	active map[observabilityvo.ArchiveKind]bool
	mu     sync.Mutex
	jobs   map[string]Job
}

func NewMemoryStore() Store {
	return &memoryStore{jobs: map[string]Job{}, active: map[observabilityvo.ArchiveKind]bool{}}
}
func (store *memoryStore) Create(job Job) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.jobs[job.ID]; ok {
		return errors.New("archive job already exists")
	}
	store.jobs[job.ID] = storedJob(job)
	return nil
}
func (store *memoryStore) Update(job Job) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.jobs[job.ID]; !ok {
		return errors.New("archive job does not exist")
	}
	store.jobs[job.ID] = storedJob(job)
	return nil
}

func storedJob(job Job) Job {
	if job.BatchProgress != nil {
		copy := *job.BatchProgress
		job.BatchProgress = &copy
	}
	if job.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
		job.Candidates = nil
	}
	return job
}
func (store *memoryStore) Latest(kind observabilityvo.ArchiveKind) (Job, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var result Job
	found := false
	for _, job := range store.jobs {
		if job.Kind == kind && (!found || (job.CreatedAt.After(result.CreatedAt) || job.CreatedAt.Equal(result.CreatedAt) && job.ID > result.ID)) {
			result, found = job, true
		}
	}
	return storedJob(result), found
}
func (store *memoryStore) Get(jobID string) (Job, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	job, ok := store.jobs[jobID]
	return storedJob(job), ok
}
func (store *memoryStore) List(kind observabilityvo.ArchiveKind, limit int) []Job {
	store.mu.Lock()
	defer store.mu.Unlock()
	jobs := make([]Job, 0, limit)
	for _, job := range store.jobs {
		if job.Kind == kind {
			jobs = append(jobs, storedJob(job))
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt) || jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) && jobs[i].ID > jobs[j].ID
	})
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs
}

func (store *memoryStore) WithKindLock(ctx context.Context, kind observabilityvo.ArchiveKind, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	if store.active[kind] {
		store.mu.Unlock()
		return ErrArchiveBusy
	}
	store.active[kind] = true
	store.mu.Unlock()
	defer func() { store.mu.Lock(); delete(store.active, kind); store.mu.Unlock() }()
	return fn()
}

func (store *memoryStore) HasIncompleteCleanup(ctx context.Context, kind observabilityvo.ArchiveKind) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, job := range store.jobs {
		if job.Kind == kind && job.Status == observabilityvo.ArchiveStatusCleanupIncomplete {
			return true, nil
		}
	}
	return false, nil
}
