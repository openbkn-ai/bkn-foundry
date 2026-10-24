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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"testing"
	"time"
)

func TestArchiveFreezeBatchUsesBoundedExactPopulation(t *testing.T) {
	for _, tc := range []struct {
		total, n       int
		badID, badTime bool
		ok             bool
	}{{10001, 10000, false, false, true}, {10000, 10000, false, false, true}, {1, 1, false, false, true}, {0, 0, false, false, true}, {10001, 9999, false, false, false}, {1, 1, true, false, false}, {1, 1, false, true, false}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			hits := []any{}
			for i := 0; i < tc.n; i++ {
				id := fmt.Sprint(i)
				if tc.badID {
					id = ""
				}
				ts := "2025-01-01T00:00:00Z"
				if tc.badTime {
					ts = "2027-01-01T00:00:00Z"
				}
				hits = append(hits, map[string]any{"_id": id, "_source": map[string]any{"@timestamp": ts}})
			}
			raw, err := json.Marshal(map[string]any{"timed_out": false, "_shards": map[string]any{"failed": 0}, "hits": map[string]any{"total": map[string]any{"value": tc.total, "relation": "eq"}, "hits": hits}})
			if err != nil {
				t.Fatal(err)
			}
			client := &archiveSafetyClient{response: raw}
			source := NewArchiveSource(client, "logs")
			r := observabilityvo.ArchiveRange{To: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
			b, err := source.FreezeBatch(context.Background(), observabilityvo.ArchiveKindLog, r)
			if tc.ok {
				if err != nil || !b.Batched || b.EligibleCount != tc.total || len(b.Candidates) != tc.n {
					t.Fatalf("count=%d eligible=%d batched=%v err=%v", len(b.Candidates), b.EligibleCount, b.Batched, err)
				}
			} else if !errors.Is(err, archivesvc.ErrIncompleteCandidates) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestArchivePurgeVerifiesFrozenIDsAbsent(t *testing.T) {
	for _, raw := range []string{
		`{"timed_out":false,"_shards":{"failed":0},"hits":{"total":{"value":1,"relation":"eq"}}}`,
		`{"timed_out":true,"_shards":{"failed":0},"hits":{"total":{"value":0,"relation":"eq"}}}`,
		`{"timed_out":false,"_shards":{"failed":1},"hits":{"total":{"value":0,"relation":"eq"}}}`,
		`{"timed_out":false,"_shards":{"failed":0},"hits":{"total":{"value":0,"relation":"gte"}}}`, `{}`,
	} {
		client := &archiveSafetyClient{cleanupResponse: []byte(raw)}
		source := NewArchiveSource(client, "logs")
		err := source.Purge(context.Background(), observabilityvo.ArchiveKindLog, []archivesvc.Candidate{{ID: "frozen"}})
		if err == nil {
			t.Fatal("incomplete cleanup accepted")
		}
		var q map[string]any
		if e := json.Unmarshal(client.body, &q); e != nil {
			t.Fatal(e)
		}
		if q["size"] != float64(0) || q["track_total_hits"] != true {
			t.Fatal("cleanup verification must be exact metadata-only count")
		}
		query := q["query"].(map[string]any)
		if query["ids"] == nil {
			t.Fatal("cleanup must verify frozen IDs, not a cutoff")
		}
	}
}
