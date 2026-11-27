// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func (store *memoryStore) CreateRun(ctx context.Context, run Run) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.runs[run.ID]; ok {
		return errors.New("archive run already exists")
	}
	store.runs[run.ID] = run
	return nil
}

func (store *memoryStore) UpdateRun(ctx context.Context, run Run) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.runs[run.ID]; !ok {
		return ErrRunNotFound
	}
	store.runs[run.ID] = run
	return nil
}

func (store *memoryStore) GetRun(ctx context.Context, id string) (Run, bool, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	run, ok := store.runs[id]
	return run, ok, nil
}

func (store *memoryStore) sortedRuns(kind observabilityvo.ArchiveKind) []Run {
	runs := make([]Run, 0)
	for _, run := range store.runs {
		if run.Kind == kind {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.Before(runs[j].CreatedAt) || runs[i].CreatedAt.Equal(runs[j].CreatedAt) && runs[i].ID < runs[j].ID
	})
	return runs
}

func (store *memoryStore) ActiveRun(ctx context.Context, kind observabilityvo.ArchiveKind) (Run, bool, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, run := range store.sortedRuns(kind) {
		if run.Active() {
			return run, true, nil
		}
	}
	return Run{}, false, nil
}

func (store *memoryStore) LatestRun(ctx context.Context, kind observabilityvo.ArchiveKind) (Run, bool, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	runs := store.sortedRuns(kind)
	if len(runs) == 0 {
		return Run{}, false, nil
	}
	return runs[len(runs)-1], true, nil
}

func (store *memoryStore) LatestBatch(ctx context.Context, batchRunID string) (Job, bool, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	latest := ""
	for id := range store.jobs {
		if strings.HasPrefix(id, batchRunID+"_") && id > latest {
			latest = id
		}
	}
	if latest == "" {
		return Job{}, false, nil
	}
	return storedJob(store.jobs[latest]), true, nil
}

func (store *memoryStore) RetainInteraction(ctx context.Context, item RetainedInteraction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.retained[item.InteractionID] = item
	return nil
}

func (store *memoryStore) RetainedIDs(ctx context.Context, policy string) ([]string, error) {
	items, err := store.ListRetained(ctx, "", policy, 0)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.InteractionID)
	}
	return ids, err
}

func (store *memoryStore) ListRetained(ctx context.Context, runID, policy string, limit int) ([]RetainedInteraction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	items := make([]RetainedInteraction, 0)
	for _, item := range store.retained {
		if (runID == "" || item.RunID == runID) && (policy == "" || item.PolicyVersion == policy) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].RetainedAt.After(items[j].RetainedAt) || items[i].RetainedAt.Equal(items[j].RetainedAt) && items[i].InteractionID < items[j].InteractionID
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

var _ RunStore = (*memoryStore)(nil)
