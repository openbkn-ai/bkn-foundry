// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
	"go.uber.org/mock/gomock"
)

type documentDraftMock struct{ *pmock.MockDraftAccess }

func (m documentDraftMock) CommitDocumentDraft(ctx context.Context, d *interfaces.Draft, g interfaces.DocumentDraftGuard) (bool, error) {
	if g.SelectionID != "lock" || g.SelectionRevision != 7 || g.Owner.ID != "u" || !g.DraftExists {
		return false, nil
	}
	return m.MockDraftAccess.UpdateDraft(ctx, d, g.DraftUpdateTime)
}

type documentSaveSource struct {
	kb.UnavailableSource
	reads int
}

func (s *documentSaveSource) SummarizeFiles(context.Context, kb.SourceScope, kb.FileQuery) (kb.FileSummary, error) {
	return kb.FileSummary{}, nil
}
func (s *documentSaveSource) ReadSampleChunks(context.Context, []kb.SampleVersion, kb.PageQuery) (kb.SamplePage, error) {
	s.reads++
	return kb.SamplePage{Entries: []kb.SampleChunk{{SampleVersion: kb.SampleVersion{KnowledgeBaseID: "1", VersionRef: kb.VersionRef{FileID: "41", ContentVersionID: "8"}}, ChunkID: "1", Text: "Order O-1", Located: "exact", Spans: []kb.Span{{BlockID: "b1", Start: 0, End: 9}}}}}, nil
}

type documentSaveSelection struct {
	interfaces.KBResourceAccess
	changed bool
}

func (s documentSaveSelection) Read(context.Context, string) (*interfaces.KBResourceState, error) {
	revision := int64(7)
	if s.changed {
		revision = 8
	}
	return &interfaces.KBResourceState{Current: &interfaces.KBSelection{ID: "lock", State: "active", Revision: revision, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8", ChunkCount: 1, SampleRole: "debug"}}}}, nil
}
func editableDocumentDraft(t *testing.T) *interfaces.Draft {
	q := interfaces.DraftSourceCitation{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8", BlockID: "b1", Start: 0, End: 9, Quote: "Order O-1"}
	input := &kbconcept.SampleInput{SelectionID: "lock", SelectionRevision: 7, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8"}}, Request: kb.ConceptRequest{NamingLanguage: "en-US", Samples: []kb.Chunk{{Text: q.Quote, Citation: kb.Citation{KnowledgeBaseID: "1", VersionRef: kb.VersionRef{FileID: "41", ContentVersionID: "8"}, BlockID: "b1", Start: 0, End: 9, Quote: q.Quote}}}}}
	object := &interfaces.DraftObjectType{ID: "order_doc", Name: "Order", PrimaryKeys: []string{"number"}, Document: &interfaces.DraftDocumentModel{Role: "entity", Reason: "Explicit order number", Citations: []interfaces.DraftSourceCitation{q}}, Properties: []*interfaces.DraftProperty{{Name: "number", DisplayName: "Number", Type: "string", Extraction: &interfaces.DraftExtractionField{Instruction: "Read the explicit order number", Examples: []interfaces.DraftSourceCitation{q}}}}}
	validated, err := kbconcept.ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{object}})
	if err != nil {
		t.Fatal(err)
	}
	return &interfaces.Draft{ProjectID: "p", Status: "ready", UpdateTime: 10, Items: interfaces.DraftItems{Inputs: &interfaces.DraftInputs{NamingLanguage: "en-US"}, ObjectTypes: validated.ObjectTypes}}
}
func TestSaveDocumentFieldsUsesNativeSamplesAndKeepsAdoptionExplicit(t *testing.T) {
	for _, scenario := range []string{"save", "readonly", "stale_source", "unlicensed", "lose_identity"} {
		t.Run(scenario, func(t *testing.T) {
			kb.ResetForTest()
			entitlement.ResetForTest()
			defer kb.ResetForTest()
			defer entitlement.ResetForTest()
			edition := licverify.EditionProfessional
			if scenario == "unlicensed" {
				edition = licverify.EditionCommunity
			}
			entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: edition} }))
			source := &documentSaveSource{}
			kb.Register(kb.Providers{Source: source, Concepts: kb.UnavailableConcepts{}})
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			da := pmock.NewMockDraftAccess(ctrl)
			ps := pmock.NewMockProjectService(ctrl)
			caller := interfaces.AccountInfo{ID: "u", Type: "user"}
			ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: caller, NamingLanguage: "en-US"}}, nil)
			draft := editableDocumentDraft(t)
			object := draft.Items.ObjectTypes[0]
			if scenario == "lose_identity" {
				object.Adopted = true
			}
			da.EXPECT().GetDraft(gomock.Any(), "p").Return(draft, true, nil)
			save := interfaces.DraftObjectTypeSave{UpdateTime: 10, Name: object.Name, Comment: object.Description, PrimaryKeys: object.PrimaryKeys, DisplayKey: object.DisplayKey}
			for _, p := range object.Properties {
				var extraction *interfaces.DraftExtractionField
				if p.Extraction != nil {
					raw, _ := json.Marshal(p.Extraction)
					extraction = &interfaces.DraftExtractionField{}
					_ = json.Unmarshal(raw, extraction)
					extraction.Instruction = "Read the explicit number, excluding dates"
				}
				save.DataProperties = append(save.DataProperties, interfaces.DraftDataPropertySave{Name: p.Name, DisplayName: p.DisplayName, Comment: p.Description, Type: p.Type, Extraction: extraction})
			}
			if scenario == "readonly" {
				save.DataProperties[1].Type = "integer"
			}
			if scenario == "lose_identity" {
				save.PrimaryKeys = nil
			}
			success := scenario == "save" || scenario == "lose_identity"
			if success {
				da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), int64(10)).Return(true, nil)
			}
			service := &draftService{da: documentDraftMock{da}, ps: ps, kbResources: documentSaveSelection{changed: scenario == "stale_source"}}
			result, err := service.SaveObjectType(context.Background(), "Bearer synthetic", "p", "order_doc", save, caller)
			if success != (err == nil) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if !success {
				return
			}
			saved := result.Items.ObjectTypes[0]
			if saved.Adopted || saved.Properties[0].Extraction.Examples[0].Quote != "" || saved.Document.Citations[0].Quote != "" || source.reads != 1 {
				t.Fatal("adoption, quote policy or source read scope changed")
			}
			if !saved.Properties[1].ReadOnly || saved.Properties[0].Extraction.Instruction != save.DataProperties[0].Extraction.Instruction {
				t.Fatal("field edit or protected columns lost")
			}
			if scenario == "save" && candidatesOf(result, nil, "").Candidates[0].State != "pending" {
				t.Fatal("saving fields implicitly excluded the candidate")
			}
			if scenario == "lose_identity" && !saved.NeedsIdentity {
				t.Fatal("incomplete identity accepted")
			}
		})
	}
}

func TestDocumentIdentityDoesNotQueryATableOrImplicitlyExclude(t *testing.T) {
	kb.ResetForTest()
	entitlement.ResetForTest()
	defer kb.ResetForTest()
	defer entitlement.ResetForTest()
	entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
	source := &documentSaveSource{}
	kb.Register(kb.Providers{Source: source, Concepts: kb.UnavailableConcepts{}})
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	da := pmock.NewMockDraftAccess(ctrl)
	ps := pmock.NewMockProjectService(ctrl)
	caller := interfaces.AccountInfo{ID: "u", Type: "user"}
	ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: caller}}, nil)
	draft := editableDocumentDraft(t)
	draft.Items.ObjectTypes[0].PrimaryKeys = nil
	draft.Items.ObjectTypes[0].NeedsIdentity = true
	da.EXPECT().GetDraft(gomock.Any(), "p").Return(draft, true, nil)
	da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), int64(10)).Return(true, nil)
	service := &draftService{da: documentDraftMock{da}, ps: ps}
	result, err := service.ChooseIdentity(context.Background(), "Bearer synthetic", "p", "order_doc", interfaces.DraftIdentity{UpdateTime: 10, Properties: []string{"number"}}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if result.Items.ObjectTypes[0].NeedsIdentity || result.Items.ObjectTypes[0].Adopted || source.reads != 0 || candidatesOf(result, nil, "").Candidates[0].State != "pending" {
		t.Fatal("document identity performed table work or decided adoption")
	}
}

func TestCreateAndEditDocumentRelationsUseNativeProofWithoutTableJoins(t *testing.T) {
	for _, scenario := range []string{"linking", "same_document", "unknown", "legacy_path"} {
		t.Run(scenario, func(t *testing.T) {
			kb.ResetForTest()
			entitlement.ResetForTest()
			defer kb.ResetForTest()
			defer entitlement.ResetForTest()
			entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
			source := &documentSaveSource{}
			kb.Register(kb.Providers{Source: source, Concepts: kb.UnavailableConcepts{}})
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			da := pmock.NewMockDraftAccess(ctrl)
			ps := pmock.NewMockProjectService(ctrl)
			caller := interfaces.AccountInfo{ID: "u", Type: "user"}
			ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").AnyTimes().Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: caller}}, nil)
			draft := editableDocumentDraft(t)
			draft.Items.ObjectTypes[0].Adopted = true
			raw, _ := json.Marshal(draft.Items.ObjectTypes[0])
			target := &interfaces.DraftObjectType{}
			_ = json.Unmarshal(raw, target)
			target.ID = "invoice_doc"
			target.Name = "Invoice"
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, target)
			da.EXPECT().GetDraft(gomock.Any(), "p").AnyTimes().Return(draft, true, nil)
			mapping := &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}
			if scenario == "same_document" {
				mapping = &interfaces.DraftDocumentMapping{Mode: "same_document", SourceNameProperty: "number", TargetNameProperty: "number"}
			}
			if scenario == "unknown" {
				mapping.MentionProperty = "missing"
			}
			if scenario == "legacy_path" {
				mapping = nil
			}
			success := scenario == "linking" || scenario == "same_document"
			if success {
				da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), int64(10)).Return(true, nil)
			}
			service := &draftService{da: documentDraftMock{da}, ps: ps, kbResources: documentSaveSelection{}}
			request := interfaces.DraftRelationTypeSave{UpdateTime: 10, ID: "order_invoice", Name: "Invoiced order", SourceObjectTypeID: "order_doc", TargetObjectTypeID: "invoice_doc", Type: "direct", DocumentMapping: mapping, Cardinality: "one_to_many"}
			result, err := service.CreateRelationType(context.Background(), "Bearer synthetic", "p", request, caller)
			if success != (err == nil) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if !success {
				return
			}
			relation := result.Items.RelationTypes[0]
			if !relation.Adopted || relation.Document == nil || relation.Document.Citations[0].Quote != "" || relation.Cardinality != "one_to_many" || relation.Evidence.Computed {
				t.Fatal("human relation or proof policy lost; table counts invented")
			}
			if scenario == "same_document" && len(relation.Mapping) != 2 {
				t.Fatal("document join condition lost")
			}
			request.UpdateTime = result.UpdateTime
			request.Comment = "Confirmed rule"
			request.Cardinality = "many_to_one"
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), result.UpdateTime).Return(true, nil)
			edited, err := service.SaveRelationType(context.Background(), "Bearer synthetic", "p", relation.ID, request, caller)
			if err != nil || edited.Items.RelationTypes[0].Cardinality != "many_to_one" {
				t.Fatal("document relation could not be edited", err)
			}
			if edited.Items.RelationTypes[0].Origin != interfaces.DRAFT_ORIGIN_MANUAL {
				t.Fatal("editing lost manual origin")
			}
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), edited.UpdateTime).Return(true, nil)
			deleted, err := service.DeleteRelationType(context.Background(), "Bearer synthetic", "p", relation.ID, edited.UpdateTime, caller)
			if err != nil || len(deleted.Items.RelationTypes) != 0 {
				t.Fatal("edited manual relation could not be deleted", err)
			}
		})
	}
}

func TestDeleteEventParticipantRelationRequiresUpdatingEventFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	da := pmock.NewMockDraftAccess(ctrl)
	ps := pmock.NewMockProjectService(ctrl)
	ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p"}}, nil)
	draft := &interfaces.Draft{ProjectID: "p", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: 10,
		Items: interfaces.DraftItems{
			ObjectTypes:   []*interfaces.DraftObjectType{{ID: "shipment", Document: &interfaces.DraftDocumentModel{Role: "event", ParticipantRelations: []string{"carrier"}}}},
			RelationTypes: []*interfaces.DraftRelationType{{ID: "carrier", Origin: interfaces.DRAFT_ORIGIN_MANUAL}},
		}}
	da.EXPECT().GetDraft(gomock.Any(), "p").Return(draft, true, nil)
	service := &draftService{da: da, ps: ps}
	_, err := service.DeleteRelationType(context.Background(), "Bearer synthetic", "p", "carrier", 10, interfaces.AccountInfo{ID: "u", Type: "user"})
	if err == nil || len(draft.Items.RelationTypes) != 1 || len(draft.Items.Decisions) != 0 {
		t.Fatalf("participant deletion must fail without modifying draft: %v", err)
	}
}

func TestAddingStructuredCatalogPreservesDocumentOnlyDraft(t *testing.T) {
	original := editableDocumentDraft(t)
	original.Items.ObjectTypes[0].Adopted = true
	original.Items.Decisions = []*interfaces.DraftDecision{{Action: interfaces.DRAFT_DECISION_ADOPT, Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "order_doc"}}
	before, _ := json.Marshal(original.Items)
	table := membersTable()
	drafted, err := newDrafter(testSetting, sampleCounter()).draft(context.Background(), []*interfaces.VegaTable{table}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &draftService{setting: testSetting}
	merged := service.applyAdded(original.Items, &addedDraft{drafted: drafted, tables: []*interfaces.VegaTable{table}}, []string{"c1"}, &interfaces.DraftCatalogsAdded{})
	if len(merged.ObjectTypes) != 2 || merged.ObjectTypes[0].Document == nil || !merged.ObjectTypes[0].Adopted || merged.Inputs.NamingLanguage != "en-US" || len(merged.Inputs.Tables) != 1 || len(merged.Inputs.CatalogIDs) != 1 || len(merged.Decisions) != 1 {
		t.Fatal("adding tables damaged document model or its decisions")
	}
	after, _ := json.Marshal(original.Items)
	if string(before) != string(after) {
		t.Fatal("source draft mutated while merging")
	}
}
