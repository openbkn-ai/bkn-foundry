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

func TestBuildProxyGrantSourcesSkipsUnboundRelationEndpoint(t *testing.T) {
	kn := &interfaces.KN{
		KNID: "kn-1",
		ObjectTypes: []*interfaces.ObjectType{
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-unbound"}},
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-bound", DataSource: &interfaces.ResourceInfo{
				Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-bound",
			}}},
		},
		RelationTypes: []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "rt-1", SourceObjectTypeID: "ot-unbound", TargetObjectTypeID: "ot-bound",
		}}},
	}
	sources, _, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 3 {
		t.Fatalf("unbound relation sources = %#v, want two object grants and one bound endpoint grant", sources)
	}
	for _, source := range sources {
		if source.ResourceID != "resource-bound" {
			t.Fatalf("unbound relation target = %q, want resource-bound", source.ResourceID)
		}
	}
}

func TestBuildProxyGrantSourcesSkipsUnscopedMetric(t *testing.T) {
	kn := &interfaces.KN{
		KNID:    "kn-1",
		Metrics: []*interfaces.MetricDefinition{{ID: "metric-unscoped"}},
	}
	sources, version, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 0 {
		t.Fatalf("unscoped metric sources = %#v, want none", sources)
	}
	if version == "" {
		t.Fatal("unscoped metric model version is empty")
	}
}

func TestBuildProxyGrantSourcesSkipsUnboundMetricScope(t *testing.T) {
	kn := &interfaces.KN{
		KNID:        "kn-1",
		ObjectTypes: []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-unbound"}}},
		Metrics: []*interfaces.MetricDefinition{{
			ID: "metric-draft", ScopeType: interfaces.ScopeTypeObjectType, ScopeRef: "ot-unbound",
		}},
	}
	sources, _, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 0 {
		t.Fatalf("unbound metric sources = %#v, want none", sources)
	}
}

func TestBuildProxyGrantSourcesSkipsIncompleteActionBinding(t *testing.T) {
	kn := &interfaces.KN{KNID: "kn-1", ActionTypes: []*interfaces.ActionType{
		{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "tool-draft", ActionSource: interfaces.ActionSource{Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "box-1"},
		}},
		{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "mcp-draft", ActionSource: interfaces.ActionSource{Type: interfaces.ACTION_SOURCE_TYPE_MCP, McpID: "mcp-1"},
		}},
	}}
	sources, _, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 0 {
		t.Fatalf("incomplete action sources = %#v, want none", sources)
	}
}

func TestBuildProxyGrantSourcesIncludesBoundLogicPropertyTool(t *testing.T) {
	kn := &interfaces.KN{KNID: "kn-1", ObjectTypes: []*interfaces.ObjectType{{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: "ot-1",
			LogicProperties: []*interfaces.LogicProperty{{
				Name: "forecast", Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
				DataSource: &interfaces.ResourceInfo{Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL, BoxID: "box-1", ToolID: "tool-1"},
			}},
		},
	}}}
	sources, _, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].ResourceType != "tool_box" ||
		sources[0].ResourceID != "box-1" || sources[0].Operation != interfaces.OPERATION_TYPE_EXECUTE {
		t.Fatalf("logic property sources = %#v", sources)
	}
}

func TestAddedProxyGrantSourcesReturnsOnlyNewTargets(t *testing.T) {
	current := []interfaces.ProxyGrantSourceSpec{
		{ResourceType: "resource", ResourceID: "resource-stable", Operation: "query_data", SourceType: "kn_proxy_binding", SourceID: "stable"},
		{ResourceType: "resource", ResourceID: "resource-old", Operation: "query_data", SourceType: "kn_proxy_binding", SourceID: "changed"},
	}
	candidate := []interfaces.ProxyGrantSourceSpec{
		{ResourceType: "resource", ResourceID: "resource-stable", Operation: "query_data", SourceType: "kn_proxy_binding", SourceID: "stable"},
		{ResourceType: "resource", ResourceID: "resource-new", Operation: "query_data", SourceType: "kn_proxy_binding", SourceID: "changed"},
	}
	added := addedProxyGrantSources(current, candidate)
	if len(added) != 1 || added[0].ResourceID != "resource-new" {
		t.Fatalf("added sources = %#v, want replacement target only", added)
	}
}
