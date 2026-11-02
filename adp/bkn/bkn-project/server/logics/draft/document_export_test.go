// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

func TestPreparedDocumentDefinitionsLoadWithStableIdentityAndEvidenceEdges(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "user_field_collision"}[collision], func(t *testing.T) {
			file := &interfaces.DraftObjectType{ID: "document", Name: "Document", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "evidence_document", SelectionID: "selection", SelectionRevision: 1}, PrimaryKeys: []string{"_id"}, DisplayKey: "_id", Properties: []*interfaces.DraftProperty{{Name: "_id", DisplayName: "Record identifier", Type: "string", ReadOnly: true}, {Name: "document_id", DisplayName: "Document", Type: "string", ReadOnly: true}}}
			block := &interfaces.DraftObjectType{ID: "block", Name: "Block", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "evidence_block", SelectionID: "selection", SelectionRevision: 1}, PrimaryKeys: []string{"_id"}, DisplayKey: "_id", Properties: []*interfaces.DraftProperty{{Name: "_id", DisplayName: "Record identifier", Type: "string", ReadOnly: true}, {Name: "document_id", DisplayName: "Document", Type: "string", ReadOnly: true}}}
			if collision {
				file.Properties = append(file.Properties, &interfaces.DraftProperty{Name: "bkn_record_id", DisplayName: "User record reference", Type: "string"})
			}
			d := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{file, block}, RelationTypes: []*interfaces.DraftRelationType{
				{ID: "has_blocks", Name: "Has blocks", Adopted: true, Source: "document", Target: "block", Cardinality: "one_to_many", Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "evidence"}, Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "_id", TargetProperty: "document_id"}}},
				{ID: "related_blocks", Name: "Related blocks", Adopted: true, Source: "document", Target: "block", Cardinality: "many_to_many", Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "evidence"}},
			}}}
			plan, err := kbdataset.NewPlan(d, "generation", "catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
			if err != nil {
				t.Fatal(err)
			}
			for i := range plan.Datasets {
				plan.Datasets[i].Ready = true
			}
			bound, err := kbdataset.BindPreview(d, plan)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(bound)
			compiled, err := compileDraft(&bound.Items)
			if err != nil {
				t.Fatal(err)
			}
			network, err := loadWithBranch(compiled.Files)
			if err != nil {
				t.Fatal(err)
			}
			if err := bkn.ValidateNetworkDeclarations(network); err != nil {
				t.Fatal(err)
			}
			if validation := bkn.ValidateNetwork(network); !validation.OK() {
				t.Fatalf("BKN semantic validation failed: %+v", validation.Errors)
			}
			alias := "bkn_record_id"
			if collision {
				alias += "_2"
			}
			objects := map[string]*bkn.BknObjectType{}
			for _, o := range network.ObjectTypes {
				objects[o.ID] = o
			}
			if objects["document"].PrimaryKeys[0] != alias || objects["document"].DisplayKey != alias {
				t.Fatal("identity references were not translated")
			}
			for _, property := range objects["document"].DataProperties {
				if property.Name == "_id" {
					t.Fatal("reserved internal identifier escaped into BKN")
				}
				if property.Name == alias && property.MappedField != alias {
					t.Fatal("stable storage mirror lost")
				}
			}
			for _, relation := range network.RelationTypes {
				if relation.ID == "has_blocks" {
					rules := relation.MappingRules.(bkn.DirectMappingRule)
					if rules[0].SourceProperty != alias || rules[0].TargetProperty != "document_id" {
						t.Fatal("direct evidence endpoint translation failed")
					}
				} else {
					rules := relation.MappingRules.(*bkn.InDirectMappingRule)
					if rules.SourceMappingRules[0].SourceProperty != alias || rules.SourceMappingRules[0].TargetProperty != "source_record_id" || rules.TargetMappingRules[0].SourceProperty != "target_record_id" || rules.TargetMappingRules[0].TargetProperty != "bkn_record_id" {
						t.Fatal("indirect evidence endpoint or backing field changed incorrectly")
					}
				}
			}
			after, _ := json.Marshal(bound)
			if string(before) != string(after) {
				t.Fatal("export mutated the logical model or its fingerprint inputs")
			}
		})
	}
}

func TestDocumentExportRefusesUnpreparedOrConflictingIdentity(t *testing.T) {
	for _, scenario := range []string{"unprepared", "conflict", "invalid_name", "editable"} {
		t.Run(scenario, func(t *testing.T) {
			property := &interfaces.DraftProperty{Name: "_id", Type: "string", ReadOnly: true, MappedField: "bkn_record_id"}
			object := &interfaces.DraftObjectType{ID: "source", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "file"}, Properties: []*interfaces.DraftProperty{property}}
			switch scenario {
			case "unprepared":
				property.MappedField = ""
			case "conflict":
				object.Properties = append(object.Properties, &interfaces.DraftProperty{Name: "bkn_record_id"})
			case "invalid_name":
				property.MappedField = "_id"
			case "editable":
				property.ReadOnly = false
			}
			if _, err := documentExportItems(&interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{object}}); err == nil {
				t.Fatal("unverified mirror accepted")
			}
		})
	}
}
