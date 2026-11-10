// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestBrokenReferences(t *testing.T) {
	ot := func(id string) *interfaces.ObjectType {
		return &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id}}
	}
	kn := &interfaces.KN{
		ObjectTypes: []*interfaces.ObjectType{ot("a")},
		RelationTypes: []*interfaces.RelationType{
			{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "ok", SourceObjectTypeID: "a", TargetObjectTypeID: "a"}},
			{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "rt", SourceObjectTypeID: "a", TargetObjectTypeID: "gone"}},
		},
		ActionTypes: []*interfaces.ActionType{
			{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "at", ObjectTypeID: "gone"}},
			{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "free"}},
		},
	}
	members := []interfaces.ConceptGroupRelation{{CGID: "cg", ConceptID: "a"}, {CGID: "cg", ConceptID: "gone"}}
	schedules := []*interfaces.ActionSchedule{{ID: "s1", ActionTypeID: "free"}, {ID: "s2", ActionTypeID: "gone"}}
	bindings := []*interfaces.CapabilityBinding{
		{ID: "b1", Status: interfaces.CAPABILITY_STATUS_MISSING, CapabilityType: "skill", CapabilityID: "sk"},
		{ID: "b2"}, // unknown status, for example the execution factory was unreachable
	}
	got := map[string]string{}
	for _, p := range brokenReferences(kn, members, schedules, bindings) {
		got[p.DefinitionType+"/"+p.DefinitionID] = p.Description
	}
	want := []string{"relation_type/rt", "action_type/at", "concept_group/cg", "action_schedule/s2", "capability_binding/b1"}
	if len(got) != len(want) {
		t.Fatalf("problems %v, want %v", got, want)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Errorf("missing %s in %v", key, got)
		}
	}
}
