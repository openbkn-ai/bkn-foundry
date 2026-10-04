// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchconversationaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
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
	if kind != observabilityvo.ArchiveKindLog {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{
		"size": 10000,
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
	})
	payload, err := source.backend.Search(ctx, source.index, body)
	if err != nil {
		return nil, err
	}
	var response struct {
		Hits struct {
			Hits []struct {
				ID     string          `json:"_id"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, err
	}
	candidates := make([]archivesvc.Candidate, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		var value struct {
			CreatedAt time.Time `json:"@timestamp"`
		}
		if err := json.Unmarshal(hit.Source, &value); err != nil {
			return nil, err
		}
		candidates = append(candidates, archivesvc.Candidate{ID: hit.ID, OccurredAt: value.CreatedAt, Payload: hit.Source})
	}
	return candidates, nil
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
	return source.backend.DeleteByQuery(ctx, source.index, body)
}
