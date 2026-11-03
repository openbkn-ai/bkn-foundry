// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"testing"

	"github.com/bytedance/sonic"
	"github.com/smartystreets/goconvey/convey"
)

func newDetailFixture() *KnowledgeNetworkDetail {
	obj := &ObjectType{
		ID:   "ot_order",
		Name: "order",
		DataProperties: []*DataProperty{{
			Name:                "amount",
			DisplayName:         "金额",
			Type:                "double",
			Comment:             "订单金额",
			MappedField:         map[string]any{"column": "amt"},
			ConditionOperations: []KnOperationType{"gt", "lt"},
			Semantic:            "status",
			Required:            true,
			Constraints:         &PropertyConstraints{Unit: "元"},
		}},
		LogicProperties: []*LogicPropertyDef{{
			Name:        "gmv",
			DisplayName: "GMV",
			Type:        "metric",
			Comment:     "总额",
			DataSource:  map[string]any{"formula": "sum(amount)"},
			Parameters:  []PropertyParameter{{Name: "window", Type: "string"}},
		}},
		PrimaryKeys: []string{"id"},
	}
	rel := &RelationType{
		ID:                 "rt_places",
		Name:               "places",
		SourceObjectTypeID: "ot_customer",
		TargetObjectTypeID: "ot_order",
		SourceObjectType:   map[string]any{"name": "customer"},
		TargetObjectType:   map[string]any{"name": "order"},
		MappingRules:       []any{map[string]any{"from": "a", "to": "b"}},
	}
	act := &ActionType{ID: "at_cancel", Name: "cancel", ObjectTypeID: "ot_order"}
	return &KnowledgeNetworkDetail{
		ID: "kn-1", Name: "sales", Comment: "c",
		ObjectTypes:   []*ObjectType{obj},
		RelationTypes: []*RelationType{rel},
		ActionTypes:   []*ActionType{act},
		ConceptGroups: []*ConceptGroup{{
			ID: "cg1", Name: "core", ObjectTypeIDs: []string{"ot_order"},
			Tags:          []string{"sales", "core"},
			Comment:       "orders and their customers",
			ObjectTypes:   []*ObjectType{obj}, // duplicate of top-level
			RelationTypes: []*RelationType{rel},
			ActionTypes:   []*ActionType{act},
		}},
	}
}

func TestSlim_Summary(t *testing.T) {
	convey.Convey("summary strips heavy fields, keeps names, dedups concept_groups", t, func() {
		d := newDetailFixture()
		d.Slim(DetailLevelSummary)

		// summary keeps only name+type per property (flat/uniform → TOON tabular)
		dp := d.ObjectTypes[0].DataProperties[0]
		convey.So(dp.Name, convey.ShouldEqual, "amount")
		convey.So(dp.Type, convey.ShouldEqual, "double")
		convey.So(dp.DisplayName, convey.ShouldEqual, "")
		convey.So(dp.Comment, convey.ShouldEqual, "")
		convey.So(dp.MappedField, convey.ShouldBeNil)
		convey.So(dp.ConditionOperations, convey.ShouldBeNil)
		convey.So(dp.Semantic, convey.ShouldEqual, "")
		convey.So(dp.Required, convey.ShouldBeFalse)
		convey.So(dp.Constraints, convey.ShouldBeNil)

		lp := d.ObjectTypes[0].LogicProperties[0]
		convey.So(lp.Name, convey.ShouldEqual, "gmv")
		convey.So(string(lp.Type), convey.ShouldEqual, "metric")
		convey.So(lp.DisplayName, convey.ShouldEqual, "")
		convey.So(lp.Comment, convey.ShouldEqual, "")
		convey.So(lp.DataSource, convey.ShouldBeNil)
		convey.So(lp.Parameters, convey.ShouldBeNil)

		r := d.RelationTypes[0]
		convey.So(r.SourceObjectTypeID, convey.ShouldEqual, "ot_customer")
		convey.So(r.TargetObjectTypeID, convey.ShouldEqual, "ot_order")
		convey.So(r.MappingRules, convey.ShouldBeNil)
		convey.So(r.SourceObjectType, convey.ShouldBeNil)
		convey.So(r.TargetObjectType, convey.ShouldBeNil)

		g := d.ConceptGroups[0]
		convey.So(g.ObjectTypeIDs, convey.ShouldResemble, []string{"ot_order"})
		convey.So(g.ObjectTypes, convey.ShouldBeNil)
		convey.So(g.RelationTypes, convey.ShouldBeNil)
		convey.So(g.ActionTypes, convey.ShouldBeNil)
		// The group semantics are the whole point of listing groups: an agent picks
		// which ones to pass to search_scope.concept_groups from these, so summary
		// must not strip them the way it strips the duplicated type arrays.
		convey.So(g.Tags, convey.ShouldResemble, []string{"sales", "core"})
		convey.So(g.Comment, convey.ShouldEqual, "orders and their customers")
	})
}

func TestSlim_Full_KeepsPropertiesButStillDedups(t *testing.T) {
	convey.Convey("full keeps heavy property fields but still dedups concept_groups and omits property declarations", t, func() {
		d := newDetailFixture()
		d.Slim(DetailLevelFull)

		dp := d.ObjectTypes[0].DataProperties[0]
		convey.So(dp.MappedField, convey.ShouldNotBeNil)
		convey.So(dp.ConditionOperations, convey.ShouldNotBeNil)
		convey.So(d.RelationTypes[0].MappingRules, convey.ShouldNotBeNil)
		// Property declarations are search_schema's alone, at every level (#2260).
		convey.So(dp.Semantic, convey.ShouldEqual, "")
		convey.So(dp.Required, convey.ShouldBeFalse)
		convey.So(dp.Constraints, convey.ShouldBeNil)

		g := d.ConceptGroups[0]
		convey.So(g.ObjectTypeIDs, convey.ShouldResemble, []string{"ot_order"})
		convey.So(g.ObjectTypes, convey.ShouldBeNil)
		convey.So(g.RelationTypes, convey.ShouldBeNil)
		convey.So(g.ActionTypes, convey.ShouldBeNil)
		convey.So(g.Tags, convey.ShouldResemble, []string{"sales", "core"})
		convey.So(g.Comment, convey.ShouldEqual, "orders and their customers")
	})
}

func TestSlim_NilSafe(t *testing.T) {
	convey.Convey("Slim tolerates nil receiver and nil elements", t, func() {
		var d *KnowledgeNetworkDetail
		convey.So(func() { d.Slim(DetailLevelSummary) }, convey.ShouldNotPanic)

		d2 := &KnowledgeNetworkDetail{
			ObjectTypes:   []*ObjectType{nil},
			RelationTypes: []*RelationType{nil},
			ConceptGroups: []*ConceptGroup{nil},
		}
		convey.So(func() { d2.Slim(DetailLevelSummary) }, convey.ShouldNotPanic)
	})
}

func TestFilterObjectTypes(t *testing.T) {
	convey.Convey("FilterObjectTypes matches by id/name, dedups, reports missing", t, func() {
		d := newDetailFixture()

		matched, missing := d.FilterObjectTypes([]string{"ot_order"})
		convey.So(len(matched), convey.ShouldEqual, 1)
		convey.So(matched[0].ID, convey.ShouldEqual, "ot_order")
		convey.So(matched[0].DataProperties[0].MappedField, convey.ShouldNotBeNil)  // full detail retained
		convey.So(matched[0].DataProperties[0].Comment, convey.ShouldEqual, "订单金额") // comment kept in drill
		convey.So(matched[0].DataProperties[0].DisplayName, convey.ShouldEqual, "") // display_name dropped in drill
		convey.So(missing, convey.ShouldBeEmpty)

		matched, _ = d.FilterObjectTypes([]string{"order"}) // by name
		convey.So(len(matched), convey.ShouldEqual, 1)
		convey.So(matched[0].ID, convey.ShouldEqual, "ot_order")

		matched, _ = d.FilterObjectTypes([]string{"ot_order", "ot_order"}) // dedup
		convey.So(len(matched), convey.ShouldEqual, 1)

		matched, missing = d.FilterObjectTypes([]string{"nope"})
		convey.So(matched, convey.ShouldBeEmpty)
		convey.So(missing, convey.ShouldResemble, []string{"nope"})

		var nilD *KnowledgeNetworkDetail
		matched, missing = nilD.FilterObjectTypes([]string{"a"})
		convey.So(matched, convey.ShouldBeEmpty)
		convey.So(missing, convey.ShouldResemble, []string{"a"})
	})
}

func TestFilterRelationTypes(t *testing.T) {
	convey.Convey("FilterRelationTypes matches by id/name, dedups, reports missing", t, func() {
		d := newDetailFixture()

		matched, missing := d.FilterRelationTypes([]string{"rt_places"})
		convey.So(len(matched), convey.ShouldEqual, 1)
		convey.So(matched[0].ID, convey.ShouldEqual, "rt_places")
		convey.So(matched[0].MappingRules, convey.ShouldNotBeNil) // full detail retained
		convey.So(missing, convey.ShouldBeEmpty)

		matched, missing = d.FilterRelationTypes([]string{"places", "ghost"})
		convey.So(len(matched), convey.ShouldEqual, 1)
		convey.So(missing, convey.ShouldResemble, []string{"ghost"})
	})
}

// TestConceptGroupDecode_KeepsSemantics guards the boundary this struct sits on.
//
// BKN returns tags and comment for every concept group, but this struct used to
// declare neither, and decoding drops undeclared keys without erroring - so the
// semantics vanished with nothing in the logs to show for it, and an agent had
// only opaque ids to choose from. Decoding a realistic payload is the only way to
// notice if the fields are dropped again.
func TestConceptGroupDecode_KeepsSemantics(t *testing.T) {
	convey.Convey("decoding a BKN concept group keeps tags and comment", t, func() {
		const payload = `{
			"id": "kn-1",
			"concept_groups": [{
				"id": "cg_inventory",
				"name": "库存与可供",
				"tags": ["可供", "库存"],
				"comment": "库存、仓库、可用量、理论可产",
				"object_type_ids": ["ot_inventory"]
			}]
		}`

		var detail KnowledgeNetworkDetail
		convey.So(sonic.Unmarshal([]byte(payload), &detail), convey.ShouldBeNil)
		convey.So(detail.ConceptGroups, convey.ShouldHaveLength, 1)

		g := detail.ConceptGroups[0]
		convey.So(g.ID, convey.ShouldEqual, "cg_inventory")
		convey.So(g.Tags, convey.ShouldResemble, []string{"可供", "库存"})
		convey.So(g.Comment, convey.ShouldEqual, "库存、仓库、可用量、理论可产")
	})
}

// indirectRelationJSON is an indirect relation as bkn-backend returns it: its relation properties
// ride in mapping_rules.properties with their declarations, beside the relation declarations.
const indirectRelationJSON = `{"id":"supplier_record","name":"合作记录","type":"indirect",
	"source_object_type_id":"supplier","target_object_type_id":"supplier",
	"aliases":["合作记录"],"cardinality":"one_to_one","reverse_name":"被记录",
	"importance":"secondary","fallback_for":"supplier_identity",
	"lifecycle":{"status":"deprecated","reason":"merged","replaced_by":"supplier_link"},
	"mapping_rules":{
		"backing_data_source":{"type":"resource","id":"res_suppliers"},
		"source_mapping_rules":[{"source_property":{"name":"supplier_id"},"target_property":{"name":"supplier_id"}}],
		"target_mapping_rules":[{"source_property":{"name":"supplier_id"},"target_property":{"name":"supplier_id"}}],
		"properties":[{"name":"cooperation_status","display_name":"记录合作状态","type":"string","comment":"状态",
			"mapped_field":{"name":"cooperation_status"},"aliases":["合作记录状态"],"semantic":"status",
			"required":true,"constraints":{"length":{"max":20}}}]}}`

// assertRelationPropertyDeclarationsOmitted checks that the relation properties of an indirect
// relation answer as object type properties do: without aliases, semantic, required and constraints,
// with the definition and mapping kept.
func assertRelationPropertyDeclarationsOmitted(rel *RelationType) {
	out, err := sonic.Marshal(rel)
	convey.So(err, convey.ShouldBeNil)
	var got struct {
		MappingRules struct {
			BackingDataSource  map[string]any   `json:"backing_data_source"`
			SourceMappingRules []map[string]any `json:"source_mapping_rules"`
			Properties         []map[string]any `json:"properties"`
		} `json:"mapping_rules"`
	}
	convey.So(sonic.Unmarshal(out, &got), convey.ShouldBeNil)
	convey.So(got.MappingRules.BackingDataSource["id"], convey.ShouldEqual, "res_suppliers")
	convey.So(got.MappingRules.SourceMappingRules, convey.ShouldHaveLength, 1)
	convey.So(got.MappingRules.Properties, convey.ShouldHaveLength, 1)
	prop := got.MappingRules.Properties[0]
	for _, key := range []string{"aliases", "semantic", "required", "constraints"} {
		convey.So(prop, convey.ShouldNotContainKey, key)
	}
	convey.So(prop["name"], convey.ShouldEqual, "cooperation_status")
	convey.So(prop["display_name"], convey.ShouldEqual, "记录合作状态")
	convey.So(prop["type"], convey.ShouldEqual, "string")
	convey.So(prop["comment"], convey.ShouldEqual, "状态")
	convey.So(prop["mapped_field"], convey.ShouldResemble, map[string]any{"name": "cooperation_status"})
}

// get_relation_types leaves out the declarations of relation properties, as get_object_types does for
// object type properties; lifecycle and the fallback marking stay (#2893).
func TestOmitRelationDeclarations_RelationProperties(t *testing.T) {
	convey.Convey("relation property declarations are omitted, the relation's kept exceptions stay", t, func() {
		var rel RelationType
		convey.So(sonic.UnmarshalString(indirectRelationJSON, &rel), convey.ShouldBeNil)
		OmitRelationDeclarations([]*RelationType{&rel})

		assertRelationPropertyDeclarationsOmitted(&rel)
		convey.So(rel.Aliases, convey.ShouldBeNil)
		convey.So(rel.Cardinality, convey.ShouldEqual, "")
		convey.So(rel.ReverseName, convey.ShouldEqual, "")
		convey.So(rel.Importance, convey.ShouldEqual, "secondary")
		convey.So(rel.FallbackFor, convey.ShouldEqual, "supplier_identity")
		convey.So(rel.Lifecycle.ReplacedBy, convey.ShouldEqual, "supplier_link")
	})

	convey.Convey("a direct relation's mapping is left as it is", t, func() {
		const direct = `{"id":"supplier_identity","type":"direct","mapping_rules":[
			{"source_property":{"name":"supplier_id"},"target_property":{"name":"supplier_id"}}]}`
		var rel, want RelationType
		convey.So(sonic.UnmarshalString(direct, &rel), convey.ShouldBeNil)
		convey.So(sonic.UnmarshalString(direct, &want), convey.ShouldBeNil)
		convey.So(func() { OmitRelationDeclarations([]*RelationType{&rel}) }, convey.ShouldNotPanic)
		convey.So(rel.MappingRules, convey.ShouldResemble, want.MappingRules)
	})

	convey.Convey("malformed relation properties are skipped without panic", t, func() {
		for _, rules := range []any{
			map[string]any{"properties": nil},
			map[string]any{"properties": "x"},
			map[string]any{"properties": []any{nil, "x", map[string]any{"name": "p", "semantic": "status"}}},
		} {
			rel := &RelationType{Type: "indirect", MappingRules: rules}
			convey.So(func() { OmitRelationDeclarations([]*RelationType{rel, nil}) }, convey.ShouldNotPanic)
		}
	})
}

// get_kn_detail at the full level carries mapping_rules, so its relation properties go without their
// declarations too (#2893).
func TestSlim_Full_OmitsRelationPropertyDeclarations(t *testing.T) {
	convey.Convey("full level omits relation property declarations", t, func() {
		var rel RelationType
		convey.So(sonic.UnmarshalString(indirectRelationJSON, &rel), convey.ShouldBeNil)
		d := &KnowledgeNetworkDetail{ID: "kn-1", RelationTypes: []*RelationType{&rel}}
		d.Slim(DetailLevelFull)
		assertRelationPropertyDeclarationsOmitted(d.RelationTypes[0])
	})
}
