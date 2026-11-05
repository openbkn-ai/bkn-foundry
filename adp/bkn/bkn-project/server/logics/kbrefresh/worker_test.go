// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
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

type failingPolls struct {
	*extractionTasks
	err error
}

func (a *failingPolls) Poll(ctx context.Context, owner interfaces.AccountInfo, id string) (*interfaces.BknAgentTask, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.extractionTasks.Poll(ctx, owner, id)
}

func TestRefreshTaskPastItsDeadlineStopsWaiting(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{{"still running", nil}, {"task service gone", errors.New("refused")}} {
		t.Run(tc.name, func(t *testing.T) {
			service, _, _, _, previous, _ := refreshPreparationFixture(t)
			for i := range previous.Files {
				previous.Files[i].File.ContentVersionID = "old-version"
			}
			service.Sources = &preparedSources{source: &extractionSource{preparedSource: &preparedSource{snapshotSource: &snapshotSource{}}}}
			journal := &memoryRefreshJournal{}
			service.Journal = journal
			tasks := &failingPolls{extractionTasks: &extractionTasks{journal: journal, t: t}}
			service.Tasks = tasks
			ctx := context.Background()
			beginReviewed(t, service, ctx, "base", "batch", "worker")
			for n := 0; n < 2; n++ {
				if err := service.ExtractStep(ctx, "base", "worker"); err != nil {
					t.Fatal(err)
				}
			}
			index := journal.value.Pending.Changed[0]
			file := &journal.value.Pending.Build.Files[index]
			if file.State != "waiting" || file.SubmittedAt == 0 {
				t.Fatalf("fixture file %+v", file)
			}
			file.SubmittedAt = time.Now().Add(-kbinstance.AgentTaskDeadline() - time.Second).UnixMilli()
			tasks.err = tc.err
			if err := service.ExtractStep(ctx, "base", "worker"); err != nil {
				t.Fatal(err)
			}
			if journal.value.State != "attention" || journal.value.Reason != kbinstance.ReasonExtractionTimedOut || journal.value.Pending.Build.Files[index].State != "failed" {
				t.Fatalf("state %s reason %s", journal.value.State, journal.value.Reason)
			}
		})
	}
}

func TestRefreshProgressIgnoresPollsThatChangeNothing(t *testing.T) {
	record := &interfaces.KBRefreshRecord{State: "running", Pending: &interfaces.KBRefreshBatch{Build: &interfaces.KBBuild{State: "running", Files: []interfaces.KBFileBuild{{State: "waiting"}}}}}
	record.MarkProgress(100)
	record.MarkProgress(200)
	if record.ProgressTime != 100 {
		t.Fatalf("progress %d", record.ProgressTime)
	}
	record.Pending.Build.Files[0].State = "validated"
	record.MarkProgress(300)
	if record.ProgressTime != 300 {
		t.Fatalf("progress %d", record.ProgressTime)
	}
	record.Pending.ApplyIndex = 5
	record.MarkProgress(400)
	if record.ProgressTime != 400 {
		t.Fatalf("progress %d", record.ProgressTime)
	}
}
