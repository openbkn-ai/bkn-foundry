// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func reviewFixture(text string) (*SampleInput, kb.ConceptProposal) {
	input := modelInput(text)
	quote := documentQuote(input)
	relation := func(id, target string) *interfaces.DraftRelationType {
		return &interfaces.DraftRelationType{ID: id, Name: "关联业务对象", Source: "source_doc", Target: target, Adopted: true, Cardinality: "many_to_one", Document: &interfaces.DraftDocumentModel{Role: "relation", Reason: "样本明确给出了双方的编号", Citations: []interfaces.DraftSourceCitation{quote}}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}}
	}
	return input, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{proposedObject(input, "source_doc", "entity"), proposedObject(input, "local_claim", "in_document"), proposedObject(input, "global_doc", "entity")}, RelationTypes: []*interfaces.DraftRelationType{relation("needs_repair", "local_claim"), relation("valid_link", "global_doc")}}
}

func TestReviewPreservesValidObjectsAndBlocksIncompatibleRelations(t *testing.T) {
	for _, text := range []string{"订单 O-1 的附件中引用供应商。", "样本 S-8 的报告引用实验室。", "专利 P-9 的实施例引用材料。"} {
		input, proposal := reviewFixture(text)
		proposal.RelationTypes[0].Mapping = []interfaces.DraftFieldMapping{{SourceProperty: "injected", TargetProperty: "injected"}}
		if _, err := ValidateModel(input, interfaces.DraftItems{}, proposal); err == nil {
			t.Fatal("strict model validation unexpectedly accepted a document-local global link")
		}
		got, err := ReviewModel(input, interfaces.DraftItems{}, proposal)
		if err != nil || len(got.ObjectTypes) != 3 || len(got.RelationTypes) != 2 {
			t.Fatal("review silently discarded a grounded proposal", err)
		}
		bad, valid := got.RelationTypes[0], got.RelationTypes[1]
		if bad.DocumentRepair != "target_identity" || valid.DocumentRepair != "" || bad.Adopted || valid.Adopted || len(bad.Mapping) != 0 {
			t.Fatal("invalid relation was not explicitly blocked", bad, valid)
		}
		if got.ObjectTypes[1].Document.Role != "in_document" || len(got.ObjectTypes[1].PrimaryKeys) != 1 || got.ObjectTypes[1].PrimaryKeys[0] != "_id" || got.ObjectTypes[1].Adopted {
			t.Fatal("review silently promoted the role, identity or adoption")
		}
		for _, relation := range got.RelationTypes {
			if relation.Document.Citations[0].Quote != "" {
				t.Fatal("original-off quote persisted")
			}
		}
		if proposal.RelationTypes[0].Document.Citations[0].Quote == "" || !proposal.RelationTypes[0].Adopted {
			t.Fatal("review mutated transient Agent input")
		}
	}
}

func TestReviewDoesNotRecoverMalformedOrUngroundedRelations(t *testing.T) {
	for _, scenario := range []string{"quote", "unknown_target", "unknown_property", "type_mismatch", "unsupported_type", "forged_review", "self_join", "invalid_mode"} {
		t.Run(scenario, func(t *testing.T) {
			input, proposal := reviewFixture("记录 R-1 引用目标编号。")
			r := proposal.RelationTypes[0]
			switch scenario {
			case "quote":
				r.Document.Citations[0].Quote = "not in the source"
			case "unknown_target":
				r.Target = "unknown"
			case "unknown_property":
				r.DocumentMapping.IdentityProperty = "unknown"
			case "type_mismatch":
				proposal.ObjectTypes[1].Properties[0].Type = "integer"
			case "unsupported_type":
				proposal.ObjectTypes[1].Properties[0].Type = "arbitrary"
			case "forged_review":
				r.DocumentRepair = "target_identity"
			case "self_join":
				r.Target = r.Source
			case "invalid_mode":
				r.DocumentMapping.Mode = "projection"
			}
			if got, err := ReviewModel(input, interfaces.DraftItems{}, proposal); err == nil || got != nil {
				t.Fatal("invalid input yielded partial review candidates")
			}
		})
	}
}

func TestReviewMarksGlobalSameDocumentJoinWithoutChangingItsMeaning(t *testing.T) {
	input, proposal := reviewFixture("对象 A 和 B 在文档中分别提及。")
	proposal.RelationTypes = proposal.RelationTypes[:1]
	proposal.RelationTypes[0].DocumentMapping = &interfaces.DraftDocumentMapping{Mode: "same_document", SourceNameProperty: "number", TargetNameProperty: "number"}
	got, err := ReviewModel(input, interfaces.DraftItems{}, proposal)
	if err != nil || got.RelationTypes[0].DocumentRepair != "same_document_scope" || got.RelationTypes[0].DocumentMapping.Mode != "same_document" {
		t.Fatal("scope violation was dropped or silently converted", err)
	}
}

func TestRepairRequiresStrictValidationBeforeClearingTheBlock(t *testing.T) {
	input, proposal := reviewFixture("订单 R-1 引用对方编号。")
	checked, err := ReviewModel(input, interfaces.DraftItems{}, proposal)
	if err != nil {
		t.Fatal(err)
	}
	items := interfaces.DraftItems{ObjectTypes: checked.ObjectTypes, RelationTypes: checked.RelationTypes}
	original := items.RelationTypes[0]
	items.RelationTypes = items.RelationTypes[:1]
	if got, err := ValidateEditedRelation(input, items, original, original); err == nil || got != nil {
		t.Fatal("saving unchanged invalid relation cleared block")
	}
	edited := *original
	edited.Target = "global_doc"
	got, err := ValidateEditedRelation(input, items, original, &edited)
	if err != nil || got.DocumentRepair != "" || got.Adopted || len(got.Mapping) != 1 || original.DocumentRepair == "" {
		t.Fatal("strict repair failed or mutated original", err)
	}
}

func TestPendingRepairAllowsObjectReviewButNotBrokenFieldReferences(t *testing.T) {
	input, proposal := reviewFixture("对象 R-1 引用另一份记录。")
	checked, err := ReviewModel(input, interfaces.DraftItems{}, proposal)
	if err != nil {
		t.Fatal(err)
	}
	items := interfaces.DraftItems{ObjectTypes: checked.ObjectTypes, RelationTypes: checked.RelationTypes}
	original := items.ObjectTypes[1]
	candidate := *original
	candidate.Name = "已核实的记录"
	if _, err := ValidateEditedObject(input, items, original, &candidate); err != nil {
		t.Fatal("pending repair froze unrelated object editing", err)
	}
	candidate.Properties = append([]*interfaces.DraftProperty(nil), original.Properties[1:]...)
	if _, err := ValidateEditedObject(input, items, original, &candidate); err == nil {
		t.Fatal("object edit removed a pending relation's source field")
	}
}
