// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestVerifyBindingsRejectsAnotherPreparedGenerationAndPhysicalField(t *testing.T) {
	object := layoutObject("entity")
	object.PrimaryKeys = []string{"number"}
	object.Document.SelectionID = "selection"
	object.Document.SelectionRevision = 1
	draft := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{object}}}
	owner := interfaces.AccountInfo{ID: "owner", Type: "user"}
	first, err := NewPlan(draft, "first", "catalog", owner)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindPreview(draft, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBindings(bound, first); err != nil {
		t.Fatal("correct physical binding rejected", err)
	}
	second, err := NewPlan(draft, "second", "catalog", owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBindings(bound, second); err == nil {
		t.Fatal("another Dataset generation accepted")
	}
	bound.Items.ObjectTypes[0].Properties[0].MappedField = "wrong_field"
	if err := VerifyBindings(bound, first); err == nil {
		t.Fatal("wrong physical field accepted")
	}
}
