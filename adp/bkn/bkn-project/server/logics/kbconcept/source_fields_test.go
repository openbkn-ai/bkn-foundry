// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func sourceFieldsFixture(domain string) (interfaces.DraftItems, *interfaces.DraftObjectType, interfaces.DualSourceChoice) {
	doc := &interfaces.DraftObjectType{ID: domain + "_doc", Adopted: true, PrimaryKeys: []string{"number"}, Document: &interfaces.DraftDocumentModel{Role: "entity", SelectionID: "selected", SelectionRevision: 1}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}, {Name: "terms", Type: "text"}}}
	system := &interfaces.DraftObjectType{ID: domain, Adopted: true, PrimaryKeys: []string{"number"}, Table: interfaces.DraftTable{ResourceID: "system-source"}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string", MappedField: "business_number"}, {Name: "amount", Type: "decimal", MappedField: "amount"}, {Name: "internal_status", Type: "string", MappedField: "status"}}}
	file := &interfaces.DraftObjectType{ID: domain + "_file", Adopted: true, PrimaryKeys: []string{"_id"}, Document: &interfaces.DraftDocumentModel{Role: "file", SelectionID: "selected", SelectionRevision: 1}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}, {Name: "amount", Type: "decimal"}, {Name: "terms", Type: "text"}}}
	fact := &interfaces.DraftObjectType{ID: domain + "_promise", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "fact", SelectionID: "selected", SelectionRevision: 1}, Properties: []*interfaces.DraftProperty{{Name: "due_date", Type: "date"}}}
	relation := &interfaces.DraftRelationType{ID: "same_" + domain, Adopted: true, Source: doc.ID, Target: system.ID, Cardinality: "one_to_one", Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}}
	choice := interfaces.DualSourceChoice{SystemObjectTypeID: system.ID, FileObjectTypeID: file.ID, RelationTypeID: relation.ID, Fields: []interfaces.SourceFieldRule{
		{Name: "number", Category: "both", SourceOfRecord: "table", DestinationObjectTypeID: system.ID},
		{Name: "amount", Category: "both", SourceOfRecord: "table", DestinationObjectTypeID: system.ID},
		{Name: "internal_status", Category: "system_only", SourceOfRecord: "table", DestinationObjectTypeID: system.ID},
		{Name: "terms", Category: "text_only", SourceOfRecord: "document", DestinationObjectTypeID: doc.ID},
		{Name: "due_date", Category: "temporal", SourceOfRecord: "fusion", DestinationObjectTypeID: fact.ID, EvidenceOrder: []string{"effective_time", "evidence_strength", "recorded_at"}},
	}}
	return interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{doc, system, file, fact}, RelationTypes: []*interfaces.DraftRelationType{relation}}, doc, choice
}
func TestDualSourceFieldAuthorityAndPlacementAcrossDomains(t *testing.T) {
	for _, domain := range []string{"agreement", "patent", "equipment"} {
		t.Run(domain, func(t *testing.T) {
			items, doc, choice := sourceFieldsFixture(domain)
			signature, err := SourcePlanSignature(items, doc.ID, choice, true)
			if err != nil {
				t.Fatal(err)
			}
			doc.Document.DualSource = &interfaces.DraftDualSourcePlan{DualSourceChoice: choice, ConfirmedBy: interfaces.AccountInfo{ID: "owner", Type: "user"}, ConfirmedAt: 1, Signature: signature}
			if !SourcePlanReady(items, doc, true) {
				t.Fatal("human-confirmed placement not accepted")
			}
			doc.Properties[1].Extraction = &interfaces.DraftExtractionField{Instruction: "Read the explicit terms"}
			doc.Table.ResourceID = "prepared-dataset"
			doc.Properties[1].MappedField = "terms"
			if !SourcePlanReady(items, doc, true) {
				t.Fatal("physical Dataset or instruction edits changed field authority")
			}
			doc.Properties[1].Name = "changed_meaning"
			if SourcePlanReady(items, doc, true) {
				t.Fatal("renamed model reused stale field decisions")
			}
		})
	}
}
func TestDualSourceFieldPlanRejectsProjectionAmbiguityAndMissingModels(t *testing.T) {
	for _, scenario := range []string{"duplicate_current", "missing_system_field", "wrong_authority", "misplaced_temporal", "missing_destination", "priority_on_table", "unadopted_file", "source_revision"} {
		t.Run(scenario, func(t *testing.T) {
			items, doc, choice := sourceFieldsFixture("agreement")
			switch scenario {
			case "duplicate_current":
				doc.Properties = append(doc.Properties, &interfaces.DraftProperty{Name: "amount", Type: "decimal"})
			case "missing_system_field":
				choice.Fields = choice.Fields[1:]
			case "wrong_authority":
				choice.Fields[1].SourceOfRecord = "document"
			case "misplaced_temporal":
				doc.Properties = append(doc.Properties, &interfaces.DraftProperty{Name: "due_date", Type: "date"})
			case "missing_destination":
				choice.Fields[4].DestinationObjectTypeID = "unknown"
			case "priority_on_table":
				choice.Fields[0].EvidenceOrder = []string{"recorded_at"}
			case "unadopted_file":
				items.ObjectTypes[2].Adopted = false
			case "source_revision":
				items.ObjectTypes[2].Document.SelectionRevision++
			}
			if _, err := SourcePlanSignature(items, doc.ID, choice, true); err == nil {
				t.Fatal("invalid field authority/placement accepted")
			}
		})
	}
}

func TestSourcePlanInvalidatesChangedMainFileScope(t *testing.T) {
	items, doc, choice := sourceFieldsFixture("equipment")
	signature, err := SourcePlanSignature(items, doc.ID, choice, true)
	if err != nil {
		t.Fatal(err)
	}
	doc.Document.DualSource = &interfaces.DraftDualSourcePlan{DualSourceChoice: choice, ConfirmedBy: interfaces.AccountInfo{ID: "owner", Type: "user"}, ConfirmedAt: 1, Signature: signature}
	items.ObjectTypes[2].Description = "Only accepted equipment reports, excluding estimates"
	if SourcePlanReady(items, doc, true) {
		t.Fatal("changed main-document scope reused old field confirmation")
	}
}

func TestAgentCannotForgeHumanSourceFieldConfirmation(t *testing.T) {
	input := modelInput("A-1 is the explicit business number.")
	object := proposedObject(input, "agreement_doc", "entity")
	object.Document.DualSource = &interfaces.DraftDualSourcePlan{ConfirmedBy: interfaces.AccountInfo{ID: "forged-owner", Type: "user"}, ConfirmedAt: 1, Signature: "forged"}
	if _, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{object}}); err == nil {
		t.Fatal("Agent supplied its own field confirmation")
	}
	object.Document.DualSource = nil
	checked, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{object}})
	if err != nil {
		t.Fatal(err)
	}
	original := checked.ObjectTypes[0]
	edited := *original
	metadata := *original.Document
	metadata.DualSource = &interfaces.DraftDualSourcePlan{ConfirmedAt: 1}
	edited.Document = &metadata
	if _, err := ValidateEditedObject(input, interfaces.DraftItems{}, original, &edited); err == nil {
		t.Fatal("edited proposal supplied its own confirmation")
	}
}
