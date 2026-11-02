// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"testing"
)

func TestDocumentRelationAdoptionRequiresTheConfirmedTargetIdentity(t *testing.T) {
	source := &interfaces.DraftObjectType{ID: "source", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "entity"}, Properties: []*interfaces.DraftProperty{{Name: "party", Type: "string"}}}
	target := &interfaces.DraftObjectType{ID: "target", Adopted: true, NeedsIdentity: true, Properties: []*interfaces.DraftProperty{{Name: "name", Type: "string"}, {Name: "code", Type: "string"}}}
	relation := &interfaces.DraftRelationType{ID: "involves", Source: source.ID, Target: target.ID, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "party", IdentityProperty: "name"}}
	items := &interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{source, target}, RelationTypes: []*interfaces.DraftRelationType{relation}}
	adopt := true
	edit := interfaces.DraftItemEdit{Adopted: &adopt}
	for _, keys := range [][]string{nil, {"code"}} {
		target.PrimaryKeys = keys
		target.NeedsIdentity = len(keys) == 0
		_, err := applyEdit(context.Background(), items, interfaces.DRAFT_KIND_RELATION_TYPES, relation.ID, edit)
		var problem *httperrors.HTTPError
		if !errors.As(err, &problem) || problem.BaseError.ErrorCode != "BknProject.Draft.NeedsIdentity" || relation.Adopted {
			t.Fatal("unconfirmed or mismatched identity adopted", err)
		}
	}
	target.PrimaryKeys = []string{"name"}
	target.NeedsIdentity = false
	if _, err := applyEdit(context.Background(), items, interfaces.DRAFT_KIND_RELATION_TYPES, relation.ID, edit); err != nil || !relation.Adopted {
		t.Fatal("confirmed matching identity blocked", err)
	}
}
