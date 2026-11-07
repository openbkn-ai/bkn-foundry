// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Adding tables to a draft that holds only an item an edition made from documents keeps that item,
// its adoption and its decisions, and leaves the draft read untouched.
func TestAddingStructuredCatalogPreservesDocumentOnlyDraft(t *testing.T) {
	original := &interfaces.Draft{ProjectID: "p", Status: "ready", UpdateTime: 10, Items: interfaces.DraftItems{
		Inputs: &interfaces.DraftInputs{NamingLanguage: "en-US"},
		ObjectTypes: []*interfaces.DraftObjectType{{ID: "order_doc", Name: "Order", PrimaryKeys: []string{"number"}, Adopted: true,
			Document:   &interfaces.DraftDocumentModel{Role: "entity", Reason: "Explicit order number"},
			Properties: []*interfaces.DraftProperty{{Name: "number", DisplayName: "Number", Type: "string", Extraction: &interfaces.DraftExtractionField{Instruction: "Read the explicit order number"}}}}},
		Decisions: []*interfaces.DraftDecision{{Action: interfaces.DRAFT_DECISION_ADOPT, Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "order_doc"}},
	}}
	before, _ := json.Marshal(original.Items)
	table := membersTable()
	drafted, err := newDrafter(testSetting, sampleCounter()).draft(context.Background(), []*interfaces.VegaTable{table}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &draftService{setting: testSetting}
	merged := service.applyAdded(original.Items, &addedDraft{drafted: drafted, tables: []*interfaces.VegaTable{table}}, []string{"c1"}, &interfaces.DraftCatalogsAdded{})
	if len(merged.ObjectTypes) != 2 || merged.ObjectTypes[0].Document == nil || !merged.ObjectTypes[0].Adopted || merged.Inputs.NamingLanguage != "en-US" || len(merged.Inputs.Tables) != 1 || len(merged.Inputs.CatalogIDs) != 1 || len(merged.Decisions) != 1 {
		t.Fatal("adding tables damaged document model or its decisions")
	}
	after, _ := json.Marshal(original.Items)
	if string(before) != string(after) {
		t.Fatal("source draft mutated while merging")
	}
}
