// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

type cleanupSources struct {
	*preparedSources
	released []string
	networks []string
	fail     bool
}

func (s *cleanupSources) ReleasePublishedHold(ctx context.Context, publication *interfaces.KBPublication, hold interfaces.KBHoldGroup, guard func(context.Context) error) error {
	if err := guard(ctx); err != nil {
		return err
	}
	s.released = append(s.released, hold.ID)
	s.networks = append(s.networks, publication.NetworkID)
	if s.fail {
		s.fail = false
		return errors.New("release acknowledgement lost")
	}
	return nil
}

var _ kb.PublishedHoldCleanup = (*cleanupSources)(nil)

func TestDiscardRetainsPreviousDataAndResumesUnknownHoldRelease(t *testing.T) {
	ctx := context.Background()
	service, releases, _, _, previous, original := refreshPreparationFixture(t)
	for i := range previous.Files {
		previous.Files[i].File.ContentVersionID = "old-version"
	}
	sources := &cleanupSources{preparedSources: original, fail: true}
	service.Sources = sources
	journal := &memoryRefreshJournal{}
	service.Journal = journal
	queued, err := service.Begin(ctx, "base", "batch", "worker")
	if err != nil || queued.State != "attention" {
		t.Fatal("expected paused batch", err)
	}
	prior := journal.value.Previous.ID
	// Switching the consumer or losing source-read access must not prevent
	// releasing this unapplied batch's protected UUIDs.
	releases.sw.ToNetworkID = "another-network"
	if err := service.DiscardBatch(ctx, "base", "worker", queued.Revision, interfaces.AccountInfo{ID: "owner", Type: "user"}); err != nil {
		t.Fatal(err)
	}
	if err := service.DiscardStep(ctx, "base", "worker"); err == nil {
		t.Fatal("lost acknowledgement was hidden")
	}
	if journal.value.Pending.Holds[0].Released {
		t.Fatal("unknown release was marked acknowledged")
	}
	for n := 0; n < 3 && journal.value.Pending != nil; n++ {
		if err := service.DiscardStep(ctx, "base", "worker"); err != nil {
			t.Fatal(err)
		}
	}
	if journal.value.Pending != nil || journal.value.Previous.ID != prior || journal.value.State != "idle" || len(sources.released) != 2 || sources.released[0] != sources.released[1] {
		t.Fatal("discard changed prior data or failed idempotent recovery")
	}
}
func TestDiscardNeverAbandonsStartedApplication(t *testing.T) {
	service, _, _, _, previous, _ := refreshPreparationFixture(t)
	for i := range previous.Files {
		previous.Files[i].File.ContentVersionID = "old-version"
	}
	journal := &memoryRefreshJournal{}
	service.Journal = journal
	queued, err := service.Begin(context.Background(), "base", "batch", "worker")
	if err != nil {
		t.Fatal(err)
	}
	journal.value.Pending.ApplicationDigest = "possibly-written"
	if err := service.DiscardBatch(context.Background(), "base", "worker", queued.Revision, interfaces.AccountInfo{ID: "owner", Type: "user"}); err == nil {
		t.Fatal("possibly applied batch discarded")
	}
	if journal.value.State != "attention" || journal.value.Pending.ApplicationDigest != "possibly-written" {
		t.Fatal("application intent changed")
	}
}

func TestDiscardRequiresAnExplicitDecisionBeforeAnotherInferenceBatch(t *testing.T) {
	ctx := context.Background()
	service, _, _, _, previous, sources := refreshPreparationFixture(t)
	duration := int64(100)
	for i := range previous.Files {
		previous.Files[i].File.ContentVersionID = "old-version"
		previous.Files[i].File.ChunkCount = 1
		previous.Files[i].DurationMillis = &duration
	}
	service.Limits = &Limits{MaxChangedPercent: 100, MaxRemovedPercent: 100, MaxDurationMillis: 600000}
	service.Sources = &cleanupSources{preparedSources: sources}
	journal := &memoryRefreshJournal{}
	service.Journal = journal
	tasks := &extractionTasks{journal: journal, t: t}
	service.Tasks = tasks
	initial, err := service.Begin(ctx, "base", "first-batch", "worker")
	if err != nil || initial.State != "running" {
		t.Fatal("fixture should pass ordinary automatic limits", err)
	}
	journal.value.State, journal.value.Reason = "attention", "submission_unknown"
	if err := service.DiscardBatch(ctx, "base", "worker", journal.value.Revision, interfaces.AccountInfo{ID: "owner", Type: "user"}); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3 && journal.value.Pending != nil; n++ {
		if err := service.DiscardStep(ctx, "base", "worker"); err != nil {
			t.Fatal(err)
		}
	}
	next, err := service.Begin(ctx, "base", "next-batch", "worker")
	if err != nil {
		t.Fatal(err)
	}
	if next.State != "attention" || next.Reason != "batch_review_required" || len(next.Pending.Estimate.Reasons) != 1 || next.Pending.Estimate.Reasons[0] != "previous_batch_discarded" {
		t.Fatal("discard silently authorized another extraction")
	}
	if err := service.runNetwork(ctx, "released-network", "worker"); err != nil {
		t.Fatal(err)
	}
	if tasks.starts != 0 {
		t.Fatal("model inference repeated without a new review decision")
	}
}
