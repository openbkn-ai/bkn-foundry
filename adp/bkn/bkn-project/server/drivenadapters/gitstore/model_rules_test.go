// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package gitstore

import (
	"bytes"
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestModelRulesCommitWithDefinitionsAndStayOutsideBKNAndCapabilities(t *testing.T) {
	ctx := context.Background()
	store := newGitStore(t.TempDir())
	base := map[string][]byte{"network.bkn": []byte("base")}
	if _, err := store.InitLineage(ctx, "assets", base, "init", author); err != nil {
		t.Fatal(err)
	}
	capabilities := map[string][]byte{"functions/f/definition.json": []byte("function"), "release.yaml": []byte("manifest")}
	if _, err := store.CommitCapabilities(ctx, "assets", "main", capabilities, "capabilities", author); err != nil {
		t.Fatal(err)
	}
	rules := []byte(`{"schema":1,"objects":[{"id":"asset_doc","fields":[]}],"relations":[]}`)
	updated := map[string][]byte{"network.bkn": []byte("updated")}
	var head string
	err := store.WithLineageLock(ctx, "assets", func(locked interfaces.GitStore) error {
		modelStore, ok := locked.(interfaces.ModelGitStore)
		if !ok {
			t.Fatal("locked store lost model support")
		}
		var err error
		head, err = modelStore.CommitModel(ctx, "assets", "main", updated, rules, "confirm", author)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := store.open("assets")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := commitAt(repo, head)
	if err != nil {
		t.Fatal(err)
	}
	file, err := commit.File("project/knowledge-base.json")
	if err != nil {
		t.Fatal("rules outside their specified directory", err)
	}
	body, err := file.Contents()
	if err != nil || body != string(rules) {
		t.Fatal("wrong rules", err)
	}
	defs, err := store.ReadTree(ctx, "assets", head)
	if err != nil || len(defs) != 1 || string(defs["network.bkn"]) != "updated" {
		t.Fatal("BKN export mixed with rules", defs, err)
	}
	caps, err := store.ReadCapabilities(ctx, "assets", head)
	if err != nil || len(caps) != 2 || string(caps["release.yaml"]) != "manifest" {
		t.Fatal("capabilities overwritten", caps, err)
	}
	saved, err := store.ReadModelRules(ctx, "assets", head)
	if err != nil || !bytes.Equal(saved, rules) {
		t.Fatal("rules not readable", err)
	}
	if _, err := store.CommitModel(ctx, "assets", "main", map[string][]byte{"../escape": []byte("bad")}, []byte("changed rules"), "invalid", author); err == nil {
		t.Fatal("invalid definition path accepted")
	}
	unchanged, err := store.ResolveRef(ctx, "assets", "main")
	if err != nil || unchanged != head {
		t.Fatal("failed model commit moved the branch", err)
	}

	same, err := store.CommitModel(ctx, "assets", "main", updated, rules, "same", author)
	if err != nil || same != head {
		t.Fatal("unchanged rules created a commit", same, err)
	}
	if err := store.Tag(ctx, "assets", "v1.0.0", head); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitModel(ctx, "assets", "main", updated, nil, "remove document model", author); err != nil {
		t.Fatal(err)
	}
	saved, err = store.ReadModelRules(ctx, "assets", "main")
	if err != nil || len(saved) != 0 {
		t.Fatal("removed rules retained", err)
	}
	saved, err = store.ReadModelRules(ctx, "assets", "v1.0.0")
	if err != nil || !bytes.Equal(saved, rules) {
		t.Fatal("old release changed", err)
	}
}
