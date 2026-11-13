// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchconversationaudit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

func capacitySearch(total int, source string) []byte {
	hits := []any{}
	if source != "" {
		hits = append(hits, map[string]any{"_index": "logs-0001", "_id": "log-a", "_seq_no": 7, "_primary_term": 3, "fields": map[string]any{"_routing": []string{"tenant-a"}}, "_source": json.RawMessage(source)})
	}
	raw, _ := json.Marshal(map[string]any{"timed_out": false, "_shards": map[string]any{"failed": 0}, "hits": map[string]any{"total": map[string]any{"value": total, "relation": "eq"}, "hits": hits}})
	return raw
}

const capacityLog = `{"@timestamp":"2026-01-01T00:00:00Z","message":"original"}`

type versionedArchiveFake struct {
	results       [][]byte
	bodies        [][]byte
	documents     []opensearch.FrozenDocument
	unconditional int
}

func (f *versionedArchiveFake) Search(_ context.Context, _ string, body []byte) ([]byte, error) {
	f.bodies = append(f.bodies, body)
	r := f.results[0]
	f.results = f.results[1:]
	return r, nil
}
func (f *versionedArchiveFake) DeleteByQuery(context.Context, string, []byte) error {
	f.unconditional++
	return nil
}
func (f *versionedArchiveFake) DeleteFrozenDocuments(_ context.Context, docs []opensearch.FrozenDocument) error {
	f.documents = append(f.documents, docs...)
	return nil
}

func TestLogArchiveFreezesVersionRoutingAndDeletesConditionally(t *testing.T) {
	backend := &versionedArchiveFake{results: [][]byte{capacitySearch(1, capacityLog), capacitySearch(0, "")}}
	source := NewArchiveSource(backend, "logs")
	batch, err := source.FreezeBatch(context.Background(), observabilityvo.ArchiveKindLog, observabilityvo.ArchiveRange{To: time.Now()})
	if err != nil || len(batch.Candidates) != 1 || batch.Candidates[0].LogDocument == nil {
		t.Fatalf("freeze: %+v %v", batch, err)
	}
	if err = source.Purge(context.Background(), observabilityvo.ArchiveKindLog, batch.Candidates); err != nil {
		t.Fatal(err)
	}
	if backend.unconditional != 0 || len(backend.documents) != 1 || backend.documents[0].ID != "log-a" || backend.documents[0].Index != "logs-0001" || *backend.documents[0].SeqNo != 7 || *backend.documents[0].PrimaryTerm != 3 || backend.documents[0].Routing != "tenant-a" {
		t.Fatal("log cleanup lost frozen version/routing")
	}
}
func TestOneLogPageKeepsEligibleCountFromIdentityProbe(t *testing.T) {
	backend := &versionedArchiveFake{results: [][]byte{capacitySearch(5, capacityLog), capacitySearch(1, capacityLog)}}
	ctx := archivesvc.WithLogLimits(context.Background(), archivesvc.LogLimits{MaxCandidates: 1, MaxPayloadBytes: 1024})
	batch, err := NewArchiveSource(backend, "logs").FreezeBatch(ctx, observabilityvo.ArchiveKindLog, observabilityvo.ArchiveRange{To: time.Now()})
	if err != nil || len(batch.Candidates) != 1 || batch.EligibleCount != 5 {
		t.Fatalf("single-row selection falsely completed range: %+v %v", batch, err)
	}
	var query map[string]any
	if json.Unmarshal(backend.bodies[1], &query) != nil || query["query"] == nil {
		t.Fatal("missing pinned identity query")
	}
}
func TestLegacyLogCheckpointRequiresMatchingArchivedContent(t *testing.T) {
	for _, changed := range []bool{false, true} {
		content := capacityLog
		if changed {
			content = `{"@timestamp":"2026-01-01T00:00:00Z","message":"newer"}`
		}
		backend := &versionedArchiveFake{results: [][]byte{capacitySearch(1, content), capacitySearch(0, "")}}
		c := archivesvc.Candidate{ID: "log-a", Payload: []byte(capacityLog)}
		err := NewArchiveSource(backend, "logs").Purge(context.Background(), observabilityvo.ArchiveKindLog, []archivesvc.Candidate{c})
		if changed {
			if err == nil || len(backend.documents) != 0 || backend.unconditional != 0 {
				t.Fatal("changed legacy source deleted")
			}
		} else if err != nil || len(backend.documents) != 1 {
			t.Fatalf("matching legacy checkpoint not recovered: %v", err)
		}
	}
}

type oversizedLogFake struct {
	versionedArchiveFake
	reads int
}

func (f *oversizedLogFake) SearchBounded(_ context.Context, _ string, body []byte, _ ...int) ([]byte, error) {
	f.reads++
	var query map[string]any
	_ = json.Unmarshal(body, &query)
	if query["_source"] == false {
		return capacitySearch(5, capacityLog), nil
	}
	return nil, opensearch.ErrSearchResponseBudget
}
func TestOversizedLogBodyIsBoundedAndIdentifiedBeforeRead(t *testing.T) {
	f := &oversizedLogFake{}
	_, err := NewArchiveSource(f, "logs").FreezeBatch(context.Background(), observabilityvo.ArchiveKindLog, observabilityvo.ArchiveRange{To: time.Now()})
	var detail *archivesvc.Failure
	if !errors.As(err, &detail) || detail.Stage != "log_search_response" || detail.CandidateID != "log-a" || !detail.AtLeast || detail.Actual != detail.Limit+1 || f.reads > 16 || len(f.documents) != 0 || f.unconditional != 0 {
		t.Fatalf("unbounded/unidentified log: %v reads=%d", err, f.reads)
	}
}
