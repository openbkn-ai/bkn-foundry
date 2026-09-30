// Copyright openbkn.ai

package knowledge_network

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"bkn-backend/interfaces"
)

func TestNormalizeImportPlanPreservesExportedPersistentDefinitions(t *testing.T) {
	const exported = `{
		"id":"kn-1","name":"network","branch":"main",
		"concept_groups":[{"id":"cg-1","name":"group","tags":["group-tag"],"comment":"group comment","object_type_ids":["ot-1"]}],
		"object_types":[{
			"id":"ot-1","name":"object","tags":["object-tag"],"comment":"object comment",
			"data_source":{"type":"resource","id":"resource-1","name":"source"},
			"data_properties":[{"name":"code","display_name":"Code","type":"string","comment":"data","mapped_field":{"name":"source_code","type":"string"}}],
			"logic_properties":[{"name":"amount","display_name":"Amount","type":"metric","comment":"logic","data_source":{"type":"metric","id":"metric-1","name":"Metric"},"parameters":[{"name":"currency","type":"string","value":"CNY"}],"analysis_dimensions":[{"name":"region","type":"string"}]}],
			"primary_keys":["code"],"display_key":"code","incremental_key":"updated_at"
		}],
		"relation_types":[{
			"id":"rt-1","name":"relation","source_object_type_id":"ot-1","target_object_type_id":"ot-2","type":"direct",
			"mapping_rules":[{"source_property":{"name":"code","display_name":"Code"},"target_property":{"name":"parent_code","display_name":"Parent code"}}]
		}],
		"action_types":[{
			"id":"at-1","name":"action","action_type":"modify","action_intent":"update object","object_type_id":"ot-1",
			"condition":{"field":"code","operation":"eq","value_from":"const","value":"A"},
			"impact_contracts":[{"object_type_id":"ot-1","expected_operation":"modify","affected_fields":["code"]}],
			"action_source":{"type":"tool","box_id":"box-1","tool_id":"tool-1"},
			"parameters":[{"name":"code","type":"string","required":true}],"schedule":{"type":"immediate"}
		}],
		"risk_types":[{"id":"risk-1","name":"risk","tags":["risk-tag"],"comment":"risk comment"}],
		"metrics":[{
			"id":"metric-1","name":"metric","metric_type":"atomic","scope_type":"object_type","scope_ref":"ot-1","unit_type":"currency","unit":"CNY",
			"time_dimension":{"property":"updated_at","default_range_policy":"last_30_days"},
			"calculation_formula":{"aggregation":{"property":"amount","aggr":"sum"},"group_by":[{"property":"region","description":"Region"}],"order_by":[{"property":"amount","direction":"desc"}]},
			"analysis_dimensions":[{"name":"region","display_name":"Region"}]
		}]
	}`

	var source interfaces.KN
	if err := json.Unmarshal([]byte(exported), &source); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	var expected interfaces.KN
	if err := json.Unmarshal([]byte(exported), &expected); err != nil {
		t.Fatalf("Unmarshal expected error = %v", err)
	}

	plan, err := normalizeImportPlan(context.Background(), &source)
	if err != nil {
		t.Fatalf("normalizeImportPlan() error = %v", err)
	}
	if len(plan.ConceptGroups) != 1 || len(plan.ObjectTypes) != 1 || len(plan.RelationTypes) != 1 ||
		len(plan.ActionTypes) != 1 || len(plan.RiskTypes) != 1 || len(plan.Metrics) != 1 {
		t.Fatalf("normalized plan has unexpected counts: %#v", plan)
	}
	if !reflect.DeepEqual(plan.ObjectTypes[0].ObjectTypeWithKeyField,
		expected.ObjectTypes[0].ObjectTypeWithKeyField) {
		t.Fatalf("object definition changed:\n got: %#v\nwant: %#v",
			plan.ObjectTypes[0].ObjectTypeWithKeyField, expected.ObjectTypes[0].ObjectTypeWithKeyField)
	}
	if !reflect.DeepEqual(plan.RelationTypes[0].RelationTypeWithKeyField,
		expected.RelationTypes[0].RelationTypeWithKeyField) {
		t.Fatalf("relation definition changed:\n got: %#v\nwant: %#v",
			plan.RelationTypes[0].RelationTypeWithKeyField, expected.RelationTypes[0].RelationTypeWithKeyField)
	}
	if !reflect.DeepEqual(plan.ActionTypes[0].ActionTypeWithKeyField,
		expected.ActionTypes[0].ActionTypeWithKeyField) {
		t.Fatalf("action definition changed:\n got: %#v\nwant: %#v",
			plan.ActionTypes[0].ActionTypeWithKeyField, expected.ActionTypes[0].ActionTypeWithKeyField)
	}
	if !reflect.DeepEqual(plan.RiskTypes[0], expected.RiskTypes[0]) {
		t.Fatalf("risk definition changed:\n got: %#v\nwant: %#v", plan.RiskTypes[0], expected.RiskTypes[0])
	}
	if !reflect.DeepEqual(plan.Metrics[0], expected.Metrics[0]) {
		t.Fatalf("metric definition changed:\n got: %#v\nwant: %#v", plan.Metrics[0], expected.Metrics[0])
	}
	if !sameImportIDs(plan.GroupMembers["cg-1"], []string{"ot-1"}) {
		t.Fatalf("group members = %v, want [ot-1]", plan.GroupMembers["cg-1"])
	}
}

func TestNormalizeImportPlanFlattensNestedDefinitionsAndMemberships(t *testing.T) {
	nestedObject := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "ot-1", OTName: "object-1",
	}}
	nestedRelation := &interfaces.RelationType{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
		RTID: "rt-1", RTName: "relation-1",
	}}
	nestedAction := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
		ATID: "at-1", ATName: "action-1",
	}}
	group := &interfaces.ConceptGroup{
		CGID:          "cg-1",
		CGName:        "group-1",
		ObjectTypeIDs: []string{"ot-existing", "ot-missing"},
		ObjectTypes:   []*interfaces.ObjectType{nestedObject},
		RelationTypes: []*interfaces.RelationType{nestedRelation},
		ActionTypes:   []*interfaces.ActionType{nestedAction},
	}
	kn := &interfaces.KN{
		KNID:          "kn-1",
		Branch:        interfaces.MAIN_BRANCH,
		ConceptGroups: []*interfaces.ConceptGroup{group},
		ObjectTypes: []*interfaces.ObjectType{
			{ObjectTypeWithKeyField: nestedObject.ObjectTypeWithKeyField},
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-2", OTName: "object-2"},
				ConceptGroups: []*interfaces.ConceptGroup{{CGID: "cg-1"}}},
		},
		RelationTypes: []*interfaces.RelationType{{RelationTypeWithKeyField: nestedRelation.RelationTypeWithKeyField}},
		ActionTypes:   []*interfaces.ActionType{{ActionTypeWithKeyField: nestedAction.ActionTypeWithKeyField}},
	}

	plan, err := normalizeImportPlan(context.Background(), kn)
	if err != nil {
		t.Fatalf("normalizeImportPlan() error = %v", err)
	}
	if len(plan.ConceptGroups) != 1 || len(plan.ObjectTypes) != 2 ||
		len(plan.RelationTypes) != 1 || len(plan.ActionTypes) != 1 {
		t.Fatalf("normalized counts = groups:%d objects:%d relations:%d actions:%d",
			len(plan.ConceptGroups), len(plan.ObjectTypes), len(plan.RelationTypes), len(plan.ActionTypes))
	}
	if len(plan.ConceptGroups[0].ObjectTypes) != 0 || len(plan.ConceptGroups[0].RelationTypes) != 0 ||
		len(plan.ConceptGroups[0].ActionTypes) != 0 {
		t.Fatal("normalized concept group retained nested definitions")
	}
	wantMembers := []string{"ot-existing", "ot-missing", "ot-1", "ot-2"}
	if !sameImportIDs(plan.GroupMembers["cg-1"], wantMembers) {
		t.Fatalf("group members = %v, want %v", plan.GroupMembers["cg-1"], wantMembers)
	}
	if len(group.ObjectTypes) != 1 || len(group.RelationTypes) != 1 || len(group.ActionTypes) != 1 {
		t.Fatal("normalization mutated the request's nested resource slices")
	}
	for _, objectType := range plan.ObjectTypes {
		if len(objectType.ConceptGroups) != 1 || objectType.ConceptGroups[0].CGID != "cg-1" {
			t.Fatalf("object %s normalized groups = %#v", objectType.OTID, objectType.ConceptGroups)
		}
	}
}

func TestNormalizeImportPlanPreservesMembershipOrderAcrossGroups(t *testing.T) {
	kn := &interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ConceptGroups: []*interfaces.ConceptGroup{
			{CGID: "cg-2", ObjectTypeIDs: []string{"ot-1", "ot-2"}},
			{CGID: "cg-1", ObjectTypeIDs: []string{"ot-1"}},
		},
		ObjectTypes: []*interfaces.ObjectType{
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-1"}},
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-2"}},
		},
	}
	plan, err := normalizeImportPlan(context.Background(), kn)
	if err != nil {
		t.Fatalf("normalizeImportPlan() error = %v", err)
	}
	for i, want := range []string{"cg-2", "cg-1"} {
		if got := plan.ObjectTypes[0].ConceptGroups[i].CGID; got != want {
			t.Fatalf("object group %d = %q, want %q", i, got, want)
		}
	}
	if len(plan.ObjectTypes[1].ConceptGroups) != 1 || plan.ObjectTypes[1].ConceptGroups[0].CGID != "cg-2" {
		t.Fatalf("second object groups = %#v, want only cg-2", plan.ObjectTypes[1].ConceptGroups)
	}
}

func TestNormalizeImportPlanRejectsConflictingDefinitions(t *testing.T) {
	newKN := func() *interfaces.KN {
		return &interfaces.KN{
			KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			ConceptGroups: []*interfaces.ConceptGroup{{
				CGID: "cg-1", CGName: "group-1",
				ObjectTypes: []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot-1", OTName: "nested-name",
				}}},
			}},
			ObjectTypes: []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID: "ot-1", OTName: "top-level-name",
			}}},
		}
	}

	if _, err := normalizeImportPlan(context.Background(), newKN()); err == nil {
		t.Fatal("accepted conflicting duplicate definitions")
	}
}

func TestNormalizeImportPlanIgnoresRuntimeFieldsWhenDeduplicating(t *testing.T) {
	nested := &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-1", OTName: "object-1"},
		Status:                 &interfaces.ObjectTypeStatus{DocCount: 10},
		Operations:             []string{"view_detail"},
	}
	topLevel := &interfaces.ObjectType{
		ObjectTypeWithKeyField: nested.ObjectTypeWithKeyField,
		Status:                 &interfaces.ObjectTypeStatus{DocCount: 20},
		Operations:             []string{"modify"},
	}
	kn := &interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ConceptGroups: []*interfaces.ConceptGroup{{
			CGID: "cg-1", CGName: "group-1", ObjectTypes: []*interfaces.ObjectType{nested},
		}},
		ObjectTypes: []*interfaces.ObjectType{topLevel},
	}

	plan, err := normalizeImportPlan(context.Background(), kn)
	if err != nil {
		t.Fatalf("normalizeImportPlan() error = %v", err)
	}
	if len(plan.ObjectTypes) != 1 || plan.ObjectTypes[0].OTID != "ot-1" {
		t.Fatalf("normalized objects = %#v", plan.ObjectTypes)
	}
}

func sameImportIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
