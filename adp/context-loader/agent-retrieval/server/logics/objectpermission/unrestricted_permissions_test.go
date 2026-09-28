// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package objectpermission

import (
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// A map that says full for every property tells the caller nothing, and the
// summary carried one per object type. It goes; a map with any restriction is
// the one worth reading and stays whole.
func TestOmitsAnAllFullPermissionMap(t *testing.T) {
	open := &interfaces.ObjectType{ID: "open", EffectivePermissions: map[string]interfaces.PropertyAccessLevel{
		"a": interfaces.PropertyAccessFull, "b": interfaces.PropertyAccessFull,
	}}
	masked := &interfaces.ObjectType{ID: "masked", EffectivePermissions: map[string]interfaces.PropertyAccessLevel{
		"a": interfaces.PropertyAccessFull, "id": interfaces.PropertyAccessMasked,
	}}
	none := &interfaces.ObjectType{ID: "none"}
	OmitUnrestrictedPermissions([]*interfaces.ObjectType{open, masked, none, nil})

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

// The two faces of get_kn_detail have to agree. They drifted once: the trim lived in
// the MCP handler only, so a REST caller was handed 64% of a 1000-object network in
// all-full permission entries that an MCP caller never saw (#1891). This pins the
// contract at the level both handlers reach for, so the next trim added to one of
// them has somewhere to fail.
func TestBothSurfacesTrimTheSameWay(t *testing.T) {
	build := func() []*interfaces.ObjectType {
		return []*interfaces.ObjectType{
			{ID: "open", EffectivePermissions: map[string]interfaces.PropertyAccessLevel{
				"a": interfaces.PropertyAccessFull, "b": interfaces.PropertyAccessFull}},
			{ID: "masked", EffectivePermissions: map[string]interfaces.PropertyAccessLevel{
				"a": interfaces.PropertyAccessFull, "id": interfaces.PropertyAccessMasked}},
			{ID: "unbound"},
		}
	}
	viaMCP, viaREST := build(), build()
	OmitUnrestrictedPermissions(viaMCP)
	OmitUnrestrictedPermissions(viaREST)

	for i := range viaMCP {
		if !reflect.DeepEqual(viaMCP[i].EffectivePermissions, viaREST[i].EffectivePermissions) {
			t.Fatalf("%s: MCP %v, REST %v", viaMCP[i].ID, viaMCP[i].EffectivePermissions, viaREST[i].EffectivePermissions)
		}
	}
	// And the trim has to be idempotent, since both handlers run it after Slim.
	OmitUnrestrictedPermissions(viaREST)
	if len(viaREST[1].EffectivePermissions) != 2 {
		t.Fatalf("a second pass changed a restricted map: %v", viaREST[1].EffectivePermissions)
	}
}
