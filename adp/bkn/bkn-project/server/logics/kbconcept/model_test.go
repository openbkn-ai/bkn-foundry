// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func modelInput(text string) *SampleInput {
	return &SampleInput{SelectionID: "lock", SelectionRevision: 7, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8"}}, Request: kb.ConceptRequest{NamingLanguage: "zh-CN", Samples: []kb.Chunk{{Text: text, Citation: kb.Citation{KnowledgeBaseID: "1", VersionRef: kb.VersionRef{FileID: "41", ContentVersionID: "8"}, BlockID: "b1", Start: 0, End: len([]rune(text)), Quote: text}}}}}
}
func documentQuote(input *SampleInput) interfaces.DraftSourceCitation {
	r := input.Request.Samples[0].Citation
	return interfaces.DraftSourceCitation{KnowledgeBaseID: r.KnowledgeBaseID, FileID: r.FileID, ContentVersionID: r.ContentVersionID, BlockID: r.BlockID, Start: r.Start, End: r.End, Quote: r.Quote}
}
func proposedObject(input *SampleInput, id, role string) *interfaces.DraftObjectType {
	q := documentQuote(input)
	return &interfaces.DraftObjectType{ID: id, Name: "业务对象", Description: "来源中的业务对象", Adopted: true, PrimaryKeys: []string{"number"}, DisplayKey: "number", Document: &interfaces.DraftDocumentModel{Role: role, Reason: "来源明确给出独立编号，需单独描述", Citations: []interfaces.DraftSourceCitation{q}}, Properties: []*interfaces.DraftProperty{{Name: "number", DisplayName: "编号", Type: "string", Extraction: &interfaces.DraftExtractionField{Instruction: "仅提取原文明确出现的编号", Examples: []interfaces.DraftSourceCitation{q}}}}}
}
func TestValidatedDocumentModelsUseGenericRulesAndSystemColumns(t *testing.T) {
	for _, text := range []string{"合同 C-1 约定交付。", "专利 P-9 包含连接件。", "维修工单 M-3 已关闭。"} {
		input := modelInput(text)
		proposal := kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{proposedObject(input, "business_record", "entity")}}
		result, err := ValidateModel(input, interfaces.DraftItems{}, proposal)
		if err != nil {
			t.Fatal(err)
		}
		object := result.ObjectTypes[0]
		if object.Adopted || object.Document.Layer != "entity" || object.Document.SelectionID != "lock" || object.Document.SelectionRevision != 7 || object.Document.Citations[0].Quote != "" {
			t.Fatal("AI adopted or unverified source metadata persisted")
		}
		found := false
		for _, p := range object.Properties {
			if p.Name == "source_quote" {
				found = p.ReadOnly
			}
		}
		if !found || proposal.ObjectTypes[0].Document.Citations[0].Quote == "" {
			t.Fatal("fixed columns missing or transient proposal mutated")
		}
	}
}
func TestInvalidDocumentModelsDoNotReachCandidates(t *testing.T) {
	for _, name := range []string{"quote", "instruction", "property_quote", "readonly", "type", "table", "role", "layer", "event", "duplicate", "shadow"} {
		t.Run(name, func(t *testing.T) {
			input := modelInput("订单 O-1 于今日交付。")
			object := proposedObject(input, "order_doc", "entity")
			existing := interfaces.DraftItems{}
			proposal := kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{object}}
			switch name {
			case "quote":
				object.Document.Citations[0].Quote = "来源没有这句"
			case "instruction":
				object.Properties[0].Extraction.Instruction = ""
			case "property_quote":
				object.Properties[0].Extraction.Examples = nil
			case "readonly":
				object.Properties[0].Name = "source_quote"
			case "type":
				object.Properties[0].Type = "unknown"
			case "table":
				object.Table.ResourceID = "fabricated"
			case "role":
				object.Document.Role = "evidence"
			case "layer":
				object.Document.Layer = "evidence"
			case "event":
				object.Document.Role = "event"
			case "duplicate":
				proposal.ObjectTypes = append(proposal.ObjectTypes, object)
			case "shadow":
				existing.ObjectTypes = []*interfaces.DraftObjectType{{ID: "order_doc"}}
			}
			if result, err := ValidateModel(input, existing, proposal); err == nil || result != nil {
				t.Fatal("invalid model reached candidates")
			}
		})
	}
}
func TestEventsRequireTimeAndCheckedParticipantRelations(t *testing.T) {
	input := modelInput("订单 O-1 在 2030 年 2 月 1 日变更交期。")
	event := proposedObject(input, "delivery_change", "event")
	event.Properties = append(event.Properties, &interfaces.DraftProperty{Name: "occurred_at", DisplayName: "发生时间", Type: "datetime", Extraction: &interfaces.DraftExtractionField{Instruction: "提取变更发生时间", Examples: []interfaces.DraftSourceCitation{documentQuote(input)}}})
	event.Document.TimeProperty = "occurred_at"
	event.Document.ParticipantRelations = []string{"affects_order"}
	relation := &interfaces.DraftRelationType{ID: "affects_order", Name: "影响订单", Source: "delivery_change", Target: "order", Cardinality: "many_to_one", Document: &interfaces.DraftDocumentModel{Role: "relation", Reason: "变更明确针对指定订单", Citations: []interfaces.DraftSourceCitation{documentQuote(input)}}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "order_no"}}
	existing := interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "order", PrimaryKeys: []string{"order_no"}, Properties: []*interfaces.DraftProperty{{Name: "order_no", Type: "string"}}}}}
	proposal := kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{event}, RelationTypes: []*interfaces.DraftRelationType{relation}}
	result, err := ValidateModel(input, existing, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if result.ObjectTypes[0].PrimaryKeys[0] != "_id" || result.RelationTypes[0].Adopted {
		t.Fatal("event identity or adoption untrusted")
	}
	relation.DocumentMapping.IdentityProperty = "unknown"
	if _, err := ValidateModel(input, existing, proposal); err == nil {
		t.Fatal("unchecked linking accepted")
	}
}

func TestSameDocumentRelationsCanConnectObjectsOfTheSameType(t *testing.T) {
	input := modelInput("部件 A 包含子部件 B。")
	object := proposedObject(input, "component", "in_document")
	object.Properties = append(object.Properties, &interfaces.DraftProperty{Name: "parent_number", DisplayName: "上级部件编号", Type: "string", Extraction: &interfaces.DraftExtractionField{Instruction: "提取当前条目明确提到的上级编号", Examples: []interfaces.DraftSourceCitation{documentQuote(input)}}})
	relation := &interfaces.DraftRelationType{ID: "component_parent", Name: "属于上级部件", Source: "component", Target: "component", Cardinality: "many_to_one", Document: &interfaces.DraftDocumentModel{Role: "relation", Reason: "同一文档内条目通过上级编号关联", Citations: []interfaces.DraftSourceCitation{documentQuote(input)}}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "same_document", SourceNameProperty: "parent_number", TargetNameProperty: "number"}}
	proposal := kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{object}, RelationTypes: []*interfaces.DraftRelationType{relation}}
	result, err := ValidateModel(input, interfaces.DraftItems{}, proposal)
	if err != nil {
		t.Fatal("same-type hierarchy is a valid document relationship", err)
	}
	if len(result.RelationTypes[0].Mapping) != 2 || result.RelationTypes[0].Mapping[0].SourceProperty != "document_id" {
		t.Fatal("same-document constraint missing")
	}
	relation.DocumentMapping.SourceNameProperty = "number"
	if _, err := ValidateModel(input, interfaces.DraftItems{}, proposal); err == nil {
		t.Fatal("trivial identity-to-itself mapping must not be proposed as a hierarchy")
	}
}
