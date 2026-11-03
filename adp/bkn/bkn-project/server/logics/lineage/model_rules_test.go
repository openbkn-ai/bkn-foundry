// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestConfirmedModelCommitsRulesBesideDefinitionsWithoutExportingThem(t *testing.T) {
	ctx := context.Background()
	id := newLineageID()
	const branch = "project/p1"
	base := definitions("A part.", "A supplier.")
	base["network.bkn"] = []byte(strings.Replace(networkText, "name: Supply chain\n", "name: Supply chain\nbkn_spec: \"3.0\"\n", 1))
	if _, err := realGS.InitLineage(ctx, id, base, "init", author); err != nil {
		t.Fatal(err)
	}
	svc := &lineageService{gs: realGS}
	if _, err := svc.CreateBranch(ctx, id, branch, ""); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"object_types/plant.bkn": objectType("plant", "Plant", "A plant.")}
	rules := []byte(`{"schema":1,"objects":[],"relations":[]}`)
	result, err := svc.AddModelDefinitions(ctx, id, branch, files, rules, nil, "confirm", author)
	if err != nil {
		t.Fatal(err)
	}
	modelStore, ok := realGS.(interfaces.ModelGitStore)
	if !ok {
		t.Fatal("missing real model store")
	}
	saved, err := modelStore.ReadModelRules(ctx, id, result.Commit)
	if err != nil || !bytes.Equal(saved, rules) {
		t.Fatal("confirmation did not persist rules", err)
	}
	exported, err := realGS.ReadTree(ctx, id, result.Commit)
	if err != nil || len(exported) != len(base)+1 {
		t.Fatal("rule metadata mixed into BKN export", err)
	}
	// A legacy definition edit preserves the independently committed model rules.
	if _, err := svc.AddDefinitions(ctx, id, branch, files, []string{"object_types/plant.bkn"}, "same", author); err != nil {
		t.Fatal(err)
	}
	saved, err = modelStore.ReadModelRules(ctx, id, "refs/heads/"+branch)
	if err != nil || !bytes.Equal(saved, rules) {
		t.Fatal("legacy edit discarded rules", err)
	}
}
