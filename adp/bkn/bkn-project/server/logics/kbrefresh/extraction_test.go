// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"encoding/json"
	"errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
	"strings"
	"testing"
)

type extractionSource struct {
	*preparedSource
	holds    int
	releases int
}

func (s *extractionSource) Hold(context.Context, kb.SourceScope, kb.HoldRequest) error {
	s.holds++
	return nil
}
func (s *extractionSource) ReleaseHold(context.Context, kb.SourceScope, string) error {
	s.releases++
	return nil
}

func (s *extractionSource) ReadBlocks(_ context.Context, ref kb.SampleVersion, _ kb.PageQuery) (kb.BlockPage, error) {
	return kb.BlockPage{Entries: []kb.EvidenceBlock{{SampleVersion: ref, BlockID: "block", Ordinal: 0, Start: 0, End: 5}}}, nil
}
func (s *extractionSource) ReadMedia(context.Context, kb.SampleVersion, kb.PageQuery) (kb.MediaPage, error) {
	return kb.MediaPage{Entries: []kb.EvidenceMedia{}}, nil
}
func (s *extractionSource) ReadText(context.Context, kb.SampleVersion, kb.Span) (string, error) {
	return "asset", nil
}

type extractionTasks struct {
	journal       *memoryRefreshJournal
	starts, polls int
	unknown       bool
	succeeded     bool
	invalidOutput bool
	t             *testing.T
}

func (a *extractionTasks) Start(_ context.Context, caller interfaces.AccountInfo, build *interfaces.KBBuild, index int, _ *interfaces.Draft, input *kb.FileInput) (*interfaces.BknAgentTask, error) {
	a.starts++
	file := a.journal.value.Pending.Build.Files[index]
	if file.State != "submitting" || len(file.InputDigest) != 64 || build.AgentVersion != 3 || caller != build.Owner || input.Text["block"] != "asset" {
		a.t.Error("submission occurred before its fixed write-ahead intent")
	}
	if a.unknown {
		return nil, errors.New("acknowledgement lost")
	}
	version, prompt := 3, 2
	return &interfaces.BknAgentTask{AgentID: kb.DocumentExtractionAgentID, TaskID: map[bool]string{true: "known-task", false: "known-task-two"}[index == 0], AgentVersion: &version, PromptVersion: &prompt, ModelID: "fixed-model-id", Model: "fixed-model", Status: interfaces.BKN_AGENT_TASK_PENDING}, nil
}
func (a *extractionTasks) Poll(_ context.Context, _ interfaces.AccountInfo, id string) (*interfaces.BknAgentTask, error) {
	a.polls++
	version, prompt := 3, 2
	if a.succeeded {
		value := "asset"
		if a.invalidOutput {
			value = "unsupported"
		}
		output, _ := json.Marshal(kbinstance.FileExtraction{Records: []kbinstance.InstanceRecord{{Reference: "current", ObjectTypeID: "asset_doc", Anchor: kbinstance.InstanceCitation{BlockID: "block", Quote: "asset"}, Values: map[string]kbinstance.InstanceValue{"number": {Value: value, Evidence: []kbinstance.InstanceCitation{{BlockID: "block", Quote: value}}}}}}, Relations: []kbinstance.InstanceRelation{}})
		return &interfaces.BknAgentTask{AgentID: kb.DocumentExtractionAgentID, TaskID: id, AgentVersion: &version, PromptVersion: &prompt, ModelID: "fixed-model-id", Model: "fixed-model", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, CreateTime: 1234, UpdateTime: 1734, Output: string(output)}, nil
	}
	return &interfaces.BknAgentTask{AgentID: kb.DocumentExtractionAgentID, TaskID: id, AgentVersion: &version, PromptVersion: &prompt, ModelID: "fixed-model-id", Model: "fixed-model", Status: interfaces.BKN_AGENT_TASK_RUNNING}, nil
}
func TestRefreshExtractionRecoversKnownTaskAndNeverReplaysUnknownSubmission(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "known", true: "unknown"}[unknown], func(t *testing.T) {
			service, _, _, _, previous, _ := refreshPreparationFixture(t)
			for i := range previous.Files {
				previous.Files[i].File.ContentVersionID = "old-version"
			}
			source := &extractionSource{preparedSource: &preparedSource{snapshotSource: &snapshotSource{}}}
			service.Sources = &preparedSources{source: source}
			journal := &memoryRefreshJournal{}
			service.Journal = journal
			tasks := &extractionTasks{journal: journal, unknown: unknown, t: t}
			service.Tasks = tasks
			ctx := context.Background()
			beginReviewed(t, service, ctx, "base", "refresh-batch", "worker")
			if err := service.ExtractStep(ctx, "base", "worker"); err != nil {
				t.Fatal(err)
			}
			if source.holds != 1 || tasks.starts != 0 || journal.value.Pending.Build.State != "queued" {
				t.Fatal("holds and execution were not ordered")
			}
			if err := service.ExtractStep(ctx, "base", "worker"); err != nil {
				t.Fatal(err)
			}
			if tasks.starts != 1 {
				t.Fatal("file not submitted exactly once")
			}
			journal.value.LeaseUntil = 0 // Simulate the crashed worker lease expiring.
			if err := service.ExtractStep(ctx, "base", "replacement-worker"); err != nil {
				t.Fatal(err)
			}
			if unknown {
				if journal.value.State != "attention" || journal.value.Pending.Build.Files[0].State != "submission_unknown" || tasks.starts != 1 || tasks.polls != 0 {
					t.Fatal("unknown submission automatically replayed")
				}
			} else if tasks.starts != 1 || tasks.polls != 1 || journal.value.Pending.Build.Files[0].AgentTaskID != "known-task" {
				t.Fatal("known task was resubmitted instead of polled")
			}
			raw, _ := json.Marshal(journal.value)
			if strings.Contains(string(raw), `"runtime_model"`) || strings.Contains(string(raw), `"output"`) || strings.Contains(string(raw), `"text"`) {
				t.Fatal("private extraction content persisted in process journal")
			}
		})
	}
}

func TestRefreshStagesEveryChangedFileBeforeAnyLiveWriteAndRevalidatesRecovery(t *testing.T) {
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
	beginReviewed(t, service, ctx, "base", "refresh-batch", "worker")
	for step := 0; step < 10 && journal.value.State != "ready"; step++ {
		if err := service.ExtractStep(ctx, "base", "worker"); err != nil {
			t.Fatal(err)
		}
	}
	if journal.value.State != "ready" || tasks.starts != 2 {
		t.Fatalf("batch did not reach validated staging: %s %s starts=%d", journal.value.State, journal.value.Reason, tasks.starts)
	}
	staged, err := service.Stage(ctx, journal.value)
	if err != nil || len(staged.Files) != 2 || len(staged.Writes) == 0 || len(staged.Digest) != 64 {
		t.Fatal("whole batch not materialized", err)
	}
	for _, file := range journal.value.Pending.Build.Files {
		if file.State != "validated" || file.RowsWritten != 0 {
			t.Fatal("staging claimed an acknowledged live write")
		}
	}

	preview := &previewStorage{fixedModelStorage: storage}
	service.Storage = preview
	_, fusion, err := service.StageFusion(ctx, journal.value)
	if err != nil || fusion == nil || len(fusion.FusionWrites) == 0 || preview.writes != 0 || preview.prunes != 0 {
		t.Fatal("fusion did not use the staged view without live mutations", err)
	}
	starts := tasks.starts
	tasks.invalidOutput = true
	if _, err := service.Stage(ctx, journal.value); err == nil || tasks.starts != starts {
		t.Fatal("changed task output was accepted or inference replayed")
	}
}

type previewStorage struct {
	*fixedModelStorage
	writes, prunes int
}

func (s *previewStorage) Replace(context.Context, interfaces.DraftDatasetSpec, map[string]any) error {
	s.writes++
	return errors.New("preview attempted a live write")
}
func (s *previewStorage) ReadFileRows(context.Context, interfaces.DraftDatasetSpec, kb.DatasetFileScope, func(context.Context) error) ([]map[string]any, error) {
	return nil, errors.New("changed staged file fell back to live source rows")
}
func (s *previewStorage) Prune(context.Context, interfaces.DraftDatasetSpec, kb.DatasetFileScope, []string, func(context.Context) error) error {
	s.prunes++
	return errors.New("preview attempted a live prune")
}
func (s *previewStorage) ReadEntityRows(ctx context.Context, _ interfaces.DraftDatasetSpec, _ string, guard func(context.Context) error) ([]map[string]any, error) {
	return []map[string]any{}, guard(ctx)
}
func (s *previewStorage) PruneEntities(context.Context, interfaces.DraftDatasetSpec, string, []string, func(context.Context) error) error {
	s.prunes++
	return errors.New("preview attempted an entity prune")
}
