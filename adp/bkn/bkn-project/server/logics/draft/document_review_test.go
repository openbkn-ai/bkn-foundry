// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestBlockedRelationCannotBeAdoptedOrCompiledEvenWithForgedAdoption(t *testing.T) {
	for _, reason := range []string{"target_identity", "same_document_scope"} {
		items := compileFixture()
		relation := items.RelationTypes[0]
		relation.Adopted = false
		relation.DocumentRepair = reason
		adopt := true
		if _, err := applyEdit(context.Background(), items, interfaces.DRAFT_KIND_RELATION_TYPES, relation.ID, interfaces.DraftItemEdit{Adopted: &adopt}); err == nil || relation.Adopted {
			t.Fatal("blocked relation adopted")
		}
		relation.Adopted = true
		if _, err := compileDraft(items); err == nil {
			t.Fatal("forged adopted relation compiled")
		}
	}
}
func TestEventCannotBypassBlockedParticipantByLeavingItsRelationUnadopted(t *testing.T) {
	items := compileFixture()
	relation := items.RelationTypes[0]
	relation.Adopted = false
	relation.DocumentRepair = "target_identity"
	event := items.ObjectTypes[0]
	event.Document = &interfaces.DraftDocumentModel{Role: "event", ParticipantRelations: []string{relation.ID}}
	event.Adopted = false
	adopt := true
	if _, err := applyEdit(context.Background(), items, interfaces.DRAFT_KIND_OBJECT_TYPES, event.ID, interfaces.DraftItemEdit{Adopted: &adopt}); err == nil || event.Adopted {
		t.Fatal("event bypassed blocked participant")
	}
	event.Adopted = true
	if _, err := compileDraft(items); err == nil {
		t.Fatal("forged event bypassed blocked participant")
	}
}
func TestRepairOnlySaveCreatesDistinctAuditSnapshot(t *testing.T) {
	relation := &interfaces.DraftRelationType{ID: "link", Name: "References", DocumentRepair: "target_identity"}
	before := relationSnapshot(relation)
	relation.DocumentRepair = ""
	if sameJoin(before, relationSnapshot(relation)) {
		t.Fatal("repair-only decision disappeared")
	}
}
