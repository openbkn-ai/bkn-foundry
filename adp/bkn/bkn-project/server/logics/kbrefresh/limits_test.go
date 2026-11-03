// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestRefreshSoftLimitsUseConfirmedDefaultsAndActualTaskDurations(t *testing.T) {
	limits := DefaultLimits()
	baseline := &interfaces.KBBuild{Files: make([]interfaces.KBFileBuild, 10)}
	duration := int64(60000)
	for i := range baseline.Files {
		baseline.Files[i].File = interfaces.KBResourceFile{KnowledgeBaseID: "kb", FileID: string(rune('a' + i)), ContentVersionID: "v1", ChunkCount: 10}
		baseline.Files[i].DurationMillis = &duration
	}
	current := append([]interfaces.KBResourceFile{}, baseline.Files[0].File, baseline.Files[1].File)
	for i := 2; i < 10; i++ {
		current = append(current, baseline.Files[i].File)
	}
	for _, scenario := range []string{"within", "changed_large", "removed_large", "unknown_duration", "duration_large", "retired_version_only"} {
		t.Run(scenario, func(t *testing.T) {
			build := &interfaces.KBBuild{Files: []interfaces.KBFileBuild{}}
			for _, file := range current {
				build.Files = append(build.Files, interfaces.KBFileBuild{File: file})
			}
			plan := &ManifestPlan{Build: build, Changed: []int{0, 1}, Removed: []interfaces.KBResourceFile{baseline.Files[0].File}}
			previous := *baseline
			previous.Files = append([]interfaces.KBFileBuild{}, baseline.Files...)
			switch scenario {
			case "changed_large":
				plan.Changed = []int{0, 1, 2}
			case "removed_large":
				build.Files = build.Files[:8]
				plan.Removed = []interfaces.KBResourceFile{baseline.Files[8].File, baseline.Files[9].File}
			case "unknown_duration":
				for i := range previous.Files {
					previous.Files[i].DurationMillis = nil
				}
			case "duration_large":
				build.Files[0].File.ChunkCount = 1000
			case "retired_version_only":
				plan.Changed = nil
			}
			estimate, err := EvaluateLimits(limits, &previous, plan)
			if err != nil {
				t.Fatal(err)
			}
			expected := scenario != "within" && scenario != "retired_version_only"
			if (len(estimate.Reasons) > 0) != expected {
				t.Fatalf("wrong soft gate %+v", estimate)
			}
			if scenario == "retired_version_only" && estimate.RemovedFiles != 0 {
				t.Fatal("superseded content version counted as removed file")
			}
		})
	}
}

func TestExceptionalBatchReleaseCannotBypassHardFailuresOrChangeItsScope(t *testing.T) {
	for _, scenario := range []string{"wrong_revision", "hard_failure", "valid_then_changed_scope"} {
		t.Run(scenario, func(t *testing.T) {
			service, _, _, _, previous, _ := refreshPreparationFixture(t)
			for i := range previous.Files {
				previous.Files[i].File.ContentVersionID = "old-version"
			}
			source := &extractionSource{preparedSource: &preparedSource{snapshotSource: &snapshotSource{}}}
			service.Sources = &preparedSources{source: source}
			journal := &memoryRefreshJournal{}
			service.Journal = journal
			tasks := &extractionTasks{journal: journal, t: t}
			service.Tasks = tasks
			ctx := context.Background()
			batch, err := service.Begin(ctx, "base", "review-batch", "worker")
			if err != nil || batch.State != "attention" || batch.Reason != "batch_review_required" || tasks.starts != 0 || source.holds != 0 {
				t.Fatal("soft gate ran an unapproved batch", err)
			}
			revision := batch.Revision
			if scenario == "wrong_revision" {
				revision++
			}
			if scenario == "hard_failure" {
				journal.value.Reason = "invalid_extraction"
			}
			err = service.ReleaseBatch(ctx, "base", "worker", revision, interfaces.AccountInfo{ID: "owner", Type: "user"})
			if scenario != "valid_then_changed_scope" {
				if err == nil || journal.value.State != "attention" {
					t.Fatal("invalid release passed")
				}
				return
			}
			if err != nil || !approvedBatch(journal.value) {
				t.Fatal("exact reviewed scope not approved", err)
			}
			journal.value.Pending.Build.Files[0].File.ContentVersionID = "different-version"
			if approvedBatch(journal.value) {
				t.Fatal("approval applied to another content version")
			}
			if err := service.ExtractStep(ctx, "base", "worker"); err != nil {
				t.Fatal(err)
			}
			if tasks.starts != 0 || source.holds != 0 || journal.value.State != "attention" {
				t.Fatal("changed scope reached an external action")
			}
		})
	}
}
