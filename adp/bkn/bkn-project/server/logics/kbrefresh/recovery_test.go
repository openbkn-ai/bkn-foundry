// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
	"time"
)

type interruptedRefreshSource struct {
	*preparedSources
	unavailable bool
	failure     error
}

func (s *interruptedRefreshSource) OpenPublished(ctx context.Context, p *interfaces.KBPublication, guard func(context.Context) error) (kb.PublishedSource, error) {
	if s.unavailable {
		if s.failure != nil {
			return nil, s.failure
		}
		return nil, kb.ErrUnavailable
	}
	return s.preparedSources.OpenPublished(ctx, p, guard)
}

type interruptedRefreshWrites struct {
	*applicationStorage
	unavailable bool
}

func (s *interruptedRefreshWrites) Replace(ctx context.Context, spec interfaces.DraftDatasetSpec, row map[string]any) error {
	if err := s.applicationStorage.Replace(ctx, spec, row); err != nil {
		return err
	}
	if s.unavailable {
		return kb.ErrUnavailable
	}
	return nil
}
func readyRefreshForRecovery(t *testing.T) (*Service, *memoryRefreshJournal, *interruptedRefreshSource, *interruptedRefreshWrites, *extractionTasks) {
	t.Helper()
	service, _, _, storage, previous, _ := refreshPreparationFixture(t)
	for i := range previous.Files {
		previous.Files[i].File.ContentVersionID = "old-version"
	}
	source := &interruptedRefreshSource{preparedSources: &preparedSources{source: &extractionSource{preparedSource: &preparedSource{snapshotSource: &snapshotSource{}}}}}
	service.Sources = source
	journal := &memoryRefreshJournal{}
	service.Journal = journal
	tasks := &extractionTasks{journal: journal, t: t, succeeded: true}
	service.Tasks = tasks
	data := &interruptedRefreshWrites{applicationStorage: &applicationStorage{fixedModelStorage: storage, rows: map[string]map[string]map[string]any{}}}
	service.Storage = data
	ctx := context.Background()
	beginReviewed(t, service, ctx, "base", "batch", "worker")
	for n := 0; n < 12 && journal.value.State != "ready"; n++ {
		if err := service.ExtractStep(ctx, "base", "worker"); err != nil {
			t.Fatal(err)
		}
	}
	if journal.value.State != "ready" {
		t.Fatal("fixture not ready")
	}
	plan, err := service.application(ctx, journal.value)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.BeginApply(ctx, "base", "worker", plan.staged.Digest, plan.fusion.Preview.Signature); err != nil {
		t.Fatal(err)
	}
	return service, journal, source, data, tasks
}
func TestApplicationRecoveryRetainsIntentAcrossRepeatedFailures(t *testing.T) {
	for _, phase := range []string{"applying_source", "applying_revoked", "applying_binding", "applying_write", "finishing"} {
		t.Run(phase, func(t *testing.T) {
			service, journal, source, data, tasks := readyRefreshForRecovery(t)
			ctx := context.Background()
			if phase == "finishing" {
				for n := 0; n < 30 && journal.value.State != "finishing"; n++ {
					if err := service.ApplyStep(ctx, "base", "worker"); err != nil {
						t.Fatal(err)
					}
				}
				if journal.value.State != "finishing" {
					t.Fatal("fixture did not complete Dataset writes")
				}
			}
			digest := journal.value.Pending.ApplicationDigest
			priorWrites := data.writes
			if phase == "applying_revoked" {
				source.failure = &kb.SourceError{StatusCode: 403}
			}
			if phase == "applying_binding" {
				source.failure = kb.ErrPublishedBindingsChanged
			}
			if phase == "applying_write" {
				data.unavailable = true
			} else {
				source.unavailable = true
			}
			expected := "applying"
			if phase == "finishing" {
				expected = "finishing"
			}
			for n := 0; n < 4; n++ {
				journal.value.NextCheck = 0
				_ = service.ApplyStep(ctx, "base", "worker")
				if journal.value.State != expected || journal.value.Pending.ApplicationDigest != digest || journal.value.NextCheck <= time.Now().UnixMilli() {
					t.Fatal("failure abandoned its recoverable phase or write intent")
				}
			}
			if phase != "applying_write" && data.writes != priorWrites {
				t.Fatal("failed permission/model guard allowed a Dataset write")
			}
			source.unavailable, data.unavailable = false, false
			for n := 0; n < 40 && journal.value.State != "idle"; n++ {
				journal.value.NextCheck = 0
				if err := service.ApplyStep(ctx, "base", "worker"); err != nil {
					t.Fatal(err)
				}
			}
			if journal.value.State != "idle" || journal.value.Pending != nil || tasks.starts != 2 {
				t.Fatal("original batch failed to recover or inference repeated")
			}
		})
	}
}
func TestIncompleteDirectoryScanRetriesWithoutAReviewDecision(t *testing.T) {
	for _, scenario := range []string{"unparsed", "changed"} {
		t.Run(scenario, func(t *testing.T) {
			service, _, _, _, _, sources := refreshPreparationFixture(t)
			snapshot := sources.source.(*preparedSource).snapshotSource
			snapshot.scenario = scenario
			journal := &memoryRefreshJournal{}
			service.Journal = journal
			_, err := service.Begin(context.Background(), "base", "batch", "worker")
			if err == nil || journal.value.State != "idle" || journal.value.Pending != nil || journal.value.NextCheck <= time.Now().UnixMilli() {
				t.Fatal("temporary source state permanently paused refresh")
			}
			snapshot.scenario = "stable"
			journal.value.NextCheck = 0
			next, err := service.Begin(context.Background(), "base", "retry", "worker")
			if err != nil || next.State != "idle" || next.Reason != "" || next.Pending != nil {
				t.Fatal("completed parsing did not recover automatically", err)
			}
		})
	}
}

func TestTemporaryScanClassificationDoesNotRetryPermissionAsAnEmptySource(t *testing.T) {
	for _, status := range []int{403, 404, 410, 429, 500, 503} {
		if temporaryScanFailure(&kb.SourceError{StatusCode: status}) != (status == 429 || status >= 500) {
			t.Fatal("incorrect source retry classification", status)
		}
	}
}
