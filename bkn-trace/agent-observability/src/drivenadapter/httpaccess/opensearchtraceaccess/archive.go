// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchtraceaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

// ArchiveStore copies the raw OTel documents referred to by frozen Operation
// facts into the same JSONL record.  It is deliberately keyed by trace ID,
// not by a fresh cutoff query, so cleanup cannot catch newer documents.
type ArchiveStore struct {
	client *opensearch.Client
	index  string
}

func NewArchiveStore(client *opensearch.Client, index string) *ArchiveStore {
	return &ArchiveStore{client: client, index: index}
}

func (store *ArchiveStore) Enrich(ctx context.Context, candidates []archivesvc.Candidate) ([]archivesvc.Candidate, error) {
	idsByCandidate := map[string][]string{}
	all := map[string]struct{}{}
	for _, candidate := range candidates {
		ids, err := traceIDs(candidate.Payload)
		if err != nil {
			return nil, err
		}
		idsByCandidate[candidate.ID] = ids
		for _, id := range ids {
			all[id] = struct{}{}
		}
	}
	if len(all) == 0 {
		return candidates, nil
	}
	ids := sortedKeys(all)
	query, _ := json.Marshal(map[string]any{"size": 10000, "track_total_hits": true, "query": map[string]any{"terms": map[string]any{"traceId.keyword": ids}}})
	body, err := store.client.Search(ctx, store.index, query)
	if err != nil {
		return nil, err
	}
	var response struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID     string          `json:"_id"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if searchResultTruncated(response.Hits.Total.Value, len(response.Hits.Hits)) {
		return nil, fmt.Errorf("technical trace archive exceeds the 10000-record batch limit")
	}
	byTrace := map[string][]json.RawMessage{}
	for _, hit := range response.Hits.Hits {
		var source map[string]any
		if json.Unmarshal(hit.Source, &source) != nil {
			continue
		}
		traceID, _ := source["traceId"].(string)
		if traceID != "" {
			byTrace[traceID] = append(byTrace[traceID], hit.Source)
		}
	}
	result := make([]archivesvc.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		var payload map[string]any
		if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
			return nil, err
		}
		ids := idsByCandidate[candidate.ID]
		technical := make([]json.RawMessage, 0)
		for _, id := range ids {
			technical = append(technical, byTrace[id]...)
		}
		payload["technical_trace_ids"] = ids
		payload["technical_traces"] = technical
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		candidate.Payload = encoded
		result = append(result, candidate)
	}
	return result, nil
}

func searchResultTruncated(total, returned int) bool { return total > returned }

func (store *ArchiveStore) Purge(ctx context.Context, candidates []archivesvc.Candidate) error {
	all := map[string]struct{}{}
	for _, candidate := range candidates {
		ids, err := traceIDs(candidate.Payload)
		if err != nil {
			return err
		}
		for _, id := range ids {
			all[id] = struct{}{}
		}
	}
	if len(all) == 0 {
		return nil
	}
	query, _ := json.Marshal(map[string]any{"query": map[string]any{"terms": map[string]any{"traceId.keyword": sortedKeys(all)}}})
	return store.client.DeleteByQuery(ctx, store.index, query)
}

type archivePayload struct {
	CallFacts []struct {
		TraceID string `json:"trace_id"`
	} `json:"call_facts"`
	TechnicalTraceIDs []string `json:"technical_trace_ids"`
}

func traceIDs(payload []byte) ([]string, error) {
	var value archivePayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, fmt.Errorf("decode archive trace ids: %w", err)
	}
	unique := map[string]struct{}{}
	for _, fact := range value.CallFacts {
		if fact.TraceID != "" {
			unique[fact.TraceID] = struct{}{}
		}
	}
	for _, id := range value.TechnicalTraceIDs {
		if id != "" {
			unique[id] = struct{}{}
		}
	}
	return sortedKeys(unique), nil
}
func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

var _ archivesvc.TraceTechnicalStore = (*ArchiveStore)(nil)
