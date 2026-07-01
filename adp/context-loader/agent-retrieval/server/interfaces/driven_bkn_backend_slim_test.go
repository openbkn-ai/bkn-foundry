// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func newDetailFixture() *KnowledgeNetworkDetail {
	obj := &ObjectType{
		ID:   "ot_order",
		Name: "order",
		DataProperties: []*DataProperty{{
			Name:                "amount",
			Type:                "double",
			Comment:             "订单金额",
			MappedField:         map[string]any{"column": "amt"},
			ConditionOperations: []KnOperationType{"gt", "lt"},
		}},
		LogicProperties: []*LogicPropertyDef{{
			Name:       "gmv",
			Type:       "metric",
			DataSource: map[string]any{"formula": "sum(amount)"},
			Parameters: []PropertyParameter{{Name: "window", Type: "string"}},
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

		dp := d.ObjectTypes[0].DataProperties[0]
		convey.So(dp.Name, convey.ShouldEqual, "amount")
		convey.So(dp.Type, convey.ShouldEqual, "double")
		convey.So(dp.Comment, convey.ShouldEqual, "订单金额")
		convey.So(dp.MappedField, convey.ShouldBeNil)
		convey.So(dp.ConditionOperations, convey.ShouldBeNil)

		lp := d.ObjectTypes[0].LogicProperties[0]
		convey.So(lp.Name, convey.ShouldEqual, "gmv")
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
	})
}

func TestSlim_Full_KeepsPropertiesButStillDedups(t *testing.T) {
	convey.Convey("full keeps heavy property fields but still dedups concept_groups", t, func() {
		d := newDetailFixture()
		d.Slim(DetailLevelFull)

		dp := d.ObjectTypes[0].DataProperties[0]
		convey.So(dp.MappedField, convey.ShouldNotBeNil)
		convey.So(dp.ConditionOperations, convey.ShouldNotBeNil)
		convey.So(d.RelationTypes[0].MappingRules, convey.ShouldNotBeNil)

		g := d.ConceptGroups[0]
		convey.So(g.ObjectTypeIDs, convey.ShouldResemble, []string{"ot_order"})
		convey.So(g.ObjectTypes, convey.ShouldBeNil)
		convey.So(g.RelationTypes, convey.ShouldBeNil)
		convey.So(g.ActionTypes, convey.ShouldBeNil)
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
		convey.So(matched[0].DataProperties[0].MappedField, convey.ShouldNotBeNil) // full detail retained
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
