// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/licverify"
	"go.uber.org/mock/gomock"
	"testing"
)

func TestSaveSameBusinessRelationRequiresExplicitVerifiedIntent(t *testing.T) {
	for _, scenario := range []string{"verify", "pending", "duplicate", "forged"} {
		t.Run(scenario, func(t *testing.T) {
			kb.ResetForTest()
			entitlement.ResetForTest()
			t.Cleanup(func() { kb.ResetForTest(); entitlement.ResetForTest() })
			entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
			kb.Register(kb.Providers{Source: &documentSaveSource{}, Concepts: kb.UnavailableConcepts{}})
			draft := editableDocumentDraft(t)
			source := draft.Items.ObjectTypes[0]
			source.Adopted = true
			_, _, backend := businessIdentityFixture()
			target := &interfaces.DraftObjectType{ID: "system_order", Name: "System orders", Adopted: true, PrimaryKeys: []string{"internal_id"}, Table: interfaces.DraftTable{ResourceID: "system-records"}, Properties: []*interfaces.DraftProperty{{Name: "internal_id", Type: "string", MappedField: "id"}, {Name: "number", Type: "string", MappedField: "business_number"}}}
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, target)
			relation := &interfaces.DraftRelationType{ID: "same_order", Name: "Same order", Source: source.ID, Target: target.ID, Cardinality: "one_to_one", Document: &interfaces.DraftDocumentModel{Role: "relation", Reason: "The same business identifier", Citations: source.Document.Citations, SelectionID: "lock", SelectionRevision: 7}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}}
			if scenario == "pending" {
				relation.Adopted = true
				relation.DocumentMapping.IdentityProperty = "internal_id"
			}
			draft.Items.RelationTypes = []*interfaces.DraftRelationType{relation}
			save := interfaces.DraftRelationTypeSave{UpdateTime: 10, Name: relation.Name, SourceObjectTypeID: source.ID, TargetObjectTypeID: target.ID, Cardinality: "one_to_one", Type: "direct", DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}, VerifyBusinessIdentity: scenario != "pending"}
			if scenario == "duplicate" {
				backend.rows[0]["distinct_values"] = 9
			}
			if scenario == "forged" {
				save.DocumentMapping.BusinessIdentity = &interfaces.DraftBusinessIdentityEvidence{Rows: 10, Distinct: 10}
			}
			controller := gomock.NewController(t)
			defer controller.Finish()
			access := pmock.NewMockDraftAccess(controller)
			projects := pmock.NewMockProjectService(controller)
			caller := interfaces.AccountInfo{ID: "u", Type: "user"}
			projects.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: caller}}, nil)
			access.EXPECT().GetDraft(gomock.Any(), "p").Return(draft, true, nil)
			if scenario == "verify" || scenario == "pending" {
				access.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), int64(10)).Return(true, nil)
			}
			service := &draftService{da: documentDraftMock{access}, ps: projects, va: backend}
			useDocumentResources(t, documentSaveSelection{})
			got, err := service.SaveRelationType(context.Background(), "Bearer synthetic", "p", relation.ID, save, caller)
			if scenario == "duplicate" || scenario == "forged" {
				if err == nil {
					t.Fatal("unverified identity saved")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			edge := got.Items.RelationTypes[0]
			if edge.Adopted {
				t.Fatal("saving a business identity silently adopted the relationship")
			}
			if scenario == "verify" && (edge.DocumentMapping.BusinessIdentity == nil || edge.DocumentMapping.BusinessIdentity.ConfirmedBy != caller || len(backend.sql) != 1) {
				t.Fatal("explicit verification did not persist trusted full-table evidence")
			}
			if scenario == "pending" && (edge.DocumentMapping.BusinessIdentity != nil || len(backend.sql) != 0) {
				t.Fatal("an unrequested count or proof was created")
			}
			adopt := true
			_, adoptionError := applyEdit(context.Background(), &got.Items, interfaces.DRAFT_KIND_RELATION_TYPES, edge.ID, interfaces.DraftItemEdit{Adopted: &adopt})
			if scenario == "pending" {
				var problem *httperrors.HTTPError
				if !errors.As(adoptionError, &problem) || problem.BaseError.ErrorCode != "BknProject.Draft.BusinessIdentityNotVerified" || edge.Adopted {
					t.Fatal("unverified business number could be adopted", adoptionError)
				}
			} else if adoptionError != nil || !edge.Adopted {
				t.Fatal("human adoption of a verified business number failed", adoptionError)
			}
			if target.PrimaryKeys[0] != "internal_id" {
				t.Fatal("system primary key changed")
			}
		})
	}
}
