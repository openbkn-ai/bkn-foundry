// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func unreachable(t *testing.T, err error) {
	t.Helper()
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusServiceUnavailable {
		t.Fatal("want the source out of reach, got", err)
	}
}

// Without an edition, the draft owns no document item: one that holds any is refused as a source
// out of reach, and table items go on as they always did.
func TestCommunityDraftsOwnNoDocumentItem(t *testing.T) {
	defer projecthooks.SetForTest(projecthooks.Hooks{})()
	hooks := documentHooks()
	if _, ok := hooks.(noDocumentHooks); !ok {
		t.Fatal("community default not in use")
	}
	ctx := context.Background()
	table := &interfaces.DraftObjectType{ID: "orders"}
	document := &interfaces.DraftObjectType{ID: "contract_doc", Document: &interfaces.DraftDocumentModel{Role: "entity"}}

	if err := hooks.CheckRead(ctx, token, &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{table}}}); err != nil {
		t.Fatal(err)
	}
	unreachable(t, hooks.CheckRead(ctx, token, &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{table, document}}}))
	unreachable(t, hooks.CheckRead(ctx, token, &interfaces.Draft{Items: interfaces.DraftItems{RelationTypes: []*interfaces.DraftRelationType{{ID: "r", Document: &interfaces.DraftDocumentModel{Role: "relation"}}}}}))

	// A table item ignores document settings; deriving a document item from it is out of reach.
	if _, handled, err := hooks.SaveObject(ctx, nil, token, nil, table, interfaces.DraftObjectTypeSave{Document: &interfaces.DraftDocumentSettings{}}, caller); handled || err != nil {
		t.Fatal("table save was taken over", handled, err)
	}
	_, handled, err := hooks.SaveObject(ctx, nil, token, nil, table, interfaces.DraftObjectTypeSave{Document: &interfaces.DraftDocumentSettings{Derive: &interfaces.DraftDocumentDerivation{}}}, caller)
	if !handled {
		t.Fatal("derive was not refused")
	}
	unreachable(t, err)
	_, _, err = hooks.SaveObject(ctx, nil, token, nil, document, interfaces.DraftObjectTypeSave{}, caller)
	unreachable(t, err)

	if _, handled, err := hooks.CreateRelation(ctx, nil, token, nil, nil, interfaces.DraftRelationTypeSave{}, caller); handled || err != nil {
		t.Fatal("table relation was taken over", handled, err)
	}
	_, _, err = hooks.CreateRelation(ctx, nil, token, nil, nil, interfaces.DraftRelationTypeSave{DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking"}}, caller)
	unreachable(t, err)

	asked, err := hooks.AskObject(ctx, nil, token, nil, table, table)
	if err != nil || asked.Working != table || asked.Editable != nil {
		t.Fatal("table ask", asked, err)
	}
	_, err = hooks.AskObject(ctx, nil, token, nil, document, document)
	unreachable(t, err)
	if id := hooks.AskAgentID(&interfaces.Draft{}); id != "" {
		t.Fatal("agent", id)
	}
	draft := &interfaces.Draft{}
	if prepared, err := hooks.PrepareConfirm(ctx, nil, token, nil, draft, caller); prepared != draft || err != nil {
		t.Fatal("confirm", err)
	}
}
