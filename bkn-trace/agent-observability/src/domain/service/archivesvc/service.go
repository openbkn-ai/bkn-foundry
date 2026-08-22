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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

var (
	ErrNothingToArchive = errors.New("archive has no eligible records")
	ErrCleanupRequired  = errors.New("archive cleanup must be completed before another archive of this kind")
)

type Candidate struct {
	ID         string    `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Payload    []byte    `json:"payload"`
}

type Job struct {
	ID             string                        `json:"archive_job_id"`
	TenantID       string                        `json:"tenant_id"`
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
	Freeze(context.Context, observabilityvo.ArchiveKind, string, observabilityvo.ArchiveRange) ([]Candidate, error)
	Purge(context.Context, observabilityvo.ArchiveKind, string, []Candidate) error
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
	Latest(string, observabilityvo.ArchiveKind) (Job, bool)
	Get(string) (Job, bool)
	List(string, observabilityvo.ArchiveKind, int) []Job
}

type Options struct {
	Now      func() time.Time
	Location *time.Location
}

type Service struct {
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

func (service *Service) Create(ctx context.Context, kind observabilityvo.ArchiveKind, tenantID string) (Job, error) {
	if !kind.Valid() || tenantID == "" {
		return Job{}, fmt.Errorf("invalid archive request")
	}
	if previous, ok := service.store.Latest(tenantID, kind); ok && previous.Status == observabilityvo.ArchiveStatusCleanupIncomplete {
		return Job{}, ErrCleanupRequired
	}
	now := service.now().UTC()
	rangeToArchive := observabilityvo.NewArchiveRange(kind, now, service.location)
	candidates, err := service.source.Freeze(ctx, kind, tenantID, rangeToArchive)
	if err != nil {
		return Job{}, fmt.Errorf("freeze archive candidates: %w", err)
	}
	if len(candidates) == 0 {
		return Job{}, ErrNothingToArchive
	}
	job := newJob(kind, tenantID, rangeToArchive, candidates, now)
	if err := service.store.Create(job); err != nil {
		return Job{}, err
	}
	manifestRef, err := service.objectStore.WriteAndVerify(ctx, job, candidates)
	if err != nil {
		job.Status, job.ErrorMessage, job.UpdatedAt = observabilityvo.ArchiveStatusFailed, err.Error(), service.now().UTC()
		_ = service.store.Update(job)
		return job, err
	}
	// Persist the verified manifest and frozen candidates before any hot data is
	// deleted. A process interruption after this point is recoverable through
	// RetryCleanup; an interruption before it leaves hot data intact.
	job.ManifestRef, job.Status, job.ErrorMessage, job.UpdatedAt = manifestRef, observabilityvo.ArchiveStatusCleanupIncomplete, "", service.now().UTC()
	if err := service.store.Update(job); err != nil {
		return Job{}, err
	}
	if err := service.source.Purge(ctx, kind, tenantID, candidates); err != nil {
		job.Status, job.ErrorMessage, job.UpdatedAt = observabilityvo.ArchiveStatusCleanupIncomplete, err.Error(), service.now().UTC()
		_ = service.store.Update(job)
		return job, nil
	}
	job.Status, job.UpdatedAt = observabilityvo.ArchiveStatusCompleted, service.now().UTC()
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

func (service *Service) Overview(ctx context.Context, kind observabilityvo.ArchiveKind, tenantID string) (Overview, error) {
	if !kind.Valid() || tenantID == "" {
		return Overview{}, fmt.Errorf("invalid archive overview")
	}
	archiveRange := observabilityvo.NewArchiveRange(kind, service.now().UTC(), service.location)
	candidates, err := service.source.Freeze(ctx, kind, tenantID, archiveRange)
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
	return Overview{Kind: kind, RetentionDays: kind.RetentionDays(), Range: archiveRange, CandidateCount: len(candidates), StorageReady: ready}, nil
}

func (service *Service) RetryCleanup(ctx context.Context, jobID, tenantID string) (Job, error) {
	job, ok := service.store.Get(jobID)
	if !ok || job.TenantID != tenantID {
		return Job{}, fmt.Errorf("archive job not found")
	}
	if job.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
		return Job{}, fmt.Errorf("archive cleanup cannot be retried")
	}
	candidates := append([]Candidate(nil), job.Candidates...)
	if len(candidates) != len(job.CandidateIDs) {
		return Job{}, fmt.Errorf("archive cleanup candidates are unavailable")
	}
	if err := service.source.Purge(ctx, job.Kind, tenantID, candidates); err != nil {
		job.ErrorMessage, job.UpdatedAt = err.Error(), service.now().UTC()
		_ = service.store.Update(job)
		return job, nil
	}
	job.Status, job.ErrorMessage, job.UpdatedAt = observabilityvo.ArchiveStatusCompleted, "", service.now().UTC()
	if err := service.store.Update(job); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (service *Service) Get(jobID, tenantID string) (Job, bool) {
	job, ok := service.store.Get(jobID)
	return job, ok && job.TenantID == tenantID
}

// OpenDownload returns the verified archive data through the application
// boundary.  The browser must never receive an internal object-store URL.
func (service *Service) OpenDownload(ctx context.Context, jobID, tenantID string) (Download, error) {
	job, ok := service.Get(jobID, tenantID)
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
func (service *Service) List(tenantID string, kind observabilityvo.ArchiveKind, limit int) []Job {
	if tenantID == "" || !kind.Valid() || limit <= 0 {
		return nil
	}
	return service.store.List(tenantID, kind, limit)
}

func newJob(kind observabilityvo.ArchiveKind, tenantID string, archiveRange observabilityvo.ArchiveRange, candidates []Candidate, now time.Time) Job {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	sort.Strings(ids)
	return Job{ID: fmt.Sprintf("arc_%s_%d", kind, now.UnixNano()), TenantID: tenantID, Kind: kind, Range: archiveRange, Status: observabilityvo.ArchiveStatusFailed, CandidateIDs: ids, Candidates: append([]Candidate(nil), candidates...), CandidateCount: len(candidates), CreatedAt: now, UpdatedAt: now}
}

type memoryStore struct {
	mu   sync.Mutex
	jobs map[string]Job
}

func NewMemoryStore() Store { return &memoryStore{jobs: map[string]Job{}} }
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
	if job.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
		job.Candidates = nil
	}
	return job
}
func (store *memoryStore) Latest(tenantID string, kind observabilityvo.ArchiveKind) (Job, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var result Job
	found := false
	for _, job := range store.jobs {
		if job.TenantID == tenantID && job.Kind == kind && (!found || job.CreatedAt.After(result.CreatedAt)) {
			result, found = job, true
		}
	}
	return result, found
}
func (store *memoryStore) Get(jobID string) (Job, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	job, ok := store.jobs[jobID]
	return job, ok
}
func (store *memoryStore) List(tenantID string, kind observabilityvo.ArchiveKind, limit int) []Job {
	store.mu.Lock()
	defer store.mu.Unlock()
	jobs := make([]Job, 0, limit)
	for _, job := range store.jobs {
		if job.TenantID == tenantID && job.Kind == kind {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs
}
