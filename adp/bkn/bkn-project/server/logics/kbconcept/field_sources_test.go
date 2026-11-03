// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestOnlyExplicitHumanChoicesCanChangePureDocumentEntityFieldAuthority(t *testing.T) {
	for _, scenario := range []string{"fusion", "document", "unconfirmed", "table", "managed", "identity", "unknown", "duplicate", "file_role", "dual_source", "app"} {
		t.Run(scenario, func(t *testing.T) {
			object := &interfaces.DraftObjectType{Document: &interfaces.DraftDocumentModel{Role: "entity"}, PrimaryKeys: []string{"number"}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string", SourceOfRecord: "document"}, {Name: "amount", Type: "decimal", SourceOfRecord: "document"}, {Name: "_id", Type: "string", ReadOnly: true}}}
			caller := interfaces.AccountInfo{ID: "owner", Type: "user"}
			decisions := []interfaces.FieldSourceDecision{{Property: "amount", SourceOfRecord: "fusion", Confirm: true}}
			switch scenario {
			case "document":
				object.Properties[1].SourceOfRecord = "fusion"
				decisions[0].SourceOfRecord = "document"
			case "unconfirmed":
				decisions[0].Confirm = false
			case "table":
				decisions[0].SourceOfRecord = "table"
			case "managed":
				decisions[0].Property = "_id"
			case "identity":
				decisions[0].Property = "number"
			case "unknown":
				decisions[0].Property = "invented"
			case "duplicate":
				decisions = append(decisions, decisions[0])
			case "file_role":
				object.Document.Role = "file"
			case "dual_source":
				object.Document.DualSource = &interfaces.DraftDualSourcePlan{}
			case "app":
				caller.Type = "app"
			}
			before := object.Properties[1].SourceOfRecord
			err := ConfirmFieldSources(object, decisions, caller)
			valid := scenario == "fusion" || scenario == "document"
			if (err == nil) != valid {
				t.Fatal("incorrect authority boundary", scenario, err)
			}
			if !valid && object.Properties[1].SourceOfRecord != before {
				t.Fatal("rejected decision partially changed authority")
			}
			if valid && object.Properties[1].SourceOfRecord != decisions[0].SourceOfRecord {
				t.Fatal("human choice lost")
			}
		})
	}
}

func TestAIProposalCannotSelectFusionAuthority(t *testing.T) {
	input := modelInput("Contract C-1 states the agreed amount.")
	object := proposedObject(input, "contract", "entity")
	object.Properties[0].SourceOfRecord = "fusion"
	checked, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{object}})
	if err != nil || checked.ObjectTypes[0].Properties[0].SourceOfRecord != "document" {
		t.Fatal("AI selected field authority", err)
	}
}
