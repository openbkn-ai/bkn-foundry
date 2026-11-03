// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"encoding/json"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func factResolutionFixture() (interfaces.DraftItems, *interfaces.DraftObjectType) {
	fact := &interfaces.DraftObjectType{ID: "delivery_fact", Document: &interfaces.DraftDocumentModel{Role: "fact"}, Properties: []*interfaces.DraftProperty{{Name: "order_no", Type: "string"}, {Name: "delivery", Type: "date"}, {Name: "effective_from", Type: "datetime", ReadOnly: true}, {Name: "effective_to", Type: "datetime", ReadOnly: true}}}
	target := &interfaces.DraftObjectType{ID: "order", Adopted: true, PrimaryKeys: []string{"number"}, Table: interfaces.DraftTable{ResourceID: "source"}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}}}
	relation := &interfaces.DraftRelationType{ID: "for_order", Adopted: true, Source: fact.ID, Target: target.ID, Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "order_no", IdentityProperty: "number"}}
	return interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{fact, target}, RelationTypes: []*interfaces.DraftRelationType{relation}}, fact
}

func TestFactResolutionRequiresHumanSingleValueAndAdoptedEntityRelationship(t *testing.T) {
	for _, scenario := range []string{"valid", "app", "unconfirmed", "event", "readonly", "unknown_relation", "other_source", "unadopted_relation", "multi_value"} {
		t.Run(scenario, func(t *testing.T) {
			items, fact := factResolutionFixture()
			caller := interfaces.AccountInfo{ID: "person", Type: "user"}
			choice := interfaces.FactResolutionDecision{ValueProperty: "delivery", SubjectRelationID: "for_order", Confirm: true}
			switch scenario {
			case "app":
				caller.Type = "app"
			case "unconfirmed":
				choice.Confirm = false
			case "event":
				fact.Document.Role = "event"
			case "readonly":
				choice.ValueProperty = "effective_from"
			case "unknown_relation":
				choice.SubjectRelationID = "missing"
			case "other_source":
				items.RelationTypes[0].Source = "other"
			case "unadopted_relation":
				items.RelationTypes[0].Adopted = false
			case "multi_value":
				fact.Properties = append(fact.Properties, &interfaces.DraftProperty{Name: "quantity", Type: "integer"})
			}
			err := ConfirmFactResolution(items, fact, choice, caller, 100)
			if (err == nil) != (scenario == "valid" || scenario == "unconfirmed") {
				t.Fatal("invalid fact rule accepted", scenario, err)
			}
			if scenario == "unconfirmed" && ValidFactResolution(items, fact, fact.Document.FactResolution) {
				t.Fatal("unfinished choice became authority")
			}
			if scenario == "valid" && !ValidFactResolution(items, fact, fact.Document.FactResolution) {
				t.Fatal("confirmation lost")
			}
		})
	}
}

func TestFactResolutionInvalidatesOnSemanticChangeAndClearsExplicitly(t *testing.T) {
	for _, change := range []string{"value_type", "mention_type", "target", "target_mapping", "mapping", "adoption"} {
		items, fact := factResolutionFixture()
		caller := interfaces.AccountInfo{ID: "person", Type: "user"}
		if err := ConfirmFactResolution(items, fact, interfaces.FactResolutionDecision{ValueProperty: "delivery", SubjectRelationID: "for_order", Confirm: true}, caller, 100); err != nil {
			t.Fatal(err)
		}
		switch change {
		case "value_type":
			fact.Properties[1].Type = "datetime"
		case "mention_type":
			fact.Properties[0].Type = "integer"
		case "target":
			items.ObjectTypes[1].Table.ResourceID = "another-table"
		case "target_mapping":
			items.ObjectTypes[1].Properties[0].MappedField = "other_column"
		case "mapping":
			items.RelationTypes[0].DocumentMapping.IdentityProperty = "other"
		case "adoption":
			items.RelationTypes[0].Adopted = false
		}
		if ValidFactResolution(items, fact, fact.Document.FactResolution) {
			t.Fatal("semantic change retained old confirmation", change)
		}
		if err := ConfirmFactResolution(items, fact, interfaces.FactResolutionDecision{Clear: true}, caller, 200); err != nil || fact.Document.FactResolution != nil {
			t.Fatal("explicit clear failed", err)
		}
	}
}

func TestFactRuleEditPreservesOnlyCurrentHumanAuthorityAndRejectsAIProof(t *testing.T) {
	input := modelInput("Reference A-1. Value 10.")
	fact := proposedObject(input, "assertion", "fact")
	fact.Properties = append(fact.Properties, &interfaces.DraftProperty{Name: "value", DisplayName: "Value", Type: "string", Extraction: &interfaces.DraftExtractionField{Instruction: "Read the stated value", Examples: []interfaces.DraftSourceCitation{documentQuote(input)}}})
	entity := proposedObject(input, "subject", "entity")
	validated, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{entity, fact}})
	if err != nil {
		t.Fatal(err)
	}
	entity, fact = validated.ObjectTypes[0], validated.ObjectTypes[1]
	entity.Adopted = true
	relation := &interfaces.DraftRelationType{ID: "applies", Adopted: true, Source: fact.ID, Target: entity.ID, Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}}
	items := interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{entity, fact}, RelationTypes: []*interfaces.DraftRelationType{relation}}
	if err := ConfirmFactResolution(items, fact, interfaces.FactResolutionDecision{ValueProperty: "value", SubjectRelationID: relation.ID, Confirm: true}, interfaces.AccountInfo{ID: "reviewer", Type: "user"}, 100); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"instruction", "value_type", "proposal", "forged"} {
		raw, _ := json.Marshal(fact)
		var edited interfaces.DraftObjectType
		_ = json.Unmarshal(raw, &edited)
		switch scenario {
		case "proposal":
			fact.Document.FactResolution.ConfirmedBy = interfaces.AccountInfo{}
			fact.Document.FactResolution.ConfirmedAt = 0
			edited.Document.FactResolution.ConfirmedBy = interfaces.AccountInfo{}
			edited.Document.FactResolution.ConfirmedAt = 0
		case "instruction":
			property(&edited, "value").Extraction.Instruction = "Read only an explicit source value"
		case "value_type":
			property(&edited, "value").Type = "text"
		case "forged":
			edited.Document.FactResolution.ConfirmedBy.ID = "forged"
		}
		checked, err := ValidateEditedObject(input, items, fact, &edited)
		if scenario == "forged" {
			if err == nil {
				t.Fatal("forged authority accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if checked.Document.FactResolution == nil || checked.Document.FactResolution.ValueProperty != "value" {
			t.Fatal("human choice lost", scenario)
		}
		if ValidFactResolution(items, checked, checked.Document.FactResolution) != (scenario == "instruction") {
			t.Fatal("wrong confirmation preservation", scenario)
		}
	}
	if _, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{fact}}); err == nil {
		t.Fatal("Agent supplied a human fact rule")
	}
}
