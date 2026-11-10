// Copyright openbkn.ai

package knowledge_network

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestOverviewGraphCarriesDeprecationOnly(t *testing.T) {
	deprecated := &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED, Reason: "merged", ReplacedBy: "patient"}
	objects := []*interfaces.ObjectType{
		{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "patient", OTName: "Patient"}},
		{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "inpatient", OTName: "Inpatient"}, Lifecycle: deprecated},
		{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "outpatient", OTName: "Outpatient"},
			Lifecycle: &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_ACTIVE}},
	}
	relations := []*interfaces.RelationType{
		{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "treats", RTName: "Treats",
			SourceObjectTypeID: "patient", TargetObjectTypeID: "inpatient", Type: "direct"}},
		{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "treats_old", RTName: "Treats (old)",
			SourceObjectTypeID: "patient", TargetObjectTypeID: "inpatient", Type: "direct"},
			RelationDeclarations: interfaces.RelationDeclarations{
				Lifecycle: &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED, Reason: "split", ReplacedBy: "treats"}}},
	}
	nodeIDs := map[string]struct{}{"patient": {}, "inpatient": {}, "outpatient": {}}

	edges := overviewEdges(relations, nodeIDs)
	nodes := overviewNodes(objects, edges, nil)

	lifecycles := map[string]*interfaces.Lifecycle{}
	for _, node := range nodes {
		lifecycles[node.ID] = node.Lifecycle
	}
	for _, edge := range edges {
		lifecycles[edge.ID] = edge.Lifecycle
	}
	for _, id := range []string{"patient", "outpatient", "treats"} {
		if lifecycles[id] != nil {
			t.Errorf("%s is active and should carry no lifecycle, got %+v", id, lifecycles[id])
		}
	}
	if got := lifecycles["inpatient"]; got == nil || *got != *deprecated {
		t.Errorf("inpatient lifecycle = %+v, want %+v", got, deprecated)
	}
	if got := lifecycles["treats_old"]; got == nil || got.Status != interfaces.LIFECYCLE_DEPRECATED || got.ReplacedBy != "treats" {
		t.Errorf("treats_old lifecycle = %+v, want deprecated, replaced by treats", got)
	}
}
