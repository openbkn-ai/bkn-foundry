// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchconversationaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

type archiveClient interface {
	Search(context.Context, string, []byte) ([]byte, error)
	DeleteByQuery(context.Context, string, []byte) error
}

// ArchiveSource is deliberately scoped to platform-hosted Agent Conversation
// operation facts. External BKN Safe audit adapters never enter this cleanup.
type ArchiveSource struct {
	backend archiveClient
	index   string
}

func NewArchiveSource(backend archiveClient, index string) *ArchiveSource {
	return &ArchiveSource{backend: backend, index: index}
}

func (source *ArchiveSource) Freeze(ctx context.Context, kind observabilityvo.ArchiveKind, archiveRange observabilityvo.ArchiveRange) ([]archivesvc.Candidate, error) {
	batch, err := source.FreezeBatch(ctx, kind, archiveRange)
	if err != nil {
		return nil, err
	}
	if batch.EligibleCount != len(batch.Candidates) {
		return nil, fmt.Errorf("%w: log archive search incomplete (batch limit 10000)", archivesvc.ErrIncompleteCandidates)
	}
	return batch.Candidates, nil
}

func (source *ArchiveSource) FreezeBatch(ctx context.Context, kind observabilityvo.ArchiveKind, archiveRange observabilityvo.ArchiveRange) (archivesvc.Batch, error) {
	if kind != observabilityvo.ArchiveKindLog {
		return archivesvc.Batch{}, nil
	}
	limits := archivesvc.LogLimitsFromContext(ctx)
	if err := limits.Validate(); err != nil {
		return archivesvc.Batch{}, err
	}
	count := limits.MaxCandidates
	query := map[string]any{
		"size":                count,
		"seq_no_primary_term": true, "stored_fields": []string{"_routing"}, "_source": true,
		"track_total_hits": true,
		"sort": []any{map[string]any{
			"@timestamp": map[string]any{"order": "asc"},
		}},
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{
						"range": map[string]any{
							"@timestamp": map[string]any{
								"lt": archiveRange.To.Format(time.RFC3339Nano),
							},
						},
					},
				},
			},
		},
	}
	responseLimit := min(2*limits.MaxPayloadBytes+(1<<20), 16<<20)
	var payload []byte
	selectedID := ""
	eligibleOverride := -1
	selectionQuery := query["query"]
	for {
		if count == 1 {
			query["size"] = 1
			query["_source"] = false
			metaBody, _ := json.Marshal(query)
			meta, readErr := source.searchBounded(ctx, metaBody, 64<<10)
			if readErr != nil {
				return archivesvc.Batch{}, readErr
			}
			var identity struct {
				TimedOut *bool `json:"timed_out"`
				Shards   struct {
					Failed *int `json:"failed"`
				} `json:"_shards"`
				Hits struct {
					Total struct {
						Value    *int   `json:"value"`
						Relation string `json:"relation"`
					} `json:"total"`
					Hits []struct {
						ID string `json:"_id"`
					} `json:"hits"`
				} `json:"hits"`
			}
			if json.Unmarshal(meta, &identity) != nil || identity.TimedOut == nil || *identity.TimedOut || identity.Shards.Failed == nil || *identity.Shards.Failed != 0 || identity.Hits.Total.Value == nil || *identity.Hits.Total.Value < 0 || identity.Hits.Total.Relation != "eq" || len(identity.Hits.Hits) != min(*identity.Hits.Total.Value, 1) {
				return archivesvc.Batch{}, archivesvc.ErrIncompleteCandidates
			}
			if len(identity.Hits.Hits) == 0 {
				return archivesvc.Batch{Batched: true}, nil
			}
			selectedID = identity.Hits.Hits[0].ID
			if selectedID == "" {
				return archivesvc.Batch{}, archivesvc.ErrIncompleteCandidates
			}
			eligibleOverride = *identity.Hits.Total.Value
			query["_source"] = true
			// Pin the probed identity before reading its possibly oversized body.
			query["query"] = map[string]any{"bool": map[string]any{"must": selectionQuery, "filter": map[string]any{"ids": map[string]any{"values": []string{selectedID}}}}}
		}
		query["size"] = count
		body, _ := json.Marshal(query)
		var err error
		payload, err = source.searchBounded(ctx, body, responseLimit)
		if !errors.Is(err, opensearch.ErrSearchResponseBudget) {
			if err != nil {
				return archivesvc.Batch{}, err
			}
			break
		}
		if count > 1 {
			count = max(1, count/2)
			continue
		}
		return archivesvc.Batch{}, &archivesvc.Failure{Stage: "log_search_response", Unit: "bytes", CandidateID: selectedID, Actual: int64(responseLimit + 1), Limit: int64(responseLimit), AtLeast: true}
	}
	var response struct {
		TimedOut *bool `json:"timed_out"`
		Shards   *struct {
			Failed *int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Total *struct {
				Value    *int   `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				ID          string `json:"_id"`
				Index       string `json:"_index"`
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
	if err := json.Unmarshal(payload, &response); err != nil {
		return archivesvc.Batch{}, err
	}
	// A partial search must never become a successful archive of the entire
	// requested range. Reject it before creating a job or deleting hot data.
	if response.TimedOut == nil || response.Shards == nil || response.Shards.Failed == nil || *response.Shards.Failed < 0 {
		return archivesvc.Batch{}, fmt.Errorf("%w: log archive search lacks valid completeness metadata", archivesvc.ErrIncompleteCandidates)
	}
	if *response.TimedOut {
		return archivesvc.Batch{}, fmt.Errorf("%w: log archive search timed out; refusing incomplete candidates", archivesvc.ErrIncompleteCandidates)
	}
	if *response.Shards.Failed > 0 {
		return archivesvc.Batch{}, fmt.Errorf("%w: log archive search has %d failed shards; refusing incomplete candidates", archivesvc.ErrIncompleteCandidates, *response.Shards.Failed)
	}
	total := response.Hits.Total
	if total == nil || total.Value == nil || *total.Value < 0 || total.Relation != "eq" {
		return archivesvc.Batch{}, fmt.Errorf("%w: log archive search did not return an exact candidate total", archivesvc.ErrIncompleteCandidates)
	}
	if len(response.Hits.Hits) != min(*total.Value, count) {
		return archivesvc.Batch{}, fmt.Errorf("%w: log archive batch has inconsistent candidate count", archivesvc.ErrIncompleteCandidates)
	}
	seen := make(map[string]bool, len(response.Hits.Hits))
	candidates := make([]archivesvc.Candidate, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		if hit.ID == "" || seen[hit.ID] || (selectedID != "" && hit.ID != selectedID) {
			return archivesvc.Batch{}, fmt.Errorf("%w: missing or duplicate log identity", archivesvc.ErrIncompleteCandidates)
		}
		seen[hit.ID] = true
		var value struct {
			CreatedAt time.Time `json:"@timestamp"`
		}
		if err := json.Unmarshal(hit.Source, &value); err != nil {
			return archivesvc.Batch{}, err
		}
		if value.CreatedAt.IsZero() || !value.CreatedAt.Before(archiveRange.To) {
			return archivesvc.Batch{}, fmt.Errorf("%w: candidate timestamp outside frozen cutoff", archivesvc.ErrIncompleteCandidates)
		}
		candidate := archivesvc.Candidate{ID: hit.ID, OccurredAt: value.CreatedAt, Payload: hit.Source}
		if _, versioned := source.backend.(frozenLogClient); versioned {
			if len(hit.Fields.Routing) > 1 || (len(hit.Fields.Routing) == 1 && hit.Routing != "" && hit.Routing != hit.Fields.Routing[0]) {
				return archivesvc.Batch{}, archivesvc.ErrIncompleteCandidates
			}
			if len(hit.Fields.Routing) == 1 {
				hit.Routing = hit.Fields.Routing[0]
			}
			candidate.LogDocument = &archivesvc.FrozenLogDocument{Index: hit.Index, SeqNo: hit.SeqNo, PrimaryTerm: hit.PrimaryTerm, Routing: hit.Routing}
			if !validLogVersion(candidate.LogDocument) {
				return archivesvc.Batch{}, fmt.Errorf("%w: log archive requires frozen document versions", archivesvc.ErrIncompleteCandidates)
			}
		}
		candidates = append(candidates, candidate)
	}
	eligible := *total.Value
	if eligibleOverride >= 0 {
		eligible = eligibleOverride
	}
	return archivesvc.Batch{Candidates: candidates, EligibleCount: eligible, Batched: true}, nil
}

func (source *ArchiveSource) searchBounded(ctx context.Context, body []byte, limit int) ([]byte, error) {
	if backend, ok := source.backend.(interface {
		SearchBounded(context.Context, string, []byte, ...int) ([]byte, error)
	}); ok {
		return backend.SearchBounded(ctx, source.index, body, limit)
	}
	raw, err := source.backend.Search(ctx, source.index, body)
	if len(raw) > limit {
		return nil, opensearch.ErrSearchResponseBudget
	}
	return raw, err
}

// Count returns the exact eligible document count without materializing log
// payloads. The archive query and the count predicate intentionally share the
// same @timestamp cutoff.
func (source *ArchiveSource) Count(ctx context.Context, kind observabilityvo.ArchiveKind, archiveRange observabilityvo.ArchiveRange) (int, error) {
	if kind != observabilityvo.ArchiveKindLog {
		return 0, nil
	}
	body, err := json.Marshal(map[string]any{
		"size":             0,
		"track_total_hits": true,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"range": map[string]any{"@timestamp": map[string]any{"lt": archiveRange.To.Format(time.RFC3339Nano)}}},
		}}},
	})
	if err != nil {
		return 0, err
	}
	payload, err := source.backend.Search(ctx, source.index, body)
	if err != nil {
		return 0, err
	}
	var response struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return 0, err
	}
	return response.Hits.Total.Value, nil
}

func (source *ArchiveSource) Purge(ctx context.Context, kind observabilityvo.ArchiveKind, candidates []archivesvc.Candidate) error {
	if kind != observabilityvo.ArchiveKindLog || len(candidates) == 0 {
		return nil
	}
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	body, err := json.Marshal(map[string]any{"query": map[string]any{"ids": map[string]any{"values": ids}}})
	if err != nil {
		return fmt.Errorf("encode frozen archive document ids: %w", err)
	}
	if backend, ok := source.backend.(frozenLogClient); ok {
		if err := source.deleteFrozen(ctx, backend, candidates); err != nil {
			return err
		}
	} else if err := source.backend.DeleteByQuery(ctx, source.index, body); err != nil {
		return err
	}
	check, err := json.Marshal(map[string]any{"size": 0, "track_total_hits": true, "query": map[string]any{"ids": map[string]any{"values": ids}}})
	if err != nil {
		return err
	}
	raw, err := source.backend.Search(ctx, source.index, check)
	if err != nil {
		return err
	}
	var result struct {
		TimedOut *bool `json:"timed_out"`
		Shards   *struct {
			Failed *int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Total *struct {
				Value    *int   `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode archive cleanup verification: %w", err)
	}
	if result.TimedOut == nil || *result.TimedOut || result.Shards == nil || result.Shards.Failed == nil || *result.Shards.Failed != 0 || result.Hits.Total == nil || result.Hits.Total.Value == nil || result.Hits.Total.Relation != "eq" || *result.Hits.Total.Value != 0 {
		return fmt.Errorf("archive cleanup could not verify all frozen IDs absent")
	}
	return nil
}

type frozenLogClient interface {
	DeleteFrozenDocuments(context.Context, []opensearch.FrozenDocument) error
}

func validLogVersion(v *archivesvc.FrozenLogDocument) bool {
	return v != nil && v.Index != "" && v.SeqNo != nil && *v.SeqNo >= 0 && v.PrimaryTerm != nil && *v.PrimaryTerm > 0
}
func (source *ArchiveSource) deleteFrozen(ctx context.Context, backend frozenLogClient, candidates []archivesvc.Candidate) error {
	docs := make([]opensearch.FrozenDocument, 0, len(candidates))
	legacy := map[string]archivesvc.Candidate{}
	for _, c := range candidates {
		if c.LogDocument == nil {
			legacy[c.ID] = c
			continue
		}
		if !validLogVersion(c.LogDocument) {
			return fmt.Errorf("frozen log document version is invalid")
		}
		v := c.LogDocument
		docs = append(docs, opensearch.FrozenDocument{Index: v.Index, ID: c.ID, SeqNo: v.SeqNo, PrimaryTerm: v.PrimaryTerm, Routing: v.Routing})
	}
	if len(legacy) > 0 {
		// Old checkpoints have full _source but no version. Match that archived
		// content first, then freeze the current version to close the read/delete race.
		keys := make([]string, 0, len(legacy))
		for _, c := range candidates {
			if c.LogDocument == nil {
				keys = append(keys, c.ID)
			}
		}
		seen := map[string]bool{}
		for start := 0; start < len(keys); {
			end := start
			size := 0
			for end < len(keys) && end-start < 512 {
				next := len(legacy[keys[end]].Payload)
				if end > start && size+next > 8<<20 {
					break
				}
				size += next
				end++
			}
			chunk := keys[start:end]
			selected := map[string]bool{}
			for _, id := range chunk {
				selected[id] = true
			}
			query, _ := json.Marshal(map[string]any{"size": len(chunk), "track_total_hits": true, "seq_no_primary_term": true, "stored_fields": []string{"_routing"}, "_source": true, "query": map[string]any{"ids": map[string]any{"values": chunk}}})
			raw, err := source.searchBounded(ctx, query, 16<<20)
			if err != nil {
				return fmt.Errorf("legacy log checkpoint verification unavailable")
			}
			var result struct {
				TimedOut *bool `json:"timed_out"`
				Shards   struct {
					Failed *int `json:"failed"`
				} `json:"_shards"`
				Hits struct {
					Total struct {
						Value    int    `json:"value"`
						Relation string `json:"relation"`
					} `json:"total"`
					Hits []struct {
						ID          string `json:"_id"`
						Index       string `json:"_index"`
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
			if json.Unmarshal(raw, &result) != nil || result.TimedOut == nil || *result.TimedOut || result.Shards.Failed == nil || *result.Shards.Failed != 0 || result.Hits.Total.Relation != "eq" || result.Hits.Total.Value != len(result.Hits.Hits) {
				return fmt.Errorf("legacy log checkpoint verification is incomplete")
			}
			for _, hit := range result.Hits.Hits {
				c, ok := legacy[hit.ID]
				if !ok || !selected[hit.ID] || seen[hit.ID] {
					return fmt.Errorf("legacy log checkpoint identity mismatch")
				}
				seen[hit.ID] = true
				var want, got bytes.Buffer
				if json.Compact(&want, c.Payload) != nil || json.Compact(&got, hit.Source) != nil || !bytes.Equal(want.Bytes(), got.Bytes()) {
					return fmt.Errorf("legacy log source changed; manual review required")
				}
				if len(hit.Fields.Routing) > 1 || (len(hit.Fields.Routing) == 1 && hit.Routing != "" && hit.Routing != hit.Fields.Routing[0]) {
					return fmt.Errorf("legacy log routing is ambiguous")
				}
				if len(hit.Fields.Routing) == 1 {
					hit.Routing = hit.Fields.Routing[0]
				}
				v := &archivesvc.FrozenLogDocument{Index: hit.Index, SeqNo: hit.SeqNo, PrimaryTerm: hit.PrimaryTerm, Routing: hit.Routing}
				if !validLogVersion(v) {
					return fmt.Errorf("legacy log checkpoint lacks document versions")
				}
				docs = append(docs, opensearch.FrozenDocument{Index: v.Index, ID: hit.ID, SeqNo: v.SeqNo, PrimaryTerm: v.PrimaryTerm, Routing: v.Routing})
			}
			start = end
		}
	}
	return backend.DeleteFrozenDocuments(ctx, docs)
}
