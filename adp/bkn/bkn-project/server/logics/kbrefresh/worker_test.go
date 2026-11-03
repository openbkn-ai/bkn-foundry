// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"testing"
)

func TestRefreshWorkerMakesPreApplyFailureVisibleWithoutRepeatingInference(t *testing.T) {
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
	ctx := context.Background()
	beginReviewed(t, service, ctx, "base", "batch", "worker")
	for n := 0; n < 12 && journal.value.State != "ready"; n++ {
		if err := service.ExtractStep(ctx, "base", "worker"); err != nil {
			t.Fatal(err)
		}
	}
	if journal.value.State != "ready" || tasks.starts != 2 {
		t.Fatal("fixture did not reach validated batch")
	}
	// This provider can read the fixed model but cannot supply the staged K5
	// snapshot. The worker must expose a decision, not loop forever as 'ready'.
	service.Storage = storage
	if err := service.runNetwork(ctx, "released-network", "worker"); err != nil {
		t.Fatal(err)
	}
	if journal.value.State != "attention" || journal.value.Reason != "write_plan_unavailable" || journal.value.Pending.ApplicationDigest != "" {
		t.Fatal("pre-apply failure was not visible and unapplied")
	}
	polls := tasks.polls
	if err := service.runNetwork(ctx, "released-network", "worker"); err != nil {
		t.Fatal(err)
	}
	if tasks.starts != 2 || tasks.polls != polls {
		t.Fatal("paused failure repeated model work")
	}
}
