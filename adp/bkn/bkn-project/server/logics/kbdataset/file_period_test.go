// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestFilePeriodMappingChangesTheModelWhileAuditRefreshDoesNot(t *testing.T) {
	object := preparationObject("file")
	draft := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{object}}}
	original, err := ModelFingerprint(draft)
	if err != nil {
		t.Fatal(err)
	}
	object.Document.FilePeriod = &interfaces.FileEffectivePeriod{From: "starts", ConfirmedBy: interfaces.AccountInfo{ID: "reviewer", Type: "user"}, ConfirmedAt: 10}
	changed, err := ModelFingerprint(draft)
	if err != nil || original == changed {
		t.Fatal("date mapping was excluded from the model signature", err)
	}
	object.Document.FilePeriod.ConfirmedBy.ID, object.Document.FilePeriod.ConfirmedAt = "another-reviewer", 20
	refreshed, err := ModelFingerprint(draft)
	if err != nil || changed != refreshed {
		t.Fatal("audit refresh invalidated the same date mapping", err)
	}
	object.Document.FilePeriod.Until = "ends"
	revised, err := ModelFingerprint(draft)
	if err != nil || changed == revised {
		t.Fatal("changed date boundary kept the old extraction signature", err)
	}
}
