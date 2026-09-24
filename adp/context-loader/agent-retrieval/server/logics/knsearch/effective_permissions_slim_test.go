// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func permissionSample() []*interfaces.KnSearchObjectType {
	return []*interfaces.KnSearchObjectType{
		{ConceptID: "open", EffectivePermissions: map[string]interfaces.PropertyAccessLevel{
			"a": interfaces.PropertyAccessFull, "b": interfaces.PropertyAccessFull,
		}},
		{ConceptID: "masked", EffectivePermissions: map[string]interfaces.PropertyAccessLevel{
			"a": interfaces.PropertyAccessFull, "salary": interfaces.PropertyAccessMasked,
		}},
		{ConceptID: "none"},
		nil,
	}
}

// A map that grants full access to every property it lists repeats the property names and says
// nothing else. A map with any restriction is the one worth reading and stays whole.
func TestModelSurfaceDropsAnAllFullPermissionMap(t *testing.T) {
	objectTypes := permissionSample()

	slimObjectTypesForModel(objectTypes, true)

	if objectTypes[0].EffectivePermissions != nil {
		t.Errorf("an all-full map was kept: %v", objectTypes[0].EffectivePermissions)
	}
	if len(objectTypes[1].EffectivePermissions) != 2 ||
		objectTypes[1].EffectivePermissions["salary"] != interfaces.PropertyAccessMasked {
		t.Errorf("a restricted map was changed: %v", objectTypes[1].EffectivePermissions)
	}
	if objectTypes[2].EffectivePermissions != nil {
		t.Errorf("an absent map was invented: %v", objectTypes[2].EffectivePermissions)
	}
}

// The REST surface publishes the map it publishes today.
func TestRESTSurfaceKeepsTheFullPermissionMap(t *testing.T) {
	objectTypes := permissionSample()

	slimObjectTypesForModel(objectTypes, false)

	if len(objectTypes[0].EffectivePermissions) != 2 {
		t.Errorf("REST lost a map it publishes today: %v", objectTypes[0].EffectivePermissions)
	}
}

// The response goes on through a JSON round trip that turns every object type into a
// map[string]any, which is why the slimming runs upstream of it, on the typed object types. This
// pins what the caller ends up with after that conversion: the omitted map stays omitted, the
// restricted one still names its restriction, and the lifted operator list is the published one.
func TestTheSlimmedShapeSurvivesTheResponseConversion(t *testing.T) {
	objectTypes := permissionSample()[:2]
	objectTypes[0].DataProperties = []*interfaces.KnSearchDataProperty{
		{Name: "a", ConditionOperations: fullIndexOps()},
		{Name: "b", ConditionOperations: fullIndexOps()},
	}
	slimObjectTypesForModel(objectTypes, true)

	resp := FilterSearchSchemaResp(
		&interfaces.KnSearchResp{ObjectTypes: objectTypes},
		nil,
		SearchSchemaScope{IncludeObjectTypes: true},
		10,
	)
	encoded, err := json.Marshal(resp.ObjectTypes)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	published := string(encoded)
	if strings.Count(published, "effective_permissions") != 1 {
		t.Errorf("only the restricted object type publishes a permission map: %s", published)
	}
	if !strings.Contains(published, `"salary":"masked"`) {
		t.Errorf("the restriction must reach the caller: %s", published)
	}
	if !strings.Contains(published, "index_operations") || strings.Contains(published, "condition_operations") {
		t.Errorf("the shared operator list belongs on the object type: %s", published)
	}
}
