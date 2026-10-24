// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchconversationaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type archiveSafetyClient struct {
	response   []byte
	body       []byte
	purgeCalls int
}

func (c *archiveSafetyClient) Search(_ context.Context, _ string, body []byte) ([]byte, error) {
	c.body = body
	return c.response, nil
}
func (c *archiveSafetyClient) DeleteByQuery(context.Context, string, []byte) error {
	c.purgeCalls++
	return nil
}

type archiveSafetyStore struct{ writes int }

func (s *archiveSafetyStore) WriteAndVerify(context.Context, archivesvc.Job, []archivesvc.Candidate) (string, error) {
	s.writes++
	return "verified", nil
}

func TestArchiveCreateRejectsIncompleteOrUntrustedSearchWithoutWritesOrPurge(t *testing.T) {
	for _, tc := range []struct {
		name, total, extra, reason string
		returned                   int
	}{
		{"over_limit", `{"value":10001,"relation":"eq"}`, "", "incomplete", 10000},
		{"lower_bound", `{"value":10000,"relation":"gte"}`, "", "exact", 10000},
		{"missing_total", `null`, "", "exact", 1},
		{"missing_relation", `{"value":1}`, "", "exact", 1},
		{"unknown_relation", `{"value":1,"relation":"unknown"}`, "", "exact", 1},
		{"missing_value", `{"relation":"eq"}`, "", "exact", 1},
		{"negative_total", `{"value":-1,"relation":"eq"}`, "", "exact", 1},
		{"less_than_returned", `{"value":0,"relation":"eq"}`, "", "inconsistent", 1},
		{"missing_timeout", `{"value":1,"relation":"eq"}`, `,"_shards":{"failed":0}`, "completeness", 1},
		{"missing_shards", `{"value":1,"relation":"eq"}`, `,"timed_out":false`, "completeness", 1},
		{"timeout", `{"value":1,"relation":"eq"}`, `,"timed_out":true,"_shards":{"failed":0}`, "timed out", 1},
		{"failed_shard", `{"value":1,"relation":"eq"}`, `,"timed_out":false,"_shards":{"failed":1}`, "failed shards", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := make([]string, tc.returned)
			for i := range hits {
				hits[i] = fmt.Sprintf(`{"_id":"log-%d","_source":{"@timestamp":"2025-01-01T00:00:00Z"}}`, i)
			}
			extra := tc.extra
			if extra == "" {
				extra = `,"timed_out":false,"_shards":{"failed":0}`
			}
			client := &archiveSafetyClient{response: []byte(`{"hits":{"total":` + tc.total + `,"hits":[` + strings.Join(hits, ",") + `]}` + extra + `}`)}
			store := &archiveSafetyStore{}
			jobs := archivesvc.NewMemoryStore()
			service := archivesvc.New(jobs, NewArchiveSource(client, "logs"), store, archivesvc.Options{Now: func() time.Time { return time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC) }, Location: time.UTC})
			_, err := service.Create(context.Background(), observabilityvo.ArchiveKindLog)
			if !errors.Is(err, archivesvc.ErrIncompleteCandidates) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("want diagnostic %q, got %v", tc.reason, err)
			}
			if store.writes != 0 || client.purgeCalls != 0 {
				t.Fatalf("unsafe side effects: writes=%d purge=%d", store.writes, client.purgeCalls)
			}
			if _, ok := jobs.Latest(observabilityvo.ArchiveKindLog); ok {
				t.Fatal("incomplete candidates must not create an archive job")
			}
			var q map[string]any
			if err := json.Unmarshal(client.body, &q); err != nil {
				t.Fatalf("decode archive search request: %v", err)
			}
			if q["track_total_hits"] != true {
				t.Fatal("freeze requires exact totals")
			}
		})
	}
}

func TestArchiveCreateAcceptsCompleteTenThousandBoundary(t *testing.T) {
	hits := make([]string, 10000)
	for i := range hits {
		hits[i] = fmt.Sprintf(`{"_id":"log-%d","_source":{"@timestamp":"2025-01-01T00:00:00Z"}}`, i)
	}
	client := &archiveSafetyClient{response: []byte(`{"timed_out":false,"_shards":{"failed":0},"hits":{"total":{"value":10000,"relation":"eq"},"hits":[` + strings.Join(hits, ",") + `]}}`)}
	store := &archiveSafetyStore{}
	service := archivesvc.New(archivesvc.NewMemoryStore(), NewArchiveSource(client, "logs"), store, archivesvc.Options{})
	job, err := service.Create(context.Background(), observabilityvo.ArchiveKindLog)
	if err != nil || job.CandidateCount != 10000 || job.Status != observabilityvo.ArchiveStatusCompleted || store.writes != 1 || client.purgeCalls != 1 {
		t.Fatalf("complete boundary failed: count=%d err=%v writes=%d purge=%d", job.CandidateCount, err, store.writes, client.purgeCalls)
	}
}

func TestArchiveCreateEmptyExactResultDoesNotWriteOrPurge(t *testing.T) {
	client := &archiveSafetyClient{response: []byte(`{"timed_out":false,"_shards":{"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`)}
	store := &archiveSafetyStore{}
	jobs := archivesvc.NewMemoryStore()
	service := archivesvc.New(jobs, NewArchiveSource(client, "logs"), store, archivesvc.Options{})
	_, err := service.Create(context.Background(), observabilityvo.ArchiveKindLog)
	if !errors.Is(err, archivesvc.ErrNothingToArchive) || store.writes != 0 || client.purgeCalls != 0 {
		t.Fatalf("empty result: err=%v writes=%d purge=%d", err, store.writes, client.purgeCalls)
	}
	if _, ok := jobs.Latest(observabilityvo.ArchiveKindLog); ok {
		t.Fatal("empty result created job")
	}
}
