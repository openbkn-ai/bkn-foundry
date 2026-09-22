// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// A map that says full for every property tells the caller nothing, and the
// summary carried one per object type. It goes; a map with any restriction is
// the one worth reading and stays whole.
func TestSummaryOmitsUnrestrictedPermissions(t *testing.T) {
	open := &interfaces.ObjectType{ID: "open", EffectivePermissions: map[string]interfaces.PropertyAccessLevel{
		"a": interfaces.PropertyAccessFull, "b": interfaces.PropertyAccessFull,
	}}
	masked := &interfaces.ObjectType{ID: "masked", EffectivePermissions: map[string]interfaces.PropertyAccessLevel{
		"a": interfaces.PropertyAccessFull, "id": interfaces.PropertyAccessMasked,
	}}
	none := &interfaces.ObjectType{ID: "none"}
	omitUnrestrictedPermissions([]*interfaces.ObjectType{open, masked, none, nil})

	if open.EffectivePermissions != nil {
		t.Errorf("an all-full map was kept: %v", open.EffectivePermissions)
	}
	if len(masked.EffectivePermissions) != 2 || masked.EffectivePermissions["id"] != interfaces.PropertyAccessMasked {
		t.Errorf("a restricted map was changed: %v", masked.EffectivePermissions)
	}
	if none.EffectivePermissions != nil {
		t.Errorf("an absent map was invented: %v", none.EffectivePermissions)
	}
}
