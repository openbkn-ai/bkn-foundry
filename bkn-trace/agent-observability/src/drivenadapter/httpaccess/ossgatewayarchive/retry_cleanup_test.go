// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ossgatewayarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestCleanupRetryReadsExistingGatewayObjectsBeforePurge(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	uploads := 0
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/objects" {
			key := r.URL.Query().Get("key")
			if r.Method == http.MethodPut {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "read failed", 500)
					return
				}
				objects[key] = data
				uploads++
				w.WriteHeader(http.StatusNoContent)
				return
			}
			data, ok := objects[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
			return
		}
		operation, key, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/"), "/archive/")
		if !ok || (operation != "upload" && operation != "download") {
			http.NotFound(w, r)
			return
		}
		method := http.MethodGet
		if operation == "upload" {
			method = http.MethodPut
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"method": method, "url": endpoint + "/objects?key=" + url.QueryEscape(key)}})
	}))
	defer server.Close()
	endpoint = server.URL
	uploadCount := func() int { mu.Lock(); defer mu.Unlock(); return uploads }
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	source := &retryCleanupSource{candidates: []archivesvc.Candidate{
		{ID: "z", OccurredAt: now.AddDate(0, 0, -8), Payload: []byte(`{"id":"z"}`)},
		{ID: "a", OccurredAt: now.AddDate(0, 0, -8), Payload: []byte(`{"id":"a"}`)},
	}}
	jobs := archivesvc.NewMemoryStore()
	client := New(Config{BaseURL: endpoint + "/api", StorageID: "archive"})
	svc := archivesvc.New(jobs, source, client, archivesvc.Options{Now: func() time.Time { return now }})
	job, err := svc.Create(context.Background(), observabilityvo.ArchiveKindTrace)
	if err != nil || job.Status != observabilityvo.ArchiveStatusCleanupIncomplete || uploadCount() != 2 || source.purges != 1 {
		t.Fatalf("create: %+v %v uploads=%d purges=%d", job, err, uploadCount(), source.purges)
	}
	key := strings.TrimSuffix(job.ManifestRef, "manifest.json") + "data.jsonl"
	mu.Lock()
	saved := bytes.Clone(objects[key])
	mu.Unlock()
	for _, failure := range []string{"missing", "corrupt"} {
		mu.Lock()
		if failure == "missing" {
			delete(objects, key)
		} else {
			objects[key] = []byte("corrupted\n")
		}
		mu.Unlock()
		// A new client/service exercises recovery using only durable job/object data.
		retry := archivesvc.New(jobs, source, New(Config{BaseURL: endpoint + "/api", StorageID: "archive"}), archivesvc.Options{})
		result, err := retry.RetryCleanup(context.Background(), job.ID)
		if err == nil || result.Status != observabilityvo.ArchiveStatusCleanupIncomplete || source.purges != 1 || uploadCount() != 2 {
			t.Fatalf("%s: %+v %v purges=%d uploads=%d", failure, result, err, source.purges, uploadCount())
		}
	}
	mu.Lock()
	objects[key] = saved
	mu.Unlock()
	result, err := svc.RetryCleanup(context.Background(), job.ID)
	if err != nil || result.Status != observabilityvo.ArchiveStatusCompleted || source.purges != 2 || source.freezes != 1 || uploadCount() != 2 {
		t.Fatalf("restored retry: %+v %v purges=%d freezes=%d uploads=%d", result, err, source.purges, source.freezes, uploadCount())
	}
}

type retryCleanupSource struct {
	candidates      []archivesvc.Candidate
	freezes, purges int
}

func (s *retryCleanupSource) Freeze(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) ([]archivesvc.Candidate, error) {
	s.freezes++
	return s.candidates, nil
}
func (s *retryCleanupSource) Purge(context.Context, observabilityvo.ArchiveKind, []archivesvc.Candidate) error {
	s.purges++
	if s.purges == 1 {
		return errors.New("temporary cleanup failure")
	}
	return nil
}
