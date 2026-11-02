// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"go.uber.org/mock/gomock"
)

type referenceMemory struct {
	record *interfaces.KBReference
	fail   bool
}

func (m *referenceMemory) Get(context.Context, string, string) (*interfaces.KBReference, error) {
	if m.record == nil {
		return nil, nil
	}
	raw, _ := json.Marshal(m.record)
	var copy interfaces.KBReference
	_ = json.Unmarshal(raw, &copy)
	return &copy, nil
}
func (m *referenceMemory) Put(_ context.Context, next *interfaces.KBReference, revision int64) (bool, error) {
	if m.fail {
		return false, nil
	}
	if m.record != nil && m.record.Revision != revision {
		return false, nil
	}
	raw, _ := json.Marshal(next)
	_ = json.Unmarshal(raw, &m.record)
	return true, nil
}

type referenceData struct {
	rows   map[string]map[string]any
	writes int
}

func (*referenceData) Catalog(context.Context, string) (string, error)           { return "catalog", nil }
func (*referenceData) Ensure(context.Context, interfaces.DraftDatasetSpec) error { return nil }
func (d *referenceData) Replace(_ context.Context, spec interfaces.DraftDatasetSpec, row map[string]any) error {
	d.writes++
	raw, _ := json.Marshal(row)
	var stored map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&stored)
	d.rows[spec.ResourceID+"/"+row["_id"].(string)] = stored
	return nil
}
func (d *referenceData) ReadRow(_ context.Context, spec interfaces.DraftDatasetSpec, id string) (map[string]any, error) {
	row := d.rows[spec.ResourceID+"/"+id]
	if row == nil {
		return nil, errors.New("missing reference")
	}
	return row, nil
}

type referenceSource struct {
	serviceSource
	input *kb.FileInput
}

func (s *referenceSource) ReadBlocks(ctx context.Context, ref kb.SampleVersion, _ kb.PageQuery) (kb.BlockPage, error) {
	if kb.OperatorAuthorization(ctx) != "Bearer operator-token" {
		return kb.BlockPage{}, &kb.SourceError{StatusCode: 403}
	}
	return kb.BlockPage{Entries: s.input.Blocks}, s.err
}
func (s *referenceSource) ReadMedia(context.Context, kb.SampleVersion, kb.PageQuery) (kb.MediaPage, error) {
	return kb.MediaPage{Entries: s.input.Media}, s.err
}
func (s *referenceSource) ReadText(_ context.Context, _ kb.SampleVersion, span kb.Span) (string, error) {
	for _, block := range s.input.Blocks {
		if block.BlockID == span.BlockID {
			return string([]rune(s.input.Text[span.BlockID])[span.Start-block.Start : span.End-block.Start]), s.err
		}
	}
	return "", ErrInvalidBuild
}
func referenceServiceFixture(t *testing.T) (*ReferenceService, *referenceMemory, *referenceData, *interfaces.KBBuild, *interfaces.Draft, ReferenceRequest) {
	t.Helper()
	build, draft, input, task := extractionFixture(t, "contract", false)
	build.Files[0].File.SampleRole = "acceptance"
	build.Mode = "acceptance"
	selection := &interfaces.KBSelection{ID: build.SelectionID, ProjectID: build.ProjectID, State: "active", Revision: build.SelectionRevision, Owner: build.Owner, Files: []interfaces.KBResourceFile{build.Files[0].File}}
	ctrl := gomock.NewController(t)
	projects := pmock.NewMockProjectService(ctrl)
	projects.EXPECT().GetProjectForStatus(gomock.Any(), "Bearer operator-token", "project", build.Owner).Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "project", KNID: build.NetworkID, Owner: build.Owner}}, false, nil).AnyTimes()
	drafts := pmock.NewMockDraftAccess(ctrl)
	drafts.EXPECT().GetDraft(gomock.Any(), "project").Return(draft, true, nil).AnyTimes()
	selections := pmock.NewMockKBResourceAccess(ctrl)
	selections.EXPECT().Read(gomock.Any(), "project").Return(&interfaces.KBResourceState{Current: selection}, nil).AnyTimes()
	source := &referenceSource{input: input}
	queue := &serviceQueue{}
	base := &BuildService{Projects: projects, Drafts: drafts, Selections: selections, Store: queue, Runtime: &fileRuntime{}, Source: source}
	store := &referenceMemory{}
	data := &referenceData{rows: map[string]map[string]any{}}
	service := &ReferenceService{Builds: base, Store: store, Datasets: data}
	proposal, _ := parseFileExtraction(task.Output)
	req := ReferenceRequest{SelectionID: build.SelectionID, SelectionRevision: build.SelectionRevision, DraftUpdateTime: build.DraftUpdateTime, FileKey: build.Files[0].File.Key(), ConfirmComplete: true, Extraction: *proposal}
	return service, store, data, build, draft, req
}
func TestBlindReferenceWorkflowStoresOnlyLocationsAndHumanDecisionPointers(t *testing.T) {
	s, store, data, build, _, request := referenceServiceFixture(t)
	if err := s.Ready(context.Background(), build); !errors.Is(err, ErrAcceptanceRequired) {
		t.Fatal("unreviewed references allowed heldout inference", err)
	}
	status, err := s.Save(context.Background(), "Bearer operator-token", "project", build.Owner, request)
	if err != nil || status.Revision != 1 || !status.Files[0].Reviewed {
		t.Fatal("reference was not confirmed", status, err)
	}
	// A lost acknowledgement reuses the exact same reference, not a new revision.
	status, err = s.Save(context.Background(), "Bearer operator-token", "project", build.Owner, request)
	if err != nil || status.Revision != 1 || data.writes != 1 {
		t.Fatal("duplicate review acknowledgement created another write", err, data.writes)
	}
	raw, _ := json.Marshal(data.rows)
	if strings.Contains(string(raw), "Reference ABC. Total 25.50.") {
		t.Fatal("disabled source quote entered reference Dataset")
	}
	process, _ := json.Marshal(store.record)
	if strings.Contains(string(process), `"extraction":`) || strings.Contains(string(process), "claim_value") || strings.Contains(string(process), "Reference ABC.") {
		t.Fatal("reference body entered process DB")
	}
	view, err := s.View(context.Background(), "Bearer operator-token", "project", request.FileKey, build.Owner)
	if err != nil || view.Extraction == nil || view.Extraction.Records[0].Anchor.Quote != request.Extraction.Records[0].Anchor.Quote {
		t.Fatal("blind reviewer could not load independently confirmed source references", err)
	}
	if err = s.Ready(context.Background(), build); err != nil || build.ReferenceRevision != 1 {
		t.Fatal("confirmed reference revision was not pinned", err)
	}
}
func TestReferenceCASFailureNeverMarksAnUncommittedAnnotationAsReviewed(t *testing.T) {
	s, store, _, build, _, request := referenceServiceFixture(t)
	store.fail = true
	if _, err := s.Save(context.Background(), "Bearer operator-token", "project", build.Owner, request); !errors.Is(err, ErrBuildChanged) {
		t.Fatal("lost review CAS was hidden", err)
	}
	if store.record != nil {
		t.Fatal("orphan Dataset write became a confirmed reference")
	}
}
func TestBlindReferenceDoesNotAcceptPartialOrWrongSourceAnnotations(t *testing.T) {
	for _, scenario := range []string{"not_confirmed", "debug_file", "changed_draft", "bad_quote"} {
		t.Run(scenario, func(t *testing.T) {
			s, store, data, build, _, request := referenceServiceFixture(t)
			switch scenario {
			case "not_confirmed":
				request.ConfirmComplete = false
			case "debug_file":
				request.FileKey = "1/not-heldout"
			case "changed_draft":
				request.DraftUpdateTime++
			case "bad_quote":
				request.Extraction.Records[0].Anchor.Quote = "invented"
			}
			if _, err := s.Save(context.Background(), "Bearer operator-token", "project", build.Owner, request); err == nil || store.record != nil || data.writes != 0 {
				t.Fatal("invalid reference changed storage", err)
			}
		})
	}
}

func TestChangedReferenceStopsHeldoutWorkBeforeAnotherSubmission(t *testing.T) {
	s, store, _, build, draft, request := referenceServiceFixture(t)
	if _, err := s.Save(context.Background(), "Bearer operator-token", "project", build.Owner, request); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(context.Background(), build); err != nil {
		t.Fatal(err)
	}
	store.record.Revision++
	process := &processMemory{active: true, record: &interfaces.KBBuildRecord{Build: build, Identity: interfaces.KBBuildIdentity{GrantID: "grant", PrincipalID: "job-app", Revision: 1}}}
	runtime := &fileRuntime{}
	coordinator := &Coordinator{Store: process, Drafts: buildDraft{draft}, Runtime: runtime, Records: &recordSink{rows: map[string]map[string]any{}}, Assessor: s}
	step(t, coordinator)
	if runtime.starts != 0 || runtime.checks != 0 || process.record.Build.State != "cancelled" || !process.record.Identity.Cleaned {
		t.Fatal("changed references allowed heldout work")
	}
}

func TestFullBuildPinsActualExecutionFromThePassingAcceptance(t *testing.T) {
	s, _, _, build, _, request := referenceServiceFixture(t)
	if _, err := s.Save(context.Background(), "Bearer operator-token", "project", build.Owner, request); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(context.Background(), build); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(build)
	var candidate interfaces.KBBuild
	if err := json.Unmarshal(raw, &candidate); err != nil {
		t.Fatal(err)
	}
	prompt := 2
	candidate.State = "succeeded"
	candidate.PromptVersion, candidate.ModelID, candidate.Model = &prompt, "accepted-model", "accepted-provider-model"
	file := &candidate.Files[0]
	file.State, file.AgentTaskID = "succeeded", "accepted-task"
	version := candidate.AgentVersion
	file.AgentVersion, file.PromptVersion, file.ModelID = &version, &prompt, candidate.ModelID
	file.InputDigest, file.RowsDigest = strings.Repeat("a", 64), strings.Repeat("b", 64)
	file.Assessment = &interfaces.KBAcceptanceCounts{Expected: 10, Predicted: 10, Correct: 10, ExpectedLinks: 10, PredictedLinks: 10, CorrectLinks: 10}
	s.Builds.Store.(*serviceQueue).record = &interfaces.KBBuildRecord{Build: &candidate}
	build.Mode = "full"
	if err := s.FullReady(context.Background(), build); err != nil {
		t.Fatal(err)
	}
	if build.PromptVersion == nil || *build.PromptVersion != prompt || build.ModelID != candidate.ModelID || build.Model != candidate.Model {
		t.Fatal("passing acceptance did not pin the actual full-build execution")
	}
	// A changed default model must be detected on the first receipt, before any
	// output is validated or written, even when the Agent version is unchanged.
	build.State, build.Files[0].State = "running", "submitting"
	task := &interfaces.BknAgentTask{TaskID: "full-task", AgentID: kb.DocumentExtractionAgentID, AgentVersion: &version, PromptVersion: &prompt, ModelID: "changed-default", Status: "running"}
	if _, err := AttachFileTask(build, 0, kb.DocumentExtractionAgentID, task, build.UpdateTime+1); err == nil {
		t.Fatal("a changed default model reused acceptance from a different model")
	}
}
