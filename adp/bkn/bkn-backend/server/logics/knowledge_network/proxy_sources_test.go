// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"testing"

	"bkn-backend/interfaces"
)

func TestBuildProxyGrantSourcesDerivesCompletePublishedSet(t *testing.T) {
	kn := &interfaces.KN{
		KNID: "kn-1",
		ObjectTypes: []*interfaces.ObjectType{
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-a", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-shared"}}},
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-b", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-shared"}}},
		},
		RelationTypes: []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "rt-1", SourceObjectTypeID: "ot-a", TargetObjectTypeID: "ot-b",
		}}},
		Metrics: []*interfaces.MetricDefinition{{ID: "metric-1", ScopeType: "object_type", ScopeRef: "ot-a"}},
		ActionTypes: []*interfaces.ActionType{{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "action-1", ActionSource: interfaces.ActionSource{Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "box-1", ToolID: "tool-1"},
		}}},
	}

	sources, version, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatalf("buildProxyGrantSources() error = %v", err)
	}
	if len(sources) != 7 {
		t.Fatalf("len(sources) = %d, want 7", len(sources))
	}
	if len(version) != len("sha256:")+64 {
		t.Fatalf("model version %q is not a sha256 digest", version)
	}

	objectSourceIDs := map[string]struct{}{}
	for _, source := range sources {
		if source.SourceType != interfaces.ProxyGrantSourceTypeKNBinding || source.KNID != kn.KNID {
			t.Fatalf("source provenance = %#v", source)
		}
		if source.BindingType == interfaces.MODULE_TYPE_OBJECT_TYPE && source.Operation == interfaces.OPERATION_TYPE_QUERY_DATA {
			objectSourceIDs[source.SourceID] = struct{}{}
		}
	}
	if len(objectSourceIDs) != 2 {
		t.Fatalf("shared resource collapsed independent object bindings: %#v", objectSourceIDs)
	}
}

func TestBuildProxyGrantSourcesVersionIncludesConcreteActionTool(t *testing.T) {
	action := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
		ATID: "action-1", ActionSource: interfaces.ActionSource{Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "box-1", ToolID: "tool-1"},
	}}
	kn := &interfaces.KN{KNID: "kn-1", ActionTypes: []*interfaces.ActionType{action}}
	_, first, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	action.ActionSource.ToolID = "tool-2"
	_, second, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("model version did not change when the concrete action tool changed")
	}
}

func TestBuildProxyGrantSourcesRejectsUnboundRelationEndpoint(t *testing.T) {
	kn := &interfaces.KN{
		KNID: "kn-1",
		RelationTypes: []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "rt-1", SourceObjectTypeID: "missing", TargetObjectTypeID: "missing",
		}}},
	}
	if _, _, err := buildProxyGrantSources(kn); err == nil {
		t.Fatal("buildProxyGrantSources() error = nil, want invalid binding error")
	}
}
