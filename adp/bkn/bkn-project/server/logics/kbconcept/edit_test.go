// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"encoding/json"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestDocumentObjectEditsPreserveManagedColumnsAndVerifyExamples(t *testing.T) {
	for _, scenario := range []string{"instruction", "readonly", "quote", "missing_examples", "role", "type", "identity"} {
		t.Run(scenario, func(t *testing.T) {
			input := modelInput("订单 O-1")
			validated, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{proposedObject(input, "order_doc", "entity")}})
			if err != nil {
				t.Fatal(err)
			}
			original := validated.ObjectTypes[0]
			original.Adopted = true
			raw, _ := json.Marshal(original)
			edited := &interfaces.DraftObjectType{}
			_ = json.Unmarshal(raw, edited)
			switch scenario {
			case "instruction":
				edited.Properties[0].Extraction.Instruction = "Read only the explicit number"
			case "readonly":
				edited.Properties[1].Type = "integer"
			case "quote":
				edited.Properties[0].Extraction.Examples[0].Quote = "invented"
			case "missing_examples":
				edited.Properties[0].Extraction.Examples = nil
			case "role":
				edited.Document.Role = "fact"
			case "type":
				edited.Properties[0].Type = "vector"
			case "identity":
				edited.PrimaryKeys = nil
			}
			result, err := ValidateEditedObject(input, interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{original}}, original, edited)
			success := scenario == "instruction" || scenario == "identity"
			if success != (err == nil) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if !success {
				return
			}
			if len(result.Properties) != len(original.Properties) || result.Document.Citations[0].Quote != "" || result.Properties[0].Extraction.Examples[0].Quote != "" {
				t.Fatal("managed fields or quote policy changed")
			}
			if scenario == "instruction" && (!result.Adopted || result.Properties[0].Extraction.Instruction == original.Properties[0].Extraction.Instruction) {
				t.Fatal("human edit or adoption lost")
			}
			if scenario == "identity" && (!result.NeedsIdentity || result.Adopted) {
				t.Fatal("missing identity stayed adopted")
			}
			if original.Properties[0].Extraction.Examples[0].Quote != "" {
				t.Fatal("stored proof was hydrated in place")
			}
		})
	}
}

func TestDocumentRelationEditsValidateBothModesAgainstCurrentSchema(t *testing.T) {
	input := modelInput("Order O-1 appears on invoice I-1")
	from, to := proposedObject(input, "order_doc", "entity"), proposedObject(input, "invoice_doc", "entity")
	from.Name, to.Name = "Order", "Invoice"
	objects, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{from, to}})
	if err != nil {
		t.Fatal(err)
	}
	existing := interfaces.DraftItems{ObjectTypes: objects.ObjectTypes}
	rawRelation := &interfaces.DraftRelationType{ID: "order_invoice", Name: "Invoiced order", Source: from.ID, Target: to.ID, Cardinality: "one_to_one", Document: &interfaces.DraftDocumentModel{Role: "relation", Reason: "Confirmed field rule", Citations: []interfaces.DraftSourceCitation{documentQuote(input)}}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}}
	initial, err := ValidateModel(input, existing, kb.ConceptProposal{RelationTypes: []*interfaces.DraftRelationType{rawRelation}})
	if err != nil {
		t.Fatal(err)
	}
	original := initial.RelationTypes[0]
	original.Adopted = true
	existing.RelationTypes = initial.RelationTypes
	for _, scenario := range []string{"linking", "same_document", "unknown", "forged"} {
		t.Run(scenario, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			edited := &interfaces.DraftRelationType{}
			_ = json.Unmarshal(raw, edited)
			switch scenario {
			case "same_document":
				edited.DocumentMapping = &interfaces.DraftDocumentMapping{Mode: "same_document", SourceNameProperty: "number", TargetNameProperty: "number"}
			case "unknown":
				edited.DocumentMapping.MentionProperty = "missing"
			case "forged":
				edited.Document.Citations[0].Quote = "not in source"
			}
			result, err := ValidateEditedRelation(input, existing, original, edited)
			success := scenario == "linking"
			if success != (err == nil) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if success && (!result.Adopted || result.Document.Citations[0].Quote != "" || result.Verification != "confirmed") {
				t.Fatal("adoption/proof policy changed")
			}
		})
	}
}

func TestEvidenceEdgesDoNotSatisfyEventParticipants(t *testing.T) {
	input := modelInput("Delivery D-1 happened today")
	event := proposedObject(input, "delivery", "event")
	event.Document.TimeProperty = "when"
	event.Document.ParticipantRelations = []string{"event_evidence"}
	event.Properties = append(event.Properties, &interfaces.DraftProperty{Name: "when", DisplayName: "Time", Type: "datetime", Extraction: &interfaces.DraftExtractionField{Instruction: "Read the explicit event time", Examples: []interfaces.DraftSourceCitation{documentQuote(input)}}})
	existing := interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "source_document", Document: &interfaces.DraftDocumentModel{Role: "evidence_document"}}}, RelationTypes: []*interfaces.DraftRelationType{{ID: "event_evidence", Source: "delivery", Target: "source_document", Document: &interfaces.DraftDocumentModel{Role: "evidence_reference"}}}}
	if _, err := ValidateModel(input, existing, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{event}}); err == nil {
		t.Fatal("technical evidence reference counted as an event participant")
	}
}

func TestObjectEditCannotPreserveGlobalEntitySameDocumentJoin(t *testing.T) {
	input := modelInput("Order O-1 appears in source file F-1")
	result, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{proposedObject(input, "order_doc", "entity"), proposedObject(input, "order_file", "file")}})
	if err != nil {
		t.Fatal(err)
	}
	original := result.ObjectTypes[0]
	raw, _ := json.Marshal(original)
	var candidate interfaces.DraftObjectType
	if err := json.Unmarshal(raw, &candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Properties[0].Extraction.Instruction = "Read the explicit business identifier"
	legacy := &interfaces.DraftRelationType{ID: "legacy_join", Source: "order_doc", Target: "order_file", DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "same_document", SourceNameProperty: "number", TargetNameProperty: "number"}, Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "document_id", TargetProperty: "document_id"}, {SourceProperty: "number", TargetProperty: "number"}}}
	if _, err := ValidateEditedObject(input, interfaces.DraftItems{ObjectTypes: result.ObjectTypes, RelationTypes: []*interfaces.DraftRelationType{legacy}}, original, &candidate); err == nil {
		t.Fatal("object edit kept an invalid global-entity document constraint")
	}
}
