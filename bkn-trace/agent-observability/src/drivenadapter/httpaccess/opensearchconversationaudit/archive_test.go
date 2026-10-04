// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchconversationaudit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestArchiveSourceUsesOTelTimestampForFreeze(t *testing.T) {
	occurredAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(map[string]any{
		"hits": map[string]any{"hits": []any{map[string]any{
			"_id": "log-1", "_source": map[string]any{"@timestamp": occurredAt.Format(time.RFC3339Nano), "message": "event"},
		}}},
	})
	client := &fakeSearchClient{result: payload}
	source := NewArchiveSource(client, "logs")
	candidates, err := source.Freeze(context.Background(), observabilityvo.ArchiveKindLog, observabilityvo.ArchiveRange{To: occurredAt.Add(time.Hour)})
	if err != nil || len(candidates) != 1 || !candidates[0].OccurredAt.Equal(occurredAt) {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	var query map[string]any
	if err := json.Unmarshal(client.body, &query); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(query)
	if containsString(string(encoded), "created_at") || !containsString(string(encoded), "@timestamp") {
		t.Fatalf("archive query must use @timestamp: %s", encoded)
	}
}

func TestArchiveSourceCountUsesTrackTotalHitsAndOTelTimestamp(t *testing.T) {
	client := &fakeSearchClient{result: []byte(`{"hits":{"total":{"value":6798,"relation":"eq"},"hits":[]}}`)}
	source := NewArchiveSource(client, "logs")
	count, err := source.Count(context.Background(), observabilityvo.ArchiveKindLog, observabilityvo.ArchiveRange{To: time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)})
	if err != nil || count != 6798 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	var query map[string]any
	if err := json.Unmarshal(client.body, &query); err != nil {
		t.Fatal(err)
	}
	if query["size"] != float64(0) || query["track_total_hits"] != true {
		t.Fatalf("count query must avoid payloads and request exact totals: %s", client.body)
	}
	if containsString(string(client.body), "created_at") || !containsString(string(client.body), "@timestamp") {
		t.Fatalf("count query must use @timestamp: %s", client.body)
	}
}
