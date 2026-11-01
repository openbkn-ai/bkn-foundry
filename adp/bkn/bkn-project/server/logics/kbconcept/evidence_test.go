// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestEvidenceTemplatesAreFixedAndIndependentOfOriginalTextRetention(t *testing.T) {
	input := modelInput("业务原文")
	used := interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "source_document"}}}
	first, err := BuildEvidence(input, used)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Model.ObjectTypes) != 3 || len(first.Model.RelationTypes) != 2 || first.DocumentID == "source_document" {
		t.Fatal("fixed template count or ID collision")
	}
	for _, object := range first.Model.ObjectTypes {
		if object.Adopted || object.Document.Layer != "evidence" || len(object.Document.Citations) != 0 {
			t.Fatal("template depended on AI evidence")
		}
		for _, p := range object.Properties {
			if !p.ReadOnly || p.Extraction != nil {
				t.Fatal("template fields are editable/extracted")
			}
		}
	}
	for _, relation := range first.Model.RelationTypes {
		if relation.Cardinality != "one_to_many" || relation.Source != first.DocumentID {
			t.Fatal("document containment direction changed")
		}
	}
	input.Files[0].IncludeOriginal = true
	second, err := BuildEvidence(input, used)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Model.ObjectTypes) != len(first.Model.ObjectTypes) || second.BlockID != first.BlockID {
		t.Fatal("quote switch changed the model instead of row-write policy")
	}
	used.ObjectTypes = append(used.ObjectTypes, first.Model.ObjectTypes...)
	used.RelationTypes = first.Model.RelationTypes
	again, err := BuildEvidence(input, used)
	if err != nil || len(again.Model.ObjectTypes) != 0 || len(again.Model.RelationTypes) != 0 || again.DocumentID != first.DocumentID {
		t.Fatal("repeated extraction duplicates fixed evidence", err)
	}
}

func TestEvidenceLabelsAreBilingualAndTamperedTemplatesAreRefused(t *testing.T) {
	input := modelInput("a")
	input.Request.NamingLanguage = "en-US"
	first, err := BuildEvidence(input, interfaces.DraftItems{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Model.ObjectTypes[0].Name != "Source document" {
		t.Fatal(first.Model.ObjectTypes[0].Name)
	}
	input.Request.NamingLanguage = "zh-CN"
	second, err := BuildEvidence(input, interfaces.DraftItems{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Model.ObjectTypes[0].Name != "来源文档" {
		t.Fatal(second.Model.ObjectTypes[0].Name)
	}
	first.Model.ObjectTypes[0].Properties[0].ReadOnly = false
	if _, err := BuildEvidence(input, interfaces.DraftItems{ObjectTypes: first.Model.ObjectTypes}); err == nil {
		t.Fatal("modified system schema silently reused")
	}
}

func TestDocumentModelsGetCheckedEvidenceLinks(t *testing.T) {
	input := modelInput("合同 C-1 的条款。")
	model, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{proposedObject(input, "contract_file", "file")}})
	if err != nil {
		t.Fatal(err)
	}
	existing := interfaces.DraftItems{ObjectTypes: model.ObjectTypes}
	evidence, err := BuildEvidence(input, existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Model.RelationTypes) != 4 {
		t.Fatal("model-to-document and model-to-block references missing")
	}
	for _, r := range evidence.Model.RelationTypes {
		if r.Document.Role != "evidence_reference" {
			continue
		}
		if r.Source != "contract_file" || r.Cardinality != "many_to_one" || r.Adopted {
			t.Fatal("wrong evidence reference")
		}
	}
	existing.ObjectTypes = append(existing.ObjectTypes, evidence.Model.ObjectTypes...)
	existing.RelationTypes = evidence.Model.RelationTypes
	again, err := BuildEvidence(input, existing)
	if err != nil || len(again.Model.RelationTypes) != 0 {
		t.Fatal("evidence references duplicated", err)
	}
}

func TestCanonicalEntitiesDoNotGetSingleDocumentEvidenceCardinality(t *testing.T) {
	input := modelInput("同一业务编号可出现在多份文件。")
	model, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{proposedObject(input, "business_entity", "entity")}})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := BuildEvidence(input, interfaces.DraftItems{ObjectTypes: model.ObjectTypes})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Model.RelationTypes) != 2 {
		t.Fatal("entity provenance cannot be collapsed to one document/block reference")
	}
	for _, relation := range evidence.Model.RelationTypes {
		if relation.Source == "business_entity" {
			t.Fatal("a canonical entity may have property-level sources from different files")
		}
	}
}
