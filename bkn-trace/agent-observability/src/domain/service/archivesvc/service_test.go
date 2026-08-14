package archivesvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestCreateWritesVerifiedBundleBeforePurgingFrozenCandidates(t *testing.T) {
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	source := &fakeSource{candidates: []Candidate{{ID: "log-1", OccurredAt: now.AddDate(0, 0, -31), Payload: []byte(`{"id":"log-1"}`)}}}
	storage := &fakeObjectStore{}
	service := New(NewMemoryStore(), source, storage, Options{Now: func() time.Time { return now }})

	job, err := service.Create(context.Background(), observabilityvo.ArchiveKindLog, "tenant-a")
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	if job.Status != observabilityvo.ArchiveStatusCompleted || !source.purged["log-1"] || !storage.verified {
		t.Fatalf("archive did not verify then purge: job=%+v purged=%v verified=%v", job, source.purged, storage.verified)
	}
}

func TestCreateDoesNotPurgeWhenBundleVerificationFails(t *testing.T) {
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	source := &fakeSource{candidates: []Candidate{{ID: "trace-1", OccurredAt: now.AddDate(0, 0, -8), Payload: []byte(`{"id":"trace-1"}`)}}}
	service := New(NewMemoryStore(), source, &fakeObjectStore{verifyErr: errors.New("mismatch")}, Options{Now: func() time.Time { return now }})

	job, err := service.Create(context.Background(), observabilityvo.ArchiveKindTrace, "tenant-a")
	if err == nil || job.Status != observabilityvo.ArchiveStatusFailed || len(source.purged) != 0 {
		t.Fatalf("verification failure must retain hot data: job=%+v err=%v purged=%v", job, err, source.purged)
	}
}

func TestKindsDoNotBlockEachOtherButIncompleteCleanupBlocksSameKind(t *testing.T) {
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	source := &fakeSource{candidates: []Candidate{{ID: "one", OccurredAt: now.AddDate(0, 0, -31), Payload: []byte(`{}`)}}, purgeErr: errors.New("delete failed")}
	service := New(NewMemoryStore(), source, &fakeObjectStore{}, Options{Now: func() time.Time { return now }})
	job, _ := service.Create(context.Background(), observabilityvo.ArchiveKindLog, "tenant-a")
	if job.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
		t.Fatalf("expected cleanup incomplete, got %s", job.Status)
	}
	if _, err := service.Create(context.Background(), observabilityvo.ArchiveKindLog, "tenant-a"); !errors.Is(err, ErrCleanupRequired) {
		t.Fatalf("same kind should require cleanup first: %v", err)
	}
	if _, err := service.Create(context.Background(), observabilityvo.ArchiveKindTrace, "tenant-a"); err != nil && !errors.Is(err, ErrNothingToArchive) {
		t.Fatalf("other archive kind must remain independent: %v", err)
	}
}

func TestRetryCleanupReusesFrozenCandidatePayload(t *testing.T) {
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	source := &fakeSource{candidates: []Candidate{{ID: "trace-1", OccurredAt: now.AddDate(0, 0, -8), Payload: []byte(`{"technical_trace_ids":["trace-a"]}`)}}, purgeErr: errors.New("temporary delete failure")}
	service := New(NewMemoryStore(), source, &fakeObjectStore{}, Options{Now: func() time.Time { return now }})
	job, err := service.Create(context.Background(), observabilityvo.ArchiveKindTrace, "tenant-a")
	if err != nil || job.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
		t.Fatalf("expected incomplete cleanup, job=%+v err=%v", job, err)
	}
	source.purgeErr = nil
	job, err = service.RetryCleanup(context.Background(), job.ID, "tenant-a")
	if err != nil || job.Status != observabilityvo.ArchiveStatusCompleted || !source.purged["trace-1"] {
		t.Fatalf("retry must purge the frozen candidate: job=%+v err=%v purged=%v", job, err, source.purged)
	}
}

func TestCreatePersistsVerifiedManifestAndCandidatesBeforePurge(t *testing.T) {
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	source := &checkingSource{store: store, candidates: []Candidate{{ID: "trace-1", OccurredAt: now.AddDate(0, 0, -8), Payload: []byte(`{"technical_trace_ids":["trace-a"]}`)}}}
	service := New(store, source, &fakeObjectStore{}, Options{Now: func() time.Time { return now }})

	if _, err := service.Create(context.Background(), observabilityvo.ArchiveKindTrace, "tenant-a"); err != nil {
		t.Fatalf("create archive: %v", err)
	}
	if !source.manifestPersisted || !source.candidatesPersisted {
		t.Fatalf("verified archive state was not persisted before purge: %+v", source)
	}
}

func TestCompletedArchiveDoesNotRetainRetryPayload(t *testing.T) {
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	service := New(store, &fakeSource{candidates: []Candidate{{ID: "log-1", OccurredAt: now.AddDate(0, 0, -31), Payload: []byte(`{"id":"log-1"}`)}}}, &fakeObjectStore{}, Options{Now: func() time.Time { return now }})

	job, err := service.Create(context.Background(), observabilityvo.ArchiveKindLog, "tenant-a")
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	stored, ok := store.Get(job.ID)
	if !ok || len(stored.Candidates) != 0 {
		t.Fatalf("completed archive must not retain retry payload: %+v", stored)
	}
}

type fakeSource struct {
	candidates []Candidate
	purged     map[string]bool
	purgeErr   error
}

func (source *fakeSource) Freeze(_ context.Context, _ observabilityvo.ArchiveKind, _ string, _ observabilityvo.ArchiveRange) ([]Candidate, error) {
	return append([]Candidate(nil), source.candidates...), nil
}
func (source *fakeSource) Purge(_ context.Context, _ observabilityvo.ArchiveKind, _ string, candidates []Candidate) error {
	if source.purged == nil {
		source.purged = map[string]bool{}
	}
	for _, candidate := range candidates {
		source.purged[candidate.ID] = true
	}
	return source.purgeErr
}

type fakeObjectStore struct {
	verified  bool
	verifyErr error
}

func (store *fakeObjectStore) WriteAndVerify(_ context.Context, _ Job, _ []Candidate) (string, error) {
	store.verified = true
	if store.verifyErr != nil {
		return "", store.verifyErr
	}
	return "archive/job/manifest.json", nil
}

type checkingSource struct {
	store               Store
	candidates          []Candidate
	manifestPersisted   bool
	candidatesPersisted bool
}

func (source *checkingSource) Freeze(_ context.Context, _ observabilityvo.ArchiveKind, _ string, _ observabilityvo.ArchiveRange) ([]Candidate, error) {
	return append([]Candidate(nil), source.candidates...), nil
}

func (source *checkingSource) Purge(_ context.Context, _ observabilityvo.ArchiveKind, _ string, candidates []Candidate) error {
	job, ok := source.store.Latest("tenant-a", observabilityvo.ArchiveKindTrace)
	if !ok || len(candidates) != 1 || len(job.Candidates) != 1 {
		return errors.New("verified archive state is unavailable before purge")
	}
	source.manifestPersisted = job.ManifestRef != ""
	source.candidatesPersisted = len(job.Candidates[0].Payload) > 0 && candidates[0].ID == job.Candidates[0].ID
	return nil
}
