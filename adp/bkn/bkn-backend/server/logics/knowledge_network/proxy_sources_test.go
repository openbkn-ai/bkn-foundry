// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"strings"
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

func TestBuildProxyGrantSourcesIncludesIndirectRelationBackingResource(t *testing.T) {
	backing := &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-bridge"}
	kn := &interfaces.KN{
		KNID: "kn-1",
		RelationTypes: []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "rt-indirect",
			MappingRules: &interfaces.InDirectMapping{
				BackingDataSource: backing,
			},
		}}},
	}

	sources, firstVersion, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 {
		t.Fatalf("indirect relation sources = %#v, want one backing resource grant", sources)
	}
	source := sources[0]
	if source.BindingType != interfaces.MODULE_TYPE_RELATION_TYPE || source.BindingID != "rt-indirect" ||
		source.ResourceType != "resource" || source.ResourceID != "resource-bridge" ||
		source.Operation != interfaces.OPERATION_TYPE_QUERY_DATA {
		t.Fatalf("indirect relation source = %#v", source)
	}

	backing.ID = "resource-bridge-v2"
	_, secondVersion, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	if firstVersion == secondVersion {
		t.Fatal("model version did not change when the indirect relation backing resource changed")
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

func TestBuildProxyGrantSourcesIncludesNestedConceptGroupBindings(t *testing.T) {
	kn := &interfaces.KN{
		KNID: "kn-1",
		ConceptGroups: []*interfaces.ConceptGroup{{
			CGID: "cg-1",
			ObjectTypes: []*interfaces.ObjectType{{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "nested-ot", DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "nested-resource",
					},
				},
			}},
			RelationTypes: []*interfaces.RelationType{{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID: "nested-rt", SourceObjectTypeID: "nested-ot", TargetObjectTypeID: "nested-ot",
				},
			}},
			ActionTypes: []*interfaces.ActionType{{
				ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
					ATID: "nested-at", ActionSource: interfaces.ActionSource{
						Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "nested-box", ToolID: "nested-tool",
					},
				},
			}},
		}},
	}

	sources, _, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		interfaces.MODULE_TYPE_OBJECT_TYPE + "\x00nested-ot\x00resource\x00nested-resource\x00" + interfaces.OPERATION_TYPE_VIEW_DETAIL:  false,
		interfaces.MODULE_TYPE_OBJECT_TYPE + "\x00nested-ot\x00resource\x00nested-resource\x00" + interfaces.OPERATION_TYPE_QUERY_DATA:   false,
		interfaces.MODULE_TYPE_RELATION_TYPE + "\x00nested-rt\x00resource\x00nested-resource\x00" + interfaces.OPERATION_TYPE_QUERY_DATA: false,
		interfaces.MODULE_TYPE_ACTION_TYPE + "\x00nested-at\x00tool_box\x00nested-box\x00" + interfaces.OPERATION_TYPE_EXECUTE:           false,
	}
	for _, source := range sources {
		key := strings.Join([]string{source.BindingType, source.BindingID, source.ResourceType,
			source.ResourceID, source.Operation}, "\x00")
		if _, exists := want[key]; exists {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Fatalf("nested concept-group source %q missing from %#v", key, sources)
		}
	}
}

func TestBuildProxyGrantSourcesDeduplicatesTopLevelAndNestedCopies(t *testing.T) {
	objectType := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "ot-1", DataSource: &interfaces.ResourceInfo{
			Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1",
		},
	}}
	topLevel := &interfaces.KN{KNID: "kn-1", ObjectTypes: []*interfaces.ObjectType{objectType}}
	withGroupCopy := &interfaces.KN{
		KNID:        "kn-1",
		ObjectTypes: []*interfaces.ObjectType{objectType},
		ConceptGroups: []*interfaces.ConceptGroup{{
			CGID: "cg-1", ObjectTypes: []*interfaces.ObjectType{objectType},
		}},
	}

	topLevelSources, topLevelVersion, err := buildProxyGrantSources(topLevel)
	if err != nil {
		t.Fatal(err)
	}
	withGroupSources, withGroupVersion, err := buildProxyGrantSources(withGroupCopy)
	if err != nil {
		t.Fatal(err)
	}
	if len(withGroupSources) != len(topLevelSources) {
		t.Fatalf("duplicate concept-group copy changed source count: got %d, want %d",
			len(withGroupSources), len(topLevelSources))
	}
	if withGroupVersion != topLevelVersion {
		t.Fatalf("duplicate concept-group copy changed proxy version: got %q, want %q",
			withGroupVersion, topLevelVersion)
	}
}
