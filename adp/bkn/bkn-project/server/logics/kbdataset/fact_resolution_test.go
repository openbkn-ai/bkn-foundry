// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestFactFingerprintIncludesAuthorityButNotUnconfirmedChoicesOrAuditRefresh(t *testing.T) {
	object := &interfaces.DraftObjectType{ID: "fact", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "fact"}}
	draft := &interfaces.Draft{ProjectID: "project", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{object}}}
	original, err := ModelFingerprint(draft)
	if err != nil {
		t.Fatal(err)
	}
	object.Document.FactResolution = &interfaces.FactResolutionRule{ValueProperty: "delivery", SubjectRelationID: "for_order", State: "proposed"}
	proposed, err := ModelFingerprint(draft)
	if err != nil || proposed != original {
		t.Fatal("unfinished choice changed execution model")
	}
	proof := object.Document.FactResolution
	proof.ConfirmedBy = interfaces.AccountInfo{ID: "reviewer", Type: "user"}
	proof.ConfirmedAt = 100
	proof.RuleSignature = "semantic-rule"
	proof.State = "confirmed"
	confirmed, err := ModelFingerprint(draft)
	if err != nil || confirmed == original {
		t.Fatal("confirmed fact rule absent from model fingerprint")
	}
	proof.ConfirmedAt = 200
	proof.ConfirmedBy.ID = "another-reviewer"
	proof.State = "stale"
	refreshed, err := ModelFingerprint(draft)
	if err != nil || refreshed != confirmed {
		t.Fatal("audit or projection changed execution model")
	}
}
