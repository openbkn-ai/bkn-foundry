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
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

// ArchiveStore copies the raw OTel documents referred to by frozen Operation
// facts into the same JSONL record. Cleanup uses only the concrete document
// versions frozen with that record; Trace IDs select reads, never deletes.
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
	query, _ := json.Marshal(map[string]any{"size": 10000, "track_total_hits": true, "seq_no_primary_term": true, "stored_fields": []string{"_routing"}, "_source": true, "query": map[string]any{"terms": map[string]any{"traceId.keyword": ids}}})
	body, err := store.client.Search(ctx, store.index, query)
	if err != nil {
		return nil, err
	}
	var response struct {
		TimedOut        *bool `json:"timed_out"`
		TerminatedEarly bool  `json:"terminated_early"`
		Shards          *struct {
			Total      *int `json:"total"`
			Successful *int `json:"successful"`
			Failed     *int `json:"failed"`
		} `json:"_shards"`
		Hits *struct {
			Total *struct {
				Value    int    `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits *[]struct {
				Index       string `json:"_index"`
				ID          string `json:"_id"`
				SeqNo       *int64 `json:"_seq_no"`
				PrimaryTerm *int64 `json:"_primary_term"`
				Routing     string `json:"_routing"`
				Fields      struct {
					Routing []string `json:"_routing"`
				} `json:"fields"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("%w: decode technical trace archive result", archivesvc.ErrIncompleteCandidates)
	}
	if response.TimedOut == nil || *response.TimedOut || response.TerminatedEarly || response.Shards == nil || response.Shards.Total == nil || *response.Shards.Total < 0 || response.Shards.Successful == nil || *response.Shards.Successful != *response.Shards.Total || response.Shards.Failed == nil || *response.Shards.Failed != 0 || response.Hits == nil || response.Hits.Total == nil || response.Hits.Total.Relation != "eq" || response.Hits.Total.Value < 0 || response.Hits.Hits == nil {
		return nil, fmt.Errorf("%w: technical trace archive search is incomplete or unverifiable", archivesvc.ErrIncompleteCandidates)
	}
	hits := *response.Hits.Hits
	if response.Hits.Total.Value != len(hits) || len(hits) > 10000 {
		return nil, fmt.Errorf("%w: technical trace archive exceeds its complete 10000-record window", archivesvc.ErrIncompleteCandidates)
	}
	byTrace := map[string][]json.RawMessage{}
	recordsByTrace := map[string][]opensearch.FrozenDocument{}
	seen := map[frozenDocumentKey]struct{}{}
	for _, hit := range hits {
		var source struct {
			TraceID string `json:"traceId"`
		}
		if json.Unmarshal(hit.Source, &source) != nil {
			return nil, fmt.Errorf("%w: invalid technical trace archive source", archivesvc.ErrIncompleteCandidates)
		}
		if _, ok := all[source.TraceID]; !ok {
			return nil, fmt.Errorf("%w: technical trace archive source is outside selection", archivesvc.ErrIncompleteCandidates)
		}
		if len(hit.Fields.Routing) > 1 || len(hit.Fields.Routing) == 1 && hit.Routing != "" && hit.Routing != hit.Fields.Routing[0] {
			return nil, fmt.Errorf("%w: ambiguous technical trace routing", archivesvc.ErrIncompleteCandidates)
		}
		routing := hit.Routing
		if len(hit.Fields.Routing) == 1 {
			routing = hit.Fields.Routing[0]
		}
		document := opensearch.FrozenDocument{Index: hit.Index, ID: hit.ID, SeqNo: hit.SeqNo, PrimaryTerm: hit.PrimaryTerm, Routing: routing}
		if !validFrozenTraceDocument(document) {
			return nil, fmt.Errorf("%w: technical trace archive omitted document identity or version", archivesvc.ErrIncompleteCandidates)
		}
		key := frozenDocumentKey{document.Index, document.ID, document.Routing}
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%w: duplicate technical trace archive document", archivesvc.ErrIncompleteCandidates)
		}
		seen[key] = struct{}{}
		byTrace[source.TraceID] = append(byTrace[source.TraceID], hit.Source)
		recordsByTrace[source.TraceID] = append(recordsByTrace[source.TraceID], document)
	}
	result := make([]archivesvc.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		var payload map[string]any
		if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
			return nil, err
		}
		ids := idsByCandidate[candidate.ID]
		technical := make([]json.RawMessage, 0)
		records := make([]opensearch.FrozenDocument, 0)
		for _, id := range ids {
			technical = append(technical, byTrace[id]...)
			records = append(records, recordsByTrace[id]...)
		}
		payload["technical_trace_ids"] = ids
		payload["technical_traces"] = technical
		payload["technical_trace_records"] = records
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

type frozenDocumentKey struct{ index, id, routing string }

func validFrozenTraceDocument(d opensearch.FrozenDocument) bool {
	return strings.TrimSpace(d.Index) != "" && strings.TrimSpace(d.ID) != "" && d.SeqNo != nil && *d.SeqNo >= 0 && d.PrimaryTerm != nil && *d.PrimaryTerm > 0
}

func (store *ArchiveStore) Purge(ctx context.Context, candidates []archivesvc.Candidate) error {
	documents := make([]opensearch.FrozenDocument, 0)
	seen := map[frozenDocumentKey]opensearch.FrozenDocument{}
	// Validate every frozen candidate before making any destructive request.
	for _, candidate := range candidates {
		ids, err := traceIDs(candidate.Payload)
		if err != nil {
			return err
		}
		var payload struct {
			Records *[]opensearch.FrozenDocument `json:"technical_trace_records"`
			Sources *[]json.RawMessage           `json:"technical_traces"`
		}
		if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
			return fmt.Errorf("decode frozen technical trace cleanup: %w", err)
		}
		if len(ids) == 0 && payload.Records == nil && payload.Sources == nil {
			continue
		}
		if payload.Records == nil || payload.Sources == nil || len(*payload.Records) != len(*payload.Sources) {
			return fmt.Errorf("%w: technical trace cleanup requires frozen document metadata; legacy cleanup was not attempted", archivesvc.ErrIncompleteCandidates)
		}
		allowed := map[string]struct{}{}
		for _, id := range ids {
			allowed[id] = struct{}{}
		}
		for i, d := range *payload.Records {
			var source struct {
				TraceID string `json:"traceId"`
			}
			if json.Unmarshal((*payload.Sources)[i], &source) != nil {
				return fmt.Errorf("%w: invalid frozen technical trace source", archivesvc.ErrIncompleteCandidates)
			}
			if _, ok := allowed[source.TraceID]; !ok || !validFrozenTraceDocument(d) {
				return fmt.Errorf("%w: invalid frozen technical trace cleanup selection", archivesvc.ErrIncompleteCandidates)
			}
			key := frozenDocumentKey{d.Index, d.ID, d.Routing}
			if previous, ok := seen[key]; ok {
				if *previous.SeqNo != *d.SeqNo || *previous.PrimaryTerm != *d.PrimaryTerm {
					return fmt.Errorf("%w: conflicting frozen technical trace versions", archivesvc.ErrIncompleteCandidates)
				}
				continue
			}
			seen[key] = d
			documents = append(documents, d)
		}
	}
	return store.client.DeleteFrozenDocuments(ctx, documents)
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
