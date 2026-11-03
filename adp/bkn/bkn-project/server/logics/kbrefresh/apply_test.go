// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"slices"
	"testing"
)

type applicationStorage struct {
	*fixedModelStorage
	rows           map[string]map[string]map[string]any
	writes, prunes int
	failAt         int
	failed         bool
}

func (s *applicationStorage) Replace(_ context.Context, spec interfaces.DraftDatasetSpec, row map[string]any) error {
	s.writes++
	raw, _ := json.Marshal(row)
	copy := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&copy)
	if s.rows[spec.ResourceID] == nil {
		s.rows[spec.ResourceID] = map[string]map[string]any{}
	}
	s.rows[spec.ResourceID][copy["_id"].(string)] = copy
	if !s.failed && s.failAt > 0 && s.writes == s.failAt {
		s.failed = true
		return errors.New("write succeeded but acknowledgement lost")
	}
	return nil
}
func (s *applicationStorage) ReadFileRows(ctx context.Context, spec interfaces.DraftDatasetSpec, scope kb.DatasetFileScope, guard func(context.Context) error) ([]map[string]any, error) {
	if err := guard(ctx); err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, row := range s.rows[spec.ResourceID] {
		if rowInFileScope(spec, row, scope) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (s *applicationStorage) Prune(ctx context.Context, spec interfaces.DraftDatasetSpec, scope kb.DatasetFileScope, keep []string, guard func(context.Context) error) error {
	if err := guard(ctx); err != nil {
		return err
	}
	s.prunes++
	for id, row := range s.rows[spec.ResourceID] {
		if rowInFileScope(spec, row, scope) && !slices.Contains(keep, id) {
			delete(s.rows[spec.ResourceID], id)
		}
	}
	return nil
}
func (s *applicationStorage) ReadEntityRows(ctx context.Context, spec interfaces.DraftDatasetSpec, model string, guard func(context.Context) error) ([]map[string]any, error) {
	if err := guard(ctx); err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, row := range s.rows[spec.ResourceID] {
		if row["model_fingerprint"] == model {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (s *applicationStorage) PruneEntities(ctx context.Context, spec interfaces.DraftDatasetSpec, model string, keep []string, guard func(context.Context) error) error {
	if err := guard(ctx); err != nil {
		return err
	}
	s.prunes++
	for id, row := range s.rows[spec.ResourceID] {
		if row["model_fingerprint"] == model && !slices.Contains(keep, id) {
			delete(s.rows[spec.ResourceID], id)
		}
	}
	return nil
}
func TestPublishedRefreshAppliesRecoversNoOpAndRetiresRemovedFilesWithoutMoreInference(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost_write_ack"}[uncertain], func(t *testing.T) {
			service, _, _, storage, previous, _ := refreshPreparationFixture(t)
			for i := range previous.Files {
				previous.Files[i].File.ContentVersionID = "old-version"
			}
			source := &extractionSource{preparedSource: &preparedSource{snapshotSource: &snapshotSource{}}}
			service.Sources = &preparedSources{source: source}
			journal := &memoryRefreshJournal{}
			service.Journal = journal
			tasks := &extractionTasks{journal: journal, t: t, succeeded: true}
			service.Tasks = tasks
			data := &applicationStorage{fixedModelStorage: storage, rows: map[string]map[string]map[string]any{}}
			if uncertain {
				data.failAt = 2
			}
			service.Storage = data
			ctx := context.Background()
			beginReviewed(t, service, ctx, "base", "batch-one", "worker")
			extract := func() {
				t.Helper()
				for step := 0; step < 12 && journal.value.State != "ready"; step++ {
					if err := service.ExtractStep(ctx, "base", "worker"); err != nil {
						t.Fatal(err)
					}
				}
				if journal.value.State != "ready" {
					t.Fatalf("extraction stopped at %s/%s", journal.value.State, journal.value.Reason)
				}
			}
			apply := func() {
				t.Helper()
				plan, err := service.application(ctx, journal.value)
				if err != nil {
					t.Fatal(err)
				}
				if err := service.BeginApply(ctx, "base", "worker", plan.staged.Digest, plan.fusion.Preview.Signature); err != nil {
					t.Fatal(err)
				}
				for step := 0; step < 30 && journal.value.State != "idle"; step++ {
					journal.value.NextCheck = 0
					err := service.ApplyStep(ctx, "base", "worker")
					if err != nil && !errors.Is(err, kb.ErrUnavailable) {
						t.Fatal(err)
					}
					if journal.value.State == "attention" {
						t.Fatalf("application stopped at %s", journal.value.Reason)
					}
				}
				if journal.value.State != "idle" {
					t.Fatalf("application never completed: %s", journal.value.State)
				}
			}
			extract()
			apply()
			if tasks.starts != 2 || journal.value.Previous == nil || journal.value.Previous.Linking == nil || !journal.value.Previous.Linking.FusionApplied || len(journal.value.Holds) != 1 {
				t.Fatal("refresh did not commit actual execution and fusion receipts")
			}
			writes := data.writes
			journal.value.NextCheck = 0
			noOp, err := service.Begin(ctx, "base", "no-op", "worker")
			if err != nil || noOp.State != "idle" || noOp.Pending != nil || data.writes != writes || tasks.starts != 2 {
				t.Fatal("unchanged source rewrote rows or reran inference", err)
			}
			// A no-source-delta reconciliation must reuse actual task receipts and
			// leave unchanged data/holds alone while checking the existing K5 plan.
			polls, releases := tasks.polls, source.releases
			if err := service.Reconcile(ctx, noOp, "worker"); err != nil {
				t.Fatal(err)
			}
			if journal.value.Pending != nil || data.writes != writes || tasks.polls != polls || source.releases != releases {
				t.Fatal("unchanged reconciliation re-extracted, rewrote rows or released current holds")
			}
			// An initial or invalidated reconciliation receipt uses the ordinary
			// durable application path without replacing its retained source holds.
			journal.value.Previous.Linking = nil
			candidate, err := journal.Get(ctx, "base")
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Reconcile(ctx, candidate, "worker"); err != nil {
				t.Fatal(err)
			}
			if journal.value.State != "ready" || !journal.value.Pending.ReuseHolds || journal.value.Pending.EvaluationTime <= 0 {
				t.Fatal("reconciliation was not durably prepared")
			}
			apply()
			if source.releases != releases || len(journal.value.Holds) != 1 || tasks.starts != 2 || tasks.polls != polls {
				t.Fatal("reconciliation discarded current holds or reran inference")
			}
			source.scenario = "empty"
			journal.value.NextCheck = 0
			beginReviewed(t, service, ctx, "base", "remove-all", "worker")
			extract()
			apply()
			for _, rows := range data.rows {
				if len(rows) != 0 {
					t.Fatal("removed source left live instance or canonical rows")
				}
			}
			if tasks.starts != 2 || source.releases != 1 || len(journal.value.Previous.Files) != 0 || len(journal.value.Holds) != 0 {
				t.Fatal("removal reran inference or lost hold cleanup")
			}
		})
	}
}
