// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"go.uber.org/mock/gomock"
	"strings"
	"testing"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestTypedProxyProjectionIncludesFunctionMountAndConceptGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	aoa := bmock.NewMockAgentOperatorAccess(ctrl)
	aoa.EXPECT().ListBoxTools(gomock.Any(), "mounted-fn").Return([]*interfaces.ToolBrief{{
		ToolID: "mounted-tool", BoxMetadataType: interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION,
	}}, nil)
	aoa.EXPECT().ListBoxTools(gomock.Any(), "group-fn").Return([]*interfaces.ToolBrief{{
		ToolID: "group-tool", BoxMetadataType: interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION,
	}}, nil)
	kn := &interfaces.KN{KNID: "kn-1", ConceptGroups: []*interfaces.ConceptGroup{{
		CGID: "group-1", ActionTypes: []*interfaces.ActionType{{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "group-action", ActionSource: interfaces.ActionSource{Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "group-fn", ToolID: "group-tool"},
		}}},
	}}}
	capabilities := []*interfaces.CapabilityBinding{{
		ID: "mount-1", KNID: kn.KNID, Branch: interfaces.MAIN_BRANCH,
		CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "mounted-fn", CapabilityID: "mounted-tool",
	}}
	sources, _, err := (&knowledgeNetworkService{aoa: aoa}).buildTypedProxyGrantSourcesWithCapabilities(t.Context(), kn, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, source := range sources {
		got[source.ResourceID] = source.ResourceType
	}
	if got["mounted-fn"] != "function" || got["group-fn"] != "function" {
		t.Fatalf("function mounts and concept group sources = %#v", got)
	}
}

func TestBuildTypedProxyGrantSourcesSeparatesFunctionAndAPI(t *testing.T) {
	ctrl := gomock.NewController(t)
	aoa := bmock.NewMockAgentOperatorAccess(ctrl)
	aoa.EXPECT().ListBoxTools(gomock.Any(), "box-fn").Return([]*interfaces.ToolBrief{{ToolID: "fn-1", BoxMetadataType: interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION}}, nil)
	aoa.EXPECT().ListBoxTools(gomock.Any(), "box-api").Return([]*interfaces.ToolBrief{{ToolID: "api-1", BoxMetadataType: interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI}}, nil)
	kn := &interfaces.KN{KNID: "kn-1", ActionTypes: []*interfaces.ActionType{
		{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "fn-action", ActionSource: interfaces.ActionSource{Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "box-fn", ToolID: "fn-1"}}},
		{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "api-action", ActionSource: interfaces.ActionSource{Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "box-api", ToolID: "api-1"}}},
	}}
	sources, _, err := (&knowledgeNetworkService{aoa: aoa}).buildTypedProxyGrantSources(t.Context(), kn)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, source := range sources {
		got[source.ResourceID] = source.ResourceType
	}
	if got["box-fn"] != "function" || got["box-api"] != "tool_box" {
		t.Fatalf("typed proxy sources = %#v", got)
	}
}

func TestBuildTypedProxyGrantSourcesReportsMissingToolIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	aoa := bmock.NewMockAgentOperatorAccess(ctrl)
	aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return([]*interfaces.ToolBrief{{
		ToolID: "present-tool", BoxMetadataType: interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI,
	}}, nil)
	kn := &interfaces.KN{KNID: "kn-1", ActionTypes: []*interfaces.ActionType{
		{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "action-1", ActionSource: interfaces.ActionSource{
			Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "box-1", ToolID: "missing-b",
		}}},
		{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "action-2", ActionSource: interfaces.ActionSource{
			Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "box-1", ToolID: "missing-a",
		}}},
	}}

	_, _, err := (&knowledgeNetworkService{aoa: aoa}).buildTypedProxyGrantSources(t.Context(), kn)
	if err == nil || err.Error() != "missing bound tools in toolbox box-1: missing-a, missing-b" {
		t.Fatalf("missing tool error = %v", err)
	}
}

func TestBuildTypedProxyGrantSourcesReportsMissingToolsFromEveryToolbox(t *testing.T) {
	ctrl := gomock.NewController(t)
	aoa := bmock.NewMockAgentOperatorAccess(ctrl)
	aoa.EXPECT().ListBoxTools(gomock.Any(), "box-a").Return(nil, nil)
	aoa.EXPECT().ListBoxTools(gomock.Any(), "box-b").Return(nil, nil)
	kn := &interfaces.KN{KNID: "kn-1", ActionTypes: []*interfaces.ActionType{
		{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "action-a", ActionSource: interfaces.ActionSource{
			Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "box-a", ToolID: "tool-a",
		}}},
		{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "action-b", ActionSource: interfaces.ActionSource{
			Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "box-b", ToolID: "tool-b",
		}}},
	}}

	_, _, err := (&knowledgeNetworkService{aoa: aoa}).buildTypedProxyGrantSources(t.Context(), kn)
	if err == nil || err.Error() != "missing bound tools in toolbox box-a: tool-a; toolbox box-b: tool-b" {
		t.Fatalf("missing tool error = %v", err)
	}
}

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

func TestBuildProxyGrantSourcesIncludesMountedExecutableCapabilities(t *testing.T) {
	kn := &interfaces.KN{KNID: "kn-1"}
	bindings := []*interfaces.CapabilityBinding{
		{ID: "binding-function", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "tool-1"},
		{ID: "binding-mcp", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			CapabilityType: interfaces.CAPABILITY_TYPE_MCP_TOOL, OwnerID: "mcp-1", CapabilityID: "lookup"},
		{ID: "binding-skill", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1"},
	}

	sources, firstVersion, err := buildProxyGrantSourcesWithCapabilities(kn, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 3 {
		t.Fatalf("len(sources) = %d, want 3", len(sources))
	}
	want := map[string]string{
		"binding-function": "tool_box/box-1", "binding-mcp": "mcp/mcp-1", "binding-skill": "skill/skill-1",
	}
	for _, source := range sources {
		if source.BindingType != "capability_binding" || source.Operation != interfaces.OPERATION_TYPE_EXECUTE {
			t.Fatalf("unexpected capability source: %#v", source)
		}
		if got := source.ResourceType + "/" + source.ResourceID; got != want[source.BindingID] {
			t.Fatalf("source target = %q, want %q", got, want[source.BindingID])
		}
	}

	bindings[1].CapabilityID = "lookup_v2"
	_, secondVersion, err := buildProxyGrantSourcesWithCapabilities(kn, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if firstVersion == secondVersion {
		t.Fatal("model version did not change when a mounted MCP tool changed")
	}
}

func TestMergeProxyCapabilityBindingsAppliesAttachAndDetach(t *testing.T) {
	current := []*interfaces.CapabilityBinding{{ID: "keep"}, {ID: "remove"}}
	added := []*interfaces.CapabilityBinding{{ID: "new"}, {ID: "keep", CapabilityID: "updated"}}

	merged := mergeProxyCapabilityBindings(current, added, []string{"remove"})

	if len(merged) != 2 || merged[0].ID != "keep" || merged[0].CapabilityID != "updated" || merged[1].ID != "new" {
		t.Fatalf("merged bindings = %#v", merged)
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

// proxyVersionFixture is the model shared with the 0.1.5 migration script's
// tests (deploy/scripts/upgrades/0.1.5/permission_model_transition). Both sides
// pin the same digest, so neither can change the projection alone.
func proxyVersionFixture() (*interfaces.KN, []*interfaces.CapabilityBinding) {
	kn := &interfaces.KN{
		KNID: "kn-golden",
		ObjectTypes: []*interfaces.ObjectType{
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID:       "ot-order",
				DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "res-order"},
				LogicProperties: []*interfaces.LogicProperty{{
					Name: "risk", Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
					DataSource: &interfaces.ResourceInfo{Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL, BoxID: "box-1", ToolID: "tool-risk"},
				}},
			}},
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID:       "ot-customer",
				DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "res-customer"},
			}},
		},
		RelationTypes: []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "rt-placed-by", SourceObjectTypeID: "ot-order", TargetObjectTypeID: "ot-customer",
		}}},
		Metrics: []*interfaces.MetricDefinition{{ID: "metric-revenue", ScopeType: "object_type", ScopeRef: "ot-order"}},
		ActionTypes: []*interfaces.ActionType{
			{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
				ATID: "at-refund", ActionSource: interfaces.ActionSource{Type: interfaces.ACTION_SOURCE_TYPE_TOOL, BoxID: "box-2", ToolID: "tool-refund"},
			}},
			{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
				ATID: "at-notify", ActionSource: interfaces.ActionSource{Type: interfaces.ACTION_SOURCE_TYPE_MCP, McpID: "mcp-1", ToolName: "notify"},
			}},
		},
	}
	capabilities := []*interfaces.CapabilityBinding{
		{ID: "cap-function", KNID: "kn-golden", Branch: interfaces.MAIN_BRANCH,
			CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-3", CapabilityID: "tool-3"},
		{ID: "cap-mcp", KNID: "kn-golden", Branch: interfaces.MAIN_BRANCH,
			CapabilityType: interfaces.CAPABILITY_TYPE_MCP_TOOL, OwnerID: "mcp-2", CapabilityID: "lookup"},
		{ID: "cap-skill-a", KNID: "kn-golden", Branch: interfaces.MAIN_BRANCH,
			CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-a"},
		{ID: "cap-skill-b", KNID: "kn-golden", Branch: interfaces.MAIN_BRANCH,
			CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-b"},
	}
	return kn, capabilities
}

// proxyVersionFixtureDigest is the fixture's model version. It is also what the
// code before #1550 derived, when Skill mounts produced no source at all:
// adding Skill sources must not move any network's version.
const proxyVersionFixtureDigest = "sha256:e151ed59ea68aecf9ba7d0fc2cab3f24e1d0e814ac0eeeae9544d0766296ebbd"

func TestProxyModelVersionMatchesSharedMigrationFixture(t *testing.T) {
	kn, capabilities := proxyVersionFixture()
	sources, version, err := buildProxyGrantSourcesWithCapabilities(kn, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if version != proxyVersionFixtureDigest {
		t.Fatalf("model version = %s, want the digest pinned with the migration script %s",
			version, proxyVersionFixtureDigest)
	}
	skills := 0
	for _, source := range sources {
		if source.ResourceType == interfaces.KNProxyTargetTypeSkill {
			skills++
		}
	}
	if skills != 2 {
		t.Fatalf("skill sources = %d, want both mounted skills to stay resolvable", skills)
	}
}

func TestProxyModelVersionExcludesSkillMounts(t *testing.T) {
	kn, capabilities := proxyVersionFixture()
	_, withSkills, err := buildProxyGrantSourcesWithCapabilities(kn, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	withoutSkills := make([]*interfaces.CapabilityBinding, 0, len(capabilities))
	for _, capability := range capabilities {
		if capability.CapabilityType != interfaces.CAPABILITY_TYPE_SKILL {
			withoutSkills = append(withoutSkills, capability)
		}
	}
	_, version, err := buildProxyGrantSourcesWithCapabilities(kn, withoutSkills)
	if err != nil {
		t.Fatal(err)
	}
	if version != withSkills {
		t.Fatalf("mounting skills changed the model version: %s != %s", withSkills, version)
	}

	capabilities[2].CapabilityID = "skill-c"
	sources, changed, err := buildProxyGrantSourcesWithCapabilities(kn, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if changed != withSkills {
		t.Fatal("replacing a mounted skill changed the model version")
	}
	found := false
	for _, source := range sources {
		if source.BindingID == "cap-skill-a" {
			found = source.ResourceType == interfaces.KNProxyTargetTypeSkill && source.ResourceID == "skill-c" &&
				source.Operation == interfaces.OPERATION_TYPE_EXECUTE &&
				source.BindingType == interfaces.KNProxyBindingTypeCapability
		}
	}
	if !found {
		t.Fatalf("replaced skill is not the resolvable source: %#v", sources)
	}
}

func TestBuildProxyGrantSourcesSkipsIncompleteSkillMount(t *testing.T) {
	kn := &interfaces.KN{KNID: "kn-1"}
	sources, _, err := buildProxyGrantSourcesWithCapabilities(kn, []*interfaces.CapabilityBinding{{
		ID: "binding-skill", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, CapabilityType: interfaces.CAPABILITY_TYPE_SKILL,
	}})
	if err != nil || len(sources) != 0 {
		t.Fatalf("incomplete skill mount = (%#v, %v), want skipped without failing the projection", sources, err)
	}
}

func TestProxyGrantSnapshotVersionIsCanonicalAndCoversAuthorizationFields(t *testing.T) {
	first := interfaces.ProxyGrantSourceSpec{
		SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-a", KNID: "kn-1",
		BindingType: interfaces.MODULE_TYPE_OBJECT_TYPE, BindingID: "ot-1",
		ResourceType: "resource", ResourceID: "resource-1", Operation: interfaces.OPERATION_TYPE_QUERY_DATA,
	}
	second := interfaces.ProxyGrantSourceSpec{
		SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-b", KNID: "kn-1",
		BindingType: interfaces.MODULE_TYPE_OBJECT_TYPE, BindingID: "ot-2",
		ResourceType: "resource", ResourceID: "resource-2", Operation: interfaces.OPERATION_TYPE_VIEW_DETAIL,
	}
	version, err := proxyGrantSnapshotVersion([]interfaces.ProxyGrantSourceSpec{first, second})
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := proxyGrantSnapshotVersion([]interfaces.ProxyGrantSourceSpec{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if reordered != version {
		t.Fatalf("reordered snapshot version = %s, want %s", reordered, version)
	}
	second.Operation = interfaces.OPERATION_TYPE_QUERY_DATA
	changed, err := proxyGrantSnapshotVersion([]interfaces.ProxyGrantSourceSpec{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if changed == version {
		t.Fatal("changing an authorization field did not change the snapshot version")
	}
}
