// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"strings"
	"testing"
)

func TestProposalNamesNormalizeOnlyUnreviewedObjectsAndReferences(t *testing.T) {
	for _, domain := range []string{"agreement", "patent", "equipment"} {
		t.Run(domain, func(t *testing.T) {
			proposal := kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{{ID: domain, Document: &interfaces.DraftDocumentModel{Role: "entity"}}, {ID: "file", Document: &interfaces.DraftDocumentModel{Role: "file"}}}, RelationTypes: []*interfaces.DraftRelationType{{ID: "same", Source: domain, Target: "system"}, {ID: "claim", Source: "file", Target: domain}}}
			existing := interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "system", Adopted: true}}}
			result, err := PrepareProposalNames(existing, proposal)
			if err != nil {
				t.Fatal(err)
			}
			if result.ObjectTypes[0].ID != domain+"_doc" || result.ObjectTypes[1].ID != "file" || result.RelationTypes[0].Source != domain+"_doc" || result.RelationTypes[0].Target != "system" || result.RelationTypes[1].Target != domain+"_doc" {
				t.Fatal("candidate references not consistently named")
			}
			if proposal.ObjectTypes[0].ID != domain || proposal.RelationTypes[0].Source != domain || existing.ObjectTypes[0].ID != "system" {
				t.Fatal("provider output or human draft mutated")
			}
		})
	}
}
func TestProposalNamesRefuseAmbiguousCollisions(t *testing.T) {
	for _, ids := range [][]string{{"order", "order_doc"}, {"order", "order"}, {strings.Repeat("a", 40), strings.Repeat("a", 36) + "bbbb"}} {
		proposal := kb.ConceptProposal{}
		for _, id := range ids {
			proposal.ObjectTypes = append(proposal.ObjectTypes, &interfaces.DraftObjectType{ID: id, Document: &interfaces.DraftDocumentModel{Role: "entity"}})
		}
		if _, err := PrepareProposalNames(interfaces.DraftItems{}, proposal); err == nil {
			t.Fatal("ambiguous identity renamed silently")
		}
	}
	proposal := kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{{ID: "order", Document: &interfaces.DraftDocumentModel{Role: "entity"}}}}
	for _, existingID := range []string{"order", "order_doc"} {
		if _, err := PrepareProposalNames(interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: existingID}}}, proposal); err == nil {
			t.Fatal("existing human identity was shadowed")
		}
	}
}
func TestLegacyDocumentIdentityDoesNotDeadlockFieldConfirmation(t *testing.T) {
	items, document, choice := sourceFieldsFixture("agreement")
	old := document.ID
	document.ID = "legacy_agreement"
	items.RelationTypes[0].Source = document.ID
	for i := range choice.Fields {
		if choice.Fields[i].DestinationObjectTypeID == old {
			choice.Fields[i].DestinationObjectTypeID = document.ID
		}
	}
	if _, err := SourcePlanSignature(items, document.ID, choice, true); err != nil {
		t.Fatal("legacy identity cannot confirm otherwise valid field decisions", err)
	}
}
