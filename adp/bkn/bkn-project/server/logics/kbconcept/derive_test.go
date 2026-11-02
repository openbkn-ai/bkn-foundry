// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"encoding/json"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestExplicitDerivationCopiesOnlyExistingGroundedFields(t *testing.T) {
	for _, text := range []string{"订单 O-1 的合同记录。", "检验报告 R-2 的样本记录。", "专利 P-3 的权利记录。"} {
		input := modelInput(text)
		source, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{proposedObject(input, "original_doc", "in_document")}})
		if err != nil {
			t.Fatal(err)
		}
		original := source.ObjectTypes[0]
		before, _ := json.Marshal(original)
		for _, role := range []string{"entity", "file"} {
			req := interfaces.DraftDocumentDerivation{ID: "derived_doc", Name: "已确认的业务记录", Role: role, Fields: []string{"number"}, Identity: "number", Confirm: true}
			if role == "file" {
				req.Identity = ""
			}
			got, err := DeriveObject(input, interfaces.DraftItems{ObjectTypes: source.ObjectTypes}, original, req)
			if err != nil {
				t.Fatal(err)
			}
			if got.Adopted || got.Document.Role != role || got.Document.Citations[0].Quote != "" || got.Properties[0].Extraction.Examples[0].Quote != "" || got.Table.ResourceID != "" {
				t.Fatal("unsafe derivation", got)
			}
			if got.Properties[0].Extraction.Instruction != original.Properties[0].Extraction.Instruction {
				t.Fatal("invented extraction rule")
			}
			if role == "file" && got.PrimaryKeys[0] != "_id" {
				t.Fatal("file lost scoped identity")
			}
		}
		after, _ := json.Marshal(original)
		if string(before) != string(after) {
			t.Fatal("original proposal changed")
		}
	}
}
func TestDerivationFailsClosedOnUnconfirmedInventedOrStaleInputs(t *testing.T) {
	for _, scenario := range []string{"unconfirmed", "unknown_field", "managed_field", "duplicate_field", "missing_identity", "identity_not_selected", "collision", "unsupported_role", "stale_selection", "stale_revision", "bad_quote", "system_source"} {
		t.Run(scenario, func(t *testing.T) {
			input := modelInput("记录 R-1。")
			checked, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{proposedObject(input, "original_doc", "in_document")}})
			if err != nil {
				t.Fatal(err)
			}
			original := checked.ObjectTypes[0]
			req := interfaces.DraftDocumentDerivation{ID: "derived_doc", Name: "业务记录", Role: "entity", Fields: []string{"number"}, Identity: "number", Confirm: true}
			switch scenario {
			case "unconfirmed":
				req.Confirm = false
			case "unknown_field":
				req.Fields = []string{"new_field"}
			case "managed_field":
				req.Fields = []string{"document_id"}
			case "duplicate_field":
				req.Fields = []string{"number", "number"}
			case "missing_identity":
				req.Identity = ""
			case "identity_not_selected":
				req.Identity = "unknown"
			case "collision":
				req.ID = original.ID
			case "unsupported_role":
				req.Role = "event"
			case "stale_selection":
				input.SelectionID = "changed"
			case "stale_revision":
				input.SelectionRevision++
			case "bad_quote":
				original.Document.Citations[0].Quote = "invented"
			case "system_source":
				original.Document = nil
			}
			if got, err := DeriveObject(input, interfaces.DraftItems{ObjectTypes: checked.ObjectTypes}, original, req); err == nil || got != nil {
				t.Fatal("unsafe derivation accepted")
			}
		})
	}
}
