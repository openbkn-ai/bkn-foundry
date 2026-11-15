// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	rootlogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// The example action types must also agree with the object and relation types of
// their own network: property types, relation endpoints and primary keys.
func TestSpecExampleActionTypesMatchTheirNetwork(t *testing.T) {
	var dirs []string
	for _, pattern := range []string{
		filepath.Join("..", "..", "bkn-specification", "examples", "*"),
		filepath.Join("..", "..", "..", "tests", "integration_tests", "bkn", "helpers", "examples", "*"),
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
	for _, dir := range dirs {
		name := filepath.Base(filepath.Dir(filepath.Dir(dir))) + "/" + filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			network, err := bknsdk.LoadNetwork(dir)
			if err != nil {
				t.Fatalf("load %s: %v", dir, err)
			}
			batch := &interfaces.BatchIDIndex{
				KNID:          network.ID,
				ObjectTypes:   map[string]*interfaces.ObjectType{},
				RelationTypes: map[string]*interfaces.RelationType{},
			}
			for _, object := range network.ObjectTypes {
				ot := rootlogics.ToADPObjectType(network.ID, network.Branch, object)
				batch.ObjectTypes[ot.OTID] = ot
			}
			for _, relation := range network.RelationTypes {
				rt := rootlogics.ToADPRelationType(network.ID, network.Branch, relation)
				batch.RelationTypes[rt.RTID] = rt
			}
			svc := &actionTypeService{}
			for _, action := range network.ActionTypes {
				at := rootlogics.ToADPActionType(network.ID, network.Branch, action)
				if err := svc.checkOperationDependencies(context.Background(), nil, at, batch); err != nil {
					t.Errorf("action type %s: %v", at.ATID, err)
				}
			}
		})
	}
}
