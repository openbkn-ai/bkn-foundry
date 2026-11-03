// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"bytes"
	"context"
	"testing"

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
func TestConfirmationCommitsRulesThroughExistingLineageWithoutSourceEvidence(t *testing.T) {
	actor := interfaces.AccountInfo{ID: "private-reviewer", Type: "user"}
	model := &interfaces.Draft{ProjectID: "p", Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "asset_doc", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "file", Layer: "fact", Reason: "private reasoning", Citations: []interfaces.DraftSourceCitation{{Quote: "private source"}}, FileIdentity: &interfaces.FileBusinessIdentity{Property: "number", ConfirmedBy: actor, ConfirmedAt: 12}}, Properties: []*interfaces.DraftProperty{{Name: "number", Extraction: &interfaces.DraftExtractionField{Instruction: "Read the explicit number", Examples: []interfaces.DraftSourceCitation{{Quote: "private source"}}}}}}}}}
	lineage := &modelConfirmationLineage{}
	service := &draftService{ls: lineage}
	entry := &interfaces.ProjectEntry{Project: &interfaces.Project{}}
	entry.KNID = "network"
	entry.Branch = "project/p"
	files := map[string][]byte{"object_types/asset_doc.bkn": []byte("compiled definition")}
	result, err := service.commitModelDefinitions(context.Background(), entry, model, files, nil, "confirm", interfaces.GitAuthor{Name: actor.ID})
	if err != nil || result.Commit != "confirmed-model" {
		t.Fatal("confirmation failed", err)
	}
	for _, secret := range []string{"private source", "private reasoning", "private-reviewer", "citations", "examples", "confirmed_at"} {
		if bytes.Contains(lineage.rules, []byte(secret)) {
			t.Fatal("private process material sent to Git", secret)
		}
	}
	if !bytes.Contains(lineage.rules, []byte("Read the explicit number")) || !bytes.Contains(lineage.rules, []byte(`"file_identity": "number"`)) {
		t.Fatal("confirmed execution rule lost")
	}
	if string(lineage.files["object_types/asset_doc.bkn"]) != "compiled definition" {
		t.Fatal("BKN artifact changed")
	}
	// Removing document types removes only the owned document rule artifact.
	model.Items.ObjectTypes[0].Document = nil
	if _, err := service.commitModelDefinitions(context.Background(), entry, model, files, nil, "confirm", interfaces.GitAuthor{}); err != nil || len(lineage.rules) != 0 {
		t.Fatal("obsolete rule artifact retained", err)
	}
}
