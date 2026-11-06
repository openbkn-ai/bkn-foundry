// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
	"go.uber.org/mock/gomock"
	"strings"
	"testing"
)

func TestSourceFieldChoicesNeedExplicitHumanConfirmationAndCurrentModels(t *testing.T) {
	draft, relation, _ := businessIdentityFixture()
	doc, system := draft.Items.ObjectTypes[0], draft.Items.ObjectTypes[1]
	doc.Document.SelectionID = "selected"
	doc.Document.SelectionRevision = 1
	system.PrimaryKeys = []string{"number"}
	file := &interfaces.DraftObjectType{ID: "agreement_file", Adopted: true, PrimaryKeys: []string{"_id"}, Document: &interfaces.DraftDocumentModel{Role: "file", SelectionID: "selected", SelectionRevision: 1}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}}}
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, file)
	choice := interfaces.DualSourceChoice{SystemObjectTypeID: system.ID, FileObjectTypeID: file.ID, RelationTypeID: relation.ID, Fields: []interfaces.SourceFieldRule{{Name: "number", Category: "both", SourceOfRecord: "table", DestinationObjectTypeID: system.ID}, {Name: "internal_id", Category: "system_only", SourceOfRecord: "table", DestinationObjectTypeID: system.ID}}}
	caller := interfaces.AccountInfo{ID: "human-owner", Type: "user"}
	if err := applySourceFieldDecision(context.Background(), draft, doc, &interfaces.DualSourceDecision{DualSourceChoice: choice}, caller); err != nil {
		t.Fatal(err)
	}
	if kbconcept.SourcePlanReady(draft.Items, doc, false) || checkSourceFieldDecisions(context.Background(), draft) == nil {
		t.Fatal("an unconfirmed field plan authorized the model")
	}
	if err := applySourceFieldDecision(context.Background(), draft, doc, &interfaces.DualSourceDecision{DualSourceChoice: choice, Confirm: true}, caller); err != nil {
		t.Fatal(err)
	}
	if !kbconcept.SourcePlanReady(draft.Items, doc, true) || checkSourceFieldDecisions(context.Background(), draft) != nil {
		t.Fatal("human-confirmed plan was not accepted")
	}
	if doc.Document.DualSource.ConfirmedBy != caller {
		t.Fatal("confirmation attribution not server owned")
	}
	file.Properties[0].Type = "integer"
	if checkSourceFieldDecisions(context.Background(), draft) == nil {
		t.Fatal("changed source schema bypassed field review")
	}
	if err := applySourceFieldDecision(context.Background(), draft, doc, &interfaces.DualSourceDecision{Clear: true}, caller); err != nil || doc.Document.DualSource != nil {
		t.Fatal("cannot explicitly clear source decisions", err)
	}
	if checkSourceFieldDecisions(context.Background(), draft) == nil {
		t.Fatal("removing source decisions bypassed an adopted identity bridge")
	}
}
func TestRelationAssistantNeverReceivesPrivateBusinessKeyVerification(t *testing.T) {
	_, relation, _ := businessIdentityFixture()
	relation.DocumentMapping.BusinessIdentity = &interfaces.DraftBusinessIdentityEvidence{ResourceID: "private-source-resource", ConfirmedBy: interfaces.AccountInfo{ID: "private-owner-id", Type: "user"}, Rows: 10, Distinct: 10, CheckedAt: 1}
	raw, err := json.Marshal(askRelationOf(relation))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-owner-id") || strings.Contains(string(raw), "private-source-resource") || strings.Contains(string(raw), "business_identity") {
		t.Fatal("private audit metadata entered an Agent input")
	}
}

func TestSaveObjectPersistsHumanFieldChoicesThroughExistingEndpoint(t *testing.T) {
	kb.ResetForTest()
	entitlement.ResetForTest()
	t.Cleanup(func() { kb.ResetForTest(); entitlement.ResetForTest() })
	entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
	kb.Register(kb.Providers{Source: &documentSaveSource{}, Concepts: kb.UnavailableConcepts{}})
	draft := editableDocumentDraft(t)
	doc := draft.Items.ObjectTypes[0]
	system := &interfaces.DraftObjectType{ID: "system_order", Name: "System orders", Adopted: true, PrimaryKeys: []string{"number"}, Table: interfaces.DraftTable{ResourceID: "system-records"}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string", MappedField: "business_number"}}}
	file := &interfaces.DraftObjectType{ID: "order_file", Name: "Order files", Adopted: true, PrimaryKeys: []string{"_id"}, Document: &interfaces.DraftDocumentModel{Role: "file", SelectionID: "lock", SelectionRevision: 7}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}}}
	relation := &interfaces.DraftRelationType{ID: "same_order", Name: "Same order", Adopted: true, Source: doc.ID, Target: system.ID, Cardinality: "one_to_one", Document: &interfaces.DraftDocumentModel{Role: "relation", Reason: "Same order number", Citations: doc.Document.Citations, SelectionID: "lock", SelectionRevision: 7}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}}
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, system, file)
	draft.Items.RelationTypes = []*interfaces.DraftRelationType{relation}
	choice := interfaces.DualSourceChoice{SystemObjectTypeID: system.ID, FileObjectTypeID: file.ID, RelationTypeID: relation.ID, Fields: []interfaces.SourceFieldRule{{Name: "number", Category: "both", SourceOfRecord: "table", DestinationObjectTypeID: system.ID}}}
	save := interfaces.DraftObjectTypeSave{UpdateTime: 10, Name: doc.Name, Comment: doc.Description, PrimaryKeys: doc.PrimaryKeys, DisplayKey: doc.DisplayKey, Document: &interfaces.DraftDocumentSettings{DualSource: &interfaces.DualSourceDecision{DualSourceChoice: choice, Confirm: true}}}
	for _, field := range doc.Properties {
		save.DataProperties = append(save.DataProperties, interfaces.DraftDataPropertySave{Name: field.Name, DisplayName: field.DisplayName, Comment: field.Description, Type: field.Type, Extraction: field.Extraction})
	}
	ctrl := gomock.NewController(t)
	access := pmock.NewMockDraftAccess(ctrl)
	projects := pmock.NewMockProjectService(ctrl)
	caller := interfaces.AccountInfo{ID: "u", Type: "user"}
	projects.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: caller, NamingLanguage: "en-US"}}, nil)
	access.EXPECT().GetDraft(gomock.Any(), "p").Return(draft, true, nil)
	access.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), int64(10)).DoAndReturn(func(_ context.Context, persisted *interfaces.Draft, _ int64) (bool, error) {
		plan := persisted.Items.ObjectTypes[0].Document.DualSource
		if plan == nil || plan.ConfirmedBy != caller || plan.ConfirmedAt <= 0 || !kbconcept.SourcePlanReady(persisted.Items, persisted.Items.ObjectTypes[0], false) {
			t.Fatal("saved confirmation was not server validated")
		}
		return true, nil
	})
	service := &draftService{da: documentDraftMock{access}, ps: projects}
	useDocumentResources(t, documentSaveSelection{})
	result, err := service.SaveObjectType(context.Background(), "Bearer synthetic", "p", doc.ID, save, caller)
	if err != nil {
		t.Fatal(err)
	}
	if result.Items.ObjectTypes[0].Adopted {
		t.Fatal("saving source choices silently adopted a candidate")
	}
}
