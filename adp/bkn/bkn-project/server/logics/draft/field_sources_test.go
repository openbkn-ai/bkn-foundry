// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestAuthorityOnlyChangesRemainVisibleInTheExistingDecisionSnapshot(t *testing.T) {
	object := &interfaces.DraftObjectType{Document: &interfaces.DraftDocumentModel{Role: "entity"}, Properties: []*interfaces.DraftProperty{{Name: "amount", Type: "decimal", SourceOfRecord: "document"}}}
	before := snapshot(object)
	object.Properties[0].SourceOfRecord = "fusion"
	after := snapshot(object)
	if snapshotsEqual(before, after) || before.Properties[0].SourceOfRecord != "document" || after.Properties[0].SourceOfRecord != "fusion" {
		t.Fatal("authority-only change lost its human decision audit")
	}
}
