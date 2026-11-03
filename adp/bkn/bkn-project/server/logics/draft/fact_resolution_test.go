// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestFactRuleOnlyChangeRemainsInTheExistingHumanDecisionSnapshot(t *testing.T) {
	object := &interfaces.DraftObjectType{Document: &interfaces.DraftDocumentModel{Role: "fact"}}
	before := snapshot(object)
	object.Document.FactResolution = &interfaces.FactResolutionRule{ValueProperty: "delivery", SubjectRelationID: "for_order", ConfirmedBy: interfaces.AccountInfo{ID: "reviewer", Type: "user"}, ConfirmedAt: 100}
	after := snapshot(object)
	if snapshotsEqual(before, after) || after.Document.FactResolution == nil || !after.Document.FactResolution.Confirm || after.Document.FactResolution.ValueProperty != "delivery" {
		t.Fatal("fact rule decision lost")
	}
}
