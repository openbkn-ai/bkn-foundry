// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type modelConfirmationLineage struct {
	interfaces.LineageService
	rules []byte
	files map[string][]byte
}

func (l *modelConfirmationLineage) AddModelDefinitions(_ context.Context, _ string, _ string, files map[string][]byte, rules []byte, _ []string, _ string, _ interfaces.GitAuthor) (*interfaces.CommitDefinitionsResult, error) {
	l.rules = rules
	l.files = files
	return &interfaces.CommitDefinitionsResult{Commit: "confirmed-model"}, nil
}

// rulesHooks is an edition whose document items carry rules beside their definitions.
type rulesHooks struct{ noDocumentHooks }

func (rulesHooks) DefinitionRules(_ context.Context, draft *interfaces.Draft, _ bool) ([]byte, error) {
	for _, o := range draft.Items.ObjectTypes {
		if o.Document != nil {
			return []byte(`{"rules":"edition"}`), nil
		}
	}
	return nil, nil
}

// A confirmation commits what the hooks give as rules beside the definition files, unchanged,
// through the existing lineage; with no rules, none is kept.
func TestConfirmationCommitsRulesThroughExistingLineage(t *testing.T) {
	defer projecthooks.SetForTest(projecthooks.Hooks{Drafts: rulesHooks{}})()
	model := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "asset_doc", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "file"}}}}}
	lineage := &modelConfirmationLineage{}
	service := &draftService{ls: lineage}
	entry := &interfaces.ProjectEntry{Project: &interfaces.Project{}}
	entry.KNID = "network"
	entry.Branch = "project/p"
	files := map[string][]byte{"object_types/asset_doc.bkn": []byte("compiled definition")}
	result, err := service.commitModelDefinitions(context.Background(), entry, model, files, nil, "confirm", interfaces.GitAuthor{Name: "u"})
	if err != nil || result.Commit != "confirmed-model" {
		t.Fatal("confirmation failed", err)
	}
	if string(lineage.rules) != `{"rules":"edition"}` {
		t.Fatal("rules", string(lineage.rules))
	}
	if string(lineage.files["object_types/asset_doc.bkn"]) != "compiled definition" {
		t.Fatal("BKN artifact changed")
	}
	model.Items.ObjectTypes[0].Document = nil
	if _, err := service.commitModelDefinitions(context.Background(), entry, model, files, nil, "confirm", interfaces.GitAuthor{}); err != nil || len(lineage.rules) != 0 {
		t.Fatal("obsolete rule artifact retained", err)
	}
}
