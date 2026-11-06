// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
	"testing"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/licverify"
)

type datasetDraftAccess struct {
	interfaces.DraftAccess
	current   *interfaces.Draft
	selection *interfaces.KBSelection
	owner     interfaces.AccountInfo
}

func clonePreparationDraft(value *interfaces.Draft) *interfaces.Draft {
	raw, _ := json.Marshal(value)
	result := &interfaces.Draft{}
	_ = json.Unmarshal(raw, result)
	return result
}
func (a *datasetDraftAccess) GetDraft(context.Context, string) (*interfaces.Draft, bool, error) {
	return clonePreparationDraft(a.current), true, nil
}
func (a *datasetDraftAccess) CommitDocumentDraft(_ context.Context, value *interfaces.Draft, guard interfaces.DocumentDraftGuard) (bool, error) {
	if guard.Owner != a.owner || guard.SelectionID != a.selection.ID || guard.SelectionRevision != a.selection.Revision || a.selection.State != "active" || guard.DraftUpdateTime != a.current.UpdateTime {
		return false, nil
	}
	if value.UpdateTime <= a.current.UpdateTime {
		return false, errors.New("non-monotonic guard")
	}
	a.current = clonePreparationDraft(value)
	return true, nil
}

type datasetProjectAccess struct {
	interfaces.ProjectService
	entry *interfaces.ProjectEntry
}

func (a *datasetProjectAccess) GetProject(context.Context, string, string) (*interfaces.ProjectEntry, error) {
	return a.entry, nil
}

type datasetSelectionAccess struct {
	interfaces.KBResourceAccess
	state *interfaces.KBResourceState
}

func (a *datasetSelectionAccess) Read(context.Context, string) (*interfaces.KBResourceState, error) {
	return a.state, nil
}

type datasetSource struct {
	kb.UnavailableSource
	allowed bool
}

func (s *datasetSource) SummarizeFiles(ctx context.Context, _ kb.SourceScope, _ kb.FileQuery) (kb.FileSummary, error) {
	if kb.OperatorAuthorization(ctx) != "Bearer operator" {
		return kb.FileSummary{}, &kb.SourceError{StatusCode: 401}
	}
	if !s.allowed {
		return kb.FileSummary{}, &kb.SourceError{StatusCode: 403}
	}
	return kb.FileSummary{}, nil
}

type datasetProvisioner struct {
	calls  []interfaces.DraftDatasetSpec
	failAt int
	after  func()
}

func (s *datasetProvisioner) Catalog(context.Context, string) (string, error) {
	return "private-catalog", nil
}
func (s *datasetProvisioner) Ensure(_ context.Context, spec interfaces.DraftDatasetSpec) error {
	s.calls = append(s.calls, spec)
	if s.after != nil {
		s.after()
	}
	if len(s.calls) == s.failAt {
		return kb.ErrUnavailable
	}
	return nil
}

func preparedDraftFixture(t *testing.T) (*draftService, *datasetDraftAccess, *datasetSource, *datasetProvisioner, *interfaces.ProjectEntry) {
	t.Helper()
	kb.ResetForTest()
	entitlement.ResetForTest()
	t.Cleanup(func() { kb.ResetForTest(); entitlement.ResetForTest() })
	entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
	owner := interfaces.AccountInfo{ID: "owner", Type: "user"}
	selection := &interfaces.KBSelection{ID: "selected", Revision: 2, State: "active", Owner: owner, Ranges: []interfaces.KBSourceRange{{KnowledgeBaseID: "1"}}}
	object := &interfaces.DraftObjectType{ID: "business_fact", Name: "Business fact", Adopted: true, PrimaryKeys: []string{"_id"}, DisplayKey: "value", Document: &interfaces.DraftDocumentModel{Role: "fact", SelectionID: selection.ID, SelectionRevision: 2}, Properties: []*interfaces.DraftProperty{{Name: "_id", Type: "string", ReadOnly: true}, {Name: "value", Type: "string", Extraction: &interfaces.DraftExtractionField{Instruction: "Extract the explicit business fact"}}}}
	access := &datasetDraftAccess{current: &interfaces.Draft{ProjectID: "p", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: 10, Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{object}}}, selection: selection, owner: owner}
	entry := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: owner}}
	source := &datasetSource{allowed: true}
	storage := &datasetProvisioner{}
	kb.Register(kb.Providers{Source: source, Concepts: kb.UnavailableConcepts{}, Datasets: storage})
	service := &draftService{da: access, kbResources: &datasetSelectionAccess{state: &interfaces.KBResourceState{Current: selection}}, ps: &datasetProjectAccess{entry: entry}}
	return service, access, source, storage, entry
}

func TestDocumentPreparationPersistsBindingsAndReusesVerifiedGeneration(t *testing.T) {
	service, access, _, storage, entry := preparedDraftFixture(t)
	value, err := service.prepareDocumentDatasets(context.Background(), "Bearer operator", entry, clonePreparationDraft(access.current), entry.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if value.Items.ObjectTypes[0].Table.ResourceID == "" || value.Items.ObjectTypes[0].Properties[0].MappedField != "bkn_record_id" || access.current.Items.DatasetPlans[0].State != "ready" {
		t.Fatal("ready binding was not persisted")
	}
	id := value.Items.ObjectTypes[0].Table.ResourceID
	again, err := service.prepareDocumentDatasets(context.Background(), "Bearer operator", entry, clonePreparationDraft(access.current), entry.Owner)
	if err != nil || again.Items.ObjectTypes[0].Table.ResourceID != id || len(storage.calls) != 2 {
		t.Fatal("repeat confirmation changed generation or skipped verification", err)
	}
}

func TestDocumentPreparationRejectsLateSourceDraftAndPermissionChanges(t *testing.T) {
	for _, reason := range []string{"selection", "draft", "permission"} {
		t.Run(reason, func(t *testing.T) {
			service, access, source, storage, entry := preparedDraftFixture(t)
			storage.after = func() {
				switch reason {
				case "selection":
					access.selection.Revision++
				case "draft":
					access.current.UpdateTime++
				case "permission":
					source.allowed = false
				}
			}
			_, err := service.prepareDocumentDatasets(context.Background(), "Bearer operator", entry, clonePreparationDraft(access.current), entry.Owner)
			var problem *httperrors.HTTPError
			expected := 409
			if reason == "permission" {
				expected = 403
			}
			if !errors.As(err, &problem) || problem.HTTPCode != expected {
				t.Fatalf("incorrect refusal for %s: %v", reason, err)
			}
			if access.current.Items.ObjectTypes[0].Table.ResourceID != "" {
				t.Fatal("late binding persisted")
			}
		})
	}
}

func TestPreparationSummaryRejectsOldModelAndSourceProgress(t *testing.T) {
	_, access, _, _, _ := preparedDraftFixture(t)
	draft := clonePreparationDraft(access.current)
	draft.UpdateTime = time.Now().UnixMilli()
	plan, err := kbdataset.NewPlan(draft, "generation", "catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	plan.State = "preparing"
	plan.Datasets[0].Ready = true
	draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
	got := datasetPreparation(draft, access.selection)
	if got.State != "preparing" || got.Ready != 1 || got.Total != 1 {
		t.Fatal(got)
	}
	draft.UpdateTime -= int64(11 * time.Minute / time.Millisecond)
	if datasetPreparation(draft, access.selection).State != "failed" {
		t.Fatal("interrupted request keeps polling")
	}
	draft.Items.ObjectTypes[0].Name = "Changed model"
	if datasetPreparation(draft, access.selection).State != "stale" {
		t.Fatal("old model appears ready")
	}
	draft.Items.ObjectTypes[0].Name = "Business fact"
	access.selection.Owner.ID = "new-owner"
	if datasetPreparation(draft, access.selection).State != "stale" {
		t.Fatal("old owner's preparation appears ready")
	}
	access.selection.Owner.ID = "owner"
	access.selection.Revision++
	if datasetPreparation(draft, access.selection).State != "stale" {
		t.Fatal("old source appears ready")
	}
}

func TestCompleteEvidenceGraphCompilesWithResourceAndOccurrenceDatasets(t *testing.T) {
	_, access, _, _, _ := preparedDraftFixture(t)
	draft := clonePreparationDraft(access.current)
	input := &kbconcept.SampleInput{SelectionID: access.selection.ID, SelectionRevision: access.selection.Revision, Request: kb.ConceptRequest{NamingLanguage: "zh-CN"}}
	evidence, err := kbconcept.BuildEvidence(input, draft.Items)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range evidence.Model.ObjectTypes {
		object.Adopted = true
	}
	for _, relation := range evidence.Model.RelationTypes {
		relation.Adopted = true
	}
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, evidence.Model.ObjectTypes...)
	draft.Items.RelationTypes = append(draft.Items.RelationTypes, evidence.Model.RelationTypes...)
	plan, err := kbdataset.NewPlan(draft, "generation", "catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := kbdataset.BindPreview(draft, plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compileDraft(&bound.Items, nil); err != nil {
		t.Fatal(err)
	}
	if evidence.MediaID == evidence.ImageID {
		t.Fatal("resource and occurrence conflated")
	}
	indirect := 0
	for _, relation := range bound.Items.RelationTypes {
		if relation.LinkTable != nil {
			indirect++
		}
	}
	if indirect != 5 || len(plan.Datasets) != 10 {
		t.Fatalf("incomplete evidence graph: %d edges, %d datasets", indirect, len(plan.Datasets))
	}
}
