// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestSameBusinessCandidateKeepsSystemPrimaryKeyAndWaitsForHumanVerification(t *testing.T) {
	input := modelInput("Agreement A-1 identifies the corresponding system record.")
	document := proposedObject(input, "agreement_doc", "entity")
	system := &interfaces.DraftObjectType{ID: "agreement", Adopted: true, PrimaryKeys: []string{"internal_id"}, Table: interfaces.DraftTable{ResourceID: "system-agreements"}, Properties: []*interfaces.DraftProperty{{Name: "internal_id", Type: "string", MappedField: "id"}, {Name: "number", Type: "string", MappedField: "agreement_number"}}}
	relation := &interfaces.DraftRelationType{ID: "same_agreement", Name: "Same agreement", Source: document.ID, Target: system.ID, Cardinality: "one_to_one", Document: &interfaces.DraftDocumentModel{Role: "relation", Reason: "The same global number occurs on both sides", Citations: []interfaces.DraftSourceCitation{documentQuote(input)}}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}}
	checked, err := ValidateModel(input, interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{system}}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{document}, RelationTypes: []*interfaces.DraftRelationType{relation}})
	if err != nil {
		t.Fatal(err)
	}
	items := interfaces.DraftItems{ObjectTypes: append(checked.ObjectTypes, system), RelationTypes: checked.RelationTypes}
	edge := checked.RelationTypes[0]
	if LinkIdentityReady(items, edge) {
		t.Fatal("AI proposal confirmed its own business key")
	}
	edge.DocumentMapping.BusinessIdentity = &interfaces.DraftBusinessIdentityEvidence{TargetObjectTypeID: system.ID, ResourceID: system.Table.ResourceID, Property: "number", MappedField: "agreement_number", Type: "string", Rows: 10, Distinct: 10, ConfirmedBy: interfaces.AccountInfo{ID: "owner", Type: "user"}, CheckedAt: 1}
	if !LinkIdentityReady(items, edge) {
		t.Fatal("verified business key not accepted")
	}
	if len(system.PrimaryKeys) != 1 || system.PrimaryKeys[0] != "internal_id" {
		t.Fatal("system primary key was replaced")
	}
	for _, scenario := range []string{"duplicate", "null", "remapped", "many_to_one", "unconfirmed"} {
		t.Run(scenario, func(t *testing.T) {
			proof := *edge.DocumentMapping.BusinessIdentity
			edge.DocumentMapping.BusinessIdentity = &proof
			switch scenario {
			case "duplicate":
				proof.Distinct--
			case "null":
				proof.Nulls = 1
			case "remapped":
				proof.MappedField = "another_field"
			case "many_to_one":
				edge.Cardinality = "many_to_one"
			case "unconfirmed":
				proof.ConfirmedBy.ID = ""
			}
			if LinkIdentityReady(items, edge) {
				t.Fatal("invalid business identity accepted")
			}
			edge.Cardinality = "one_to_one"
			proof.Distinct = 10
			proof.Nulls = 0
			proof.MappedField = "agreement_number"
			proof.ConfirmedBy.ID = "owner"
		})
	}
}
