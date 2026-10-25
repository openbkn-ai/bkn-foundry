// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func archiveFixtureObjects(job Job) map[string][]byte {
	var data []byte
	for _, c := range job.Candidates {
		data = append(data, c.Payload...)
		data = append(data, '\n')
	}
	sum := sha256.Sum256(data)
	key := strings.TrimSuffix(job.ManifestRef, "manifest.json") + "data.jsonl"
	body, _ := json.Marshal(map[string]any{"archive_job_id": job.ID, "archive_kind": job.Kind, "candidate_count": job.CandidateCount, "data_key": key, "sha256": hex.EncodeToString(sum[:])})
	return map[string][]byte{job.ManifestRef: body, key: data}
}

func TestRetryCleanupRevalidatesStoredArchiveBeforeDeleting(t *testing.T) {
	for _, kind := range []observabilityvo.ArchiveKind{observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveKindLog} {
		for _, failure := range []string{"missing reader", "invalid manifest", "missing manifest", "missing data", "corrupt data", "wrong job", "wrong kind", "wrong count", "wrong data key", "rewritten archive", "candidate IDs mismatch", "candidate count mismatch", "valid"} {
			t.Run(string(kind)+"/"+failure, func(t *testing.T) {
				now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
				candidate := Candidate{ID: "frozen", OccurredAt: now.AddDate(0, 0, -31), Payload: []byte(`{"id":"frozen"}`)}
				job := newJob(kind, observabilityvo.ArchiveRange{To: now}, []Candidate{candidate}, now)
				job.Status = observabilityvo.ArchiveStatusCleanupIncomplete
				job.ManifestRef = "archive/job/manifest.json"
				job.BatchProgress = &BatchProgress{RunID: "run", BatchNumber: 1, EligibleAtSelection: 1, VerifiedCount: 1, RemainingAtSelection: 0}
				objects := archiveFixtureObjects(job)
				var manifest map[string]any
				if err := json.Unmarshal(objects[job.ManifestRef], &manifest); err != nil {
					t.Fatal(err)
				}
				switch failure {
				case "missing manifest":
					delete(objects, job.ManifestRef)
				case "missing data":
					delete(objects, "archive/job/data.jsonl")
				case "corrupt data":
					objects["archive/job/data.jsonl"] = []byte("corrupt\n")
				case "wrong job":
					manifest["archive_job_id"] = "another-job"
				case "wrong kind":
					manifest["archive_kind"] = "other"
				case "wrong count":
					manifest["candidate_count"] = 2
				case "wrong data key":
					manifest["data_key"] = "another-job/data.jsonl"
					objects["another-job/data.jsonl"] = objects["archive/job/data.jsonl"]
				case "rewritten archive":
					objects["archive/job/data.jsonl"] = []byte("different but self-consistent\n")
					sum := sha256.Sum256(objects["archive/job/data.jsonl"])
					manifest["sha256"] = hex.EncodeToString(sum[:])
				case "candidate IDs mismatch":
					job.CandidateIDs = []string{"other"}
				case "candidate count mismatch":
					job.CandidateCount = 2
				}
				if failure != "missing manifest" {
					body, err := json.Marshal(manifest)
					if err != nil {
						t.Fatal(err)
					}
					objects[job.ManifestRef] = body
				}
				jobs := NewMemoryStore()
				if err := jobs.Create(job); err != nil {
					t.Fatal(err)
				}
				if failure == "invalid manifest" {
					objects[job.ManifestRef] = []byte("not JSON")
				}
				source := &traceArchiveFlowCore{}
				storage := &fakeDownloadObjectStore{objects: objects}
				// A fresh service must validate persisted state, without refreezing or rewriting it.
				var objectStore ObjectStore = storage
				if failure == "missing reader" {
					objectStore = writeOnlyArchiveFixture{storage}
				}
				svc := New(jobs, source, objectStore, Options{Now: func() time.Time { return now.Add(time.Minute) }})
				result, err := svc.RetryCleanup(context.Background(), job.ID)
				if failure == "valid" {
					if err != nil || result.Status != observabilityvo.ArchiveStatusCompleted || source.purges != 1 || result.BatchProgress.CleanedInRun != 1 {
						t.Fatalf("valid retry: %+v err=%v purges=%d", result, err, source.purges)
					}
				} else {
					stored, _ := jobs.Get(job.ID)
					if err == nil || source.purges != 0 || stored.Status != observabilityvo.ArchiveStatusCleanupIncomplete || stored.ErrorMessage == "" {
						t.Fatalf("unsafe retry: err=%v purges=%d stored=%+v", err, source.purges, stored)
					}
					if !reflect.DeepEqual(stored.Candidates, job.Candidates) || !reflect.DeepEqual(stored.BatchProgress, job.BatchProgress) || stored.ManifestRef != job.ManifestRef {
						t.Fatal("failure lost frozen recovery state")
					}
					if _, err := svc.Create(context.Background(), kind); !errors.Is(err, ErrCleanupRequired) {
						t.Fatalf("failed retry no longer blocks new archive: %v", err)
					}
				}
				if source.freezes != 0 || storage.verified {
					t.Fatal("retry refroze candidates or rewrote archive")
				}
			})
		}
	}
}

// Expose only the write port to model a custom store without recovery reads.
type writeOnlyArchiveFixture struct{ ObjectStore }

func TestRetryVerificationReadFailurePreservesCauseWithoutExposingSignedURL(t *testing.T) {
	for _, cause := range []error{
		&url.Error{Op: "Get", URL: "https://objects.invalid/data?X-Amz-Signature=TEST-SIGNATURE", Err: errors.New("connection failed")},
		io.ErrUnexpectedEOF,
		context.Canceled,
	} {
		now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
		job := newJob(observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{To: now}, []Candidate{{ID: "one", Payload: []byte(`{}`)}}, now)
		job.Status = observabilityvo.ArchiveStatusCleanupIncomplete
		job.ManifestRef = "archive/job/manifest.json"
		jobs := NewMemoryStore()
		if err := jobs.Create(job); err != nil {
			t.Fatal(err)
		}
		source := &traceArchiveFlowCore{}
		svc := New(jobs, source, &fakeDownloadObjectStore{readErr: cause}, Options{})
		result, err := svc.RetryCleanup(context.Background(), job.ID)
		if !errors.Is(err, cause) || source.purges != 0 || result.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
			t.Fatalf("read failure lost: %+v %v", result, err)
		}
		stored, _ := jobs.Get(job.ID)
		if stored.ErrorMessage == "" || strings.Contains(stored.ErrorMessage, "https://") || strings.Contains(stored.ErrorMessage, "TEST-SIGNATURE") {
			t.Fatalf("unsafe public message: %q", stored.ErrorMessage)
		}
	}
}
