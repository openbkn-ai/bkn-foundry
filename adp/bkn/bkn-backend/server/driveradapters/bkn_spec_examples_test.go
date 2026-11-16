// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// specExampleNetworks lists every published example network that defines action types:
// the specification examples and the package the integration tests import.
func specExampleNetworks(t *testing.T) []string {
	t.Helper()
	var dirs []string
	for _, pattern := range []string{
		filepath.Join("..", "bkn-specification", "examples", "*"),
		filepath.Join("..", "..", "tests", "integration_tests", "bkn", "helpers", "examples", "*"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, dir := range matches {
			if info, err := os.Stat(filepath.Join(dir, "action_types")); err == nil && info.IsDir() {
				dirs = append(dirs, dir)
			}
		}
	}
	if len(dirs) == 0 {
		t.Fatal("no example network with action types was found")
	}
	return dirs
}

// The examples are what users copy. Each action type in them must pass the same
// validation an imported package goes through, in strict mode.
func TestSpecExampleActionTypesPassImportValidation(t *testing.T) {
	for _, dir := range specExampleNetworks(t) {
		name := filepath.Base(filepath.Dir(filepath.Dir(dir))) + "/" + filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			network, err := bknsdk.LoadNetwork(dir)
			if err != nil {
				t.Fatalf("load %s: %v", dir, err)
			}
			kn := logics.ToADPNetWork(network)
			actionTypes := make([]*interfaces.ActionType, 0, len(network.ActionTypes))
			for _, action := range network.ActionTypes {
				actionTypes = append(actionTypes, logics.ToADPActionType(kn.KNID, kn.Branch, action))
			}
			if len(actionTypes) == 0 {
				t.Fatalf("%s has an action_types directory but no action types were loaded", dir)
			}
			for _, action := range actionTypes {
				if err := ValidateActionType(context.Background(), action, true); err != nil {
					t.Errorf("action type %s: %v", action.ATID, err)
				}
			}
		})
	}
}
