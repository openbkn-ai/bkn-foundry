// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func newExportKNFixture() *KN {
	ot := &ObjectType{
		ObjectTypeWithKeyField: ObjectTypeWithKeyField{
			OTID:   "ot_order",
			OTName: "order",
			DataProperties: []*DataProperty{{
				Name:                "amount",
				DisplayName:         "金额",
				Type:                "double",
				Comment:             "订单金额",
				MappedField:         &Field{},
				IndexConfig:         &IndexConfig{},
				ConditionOperations: []string{"gt", "lt"},
			}},
			LogicProperties: []*LogicProperty{{
				Name:         "gmv",
				Type:         "metric",
				DataSource:   &ResourceInfo{},
				Parameters:   []Parameter{{}},
				AnalysisDims: []Field{{}},
			}},
		},
	}
	rt := &RelationType{
		RelationTypeWithKeyField: RelationTypeWithKeyField{
			RTID:               "rt_places",
			RTName:             "places",
			SourceObjectTypeID: "ot_customer",
			TargetObjectTypeID: "ot_order",
			MappingRules:       []any{map[string]any{"from": "a", "to": "b"}},
		},
	}
	at := &ActionType{}
	return &KN{
		ObjectTypes:   []*ObjectType{ot},
		RelationTypes: []*RelationType{rt},
		ConceptGroups: []*ConceptGroup{{
			CGID:          "cg1",
			CGName:        "core",
			ObjectTypeIDs: []string{"ot_order"},
			ObjectTypes:   []*ObjectType{ot}, // duplicate of top-level
			RelationTypes: []*RelationType{rt},
			ActionTypes:   []*ActionType{at},
		}},
	}
}

func TestKN_SlimForSummary(t *testing.T) {
	Convey("SlimForSummary strips heavy fields, keeps property names, dedups concept_groups", t, func() {
		kn := newExportKNFixture()
		kn.SlimForSummary()

		dp := kn.ObjectTypes[0].DataProperties[0]
		So(dp.Name, ShouldEqual, "amount")
		So(dp.Type, ShouldEqual, "double")
		So(dp.Comment, ShouldEqual, "订单金额")
		So(dp.MappedField, ShouldBeNil)
		So(dp.IndexConfig, ShouldBeNil)
		So(dp.ConditionOperations, ShouldBeNil)

		lp := kn.ObjectTypes[0].LogicProperties[0]
		So(lp.Name, ShouldEqual, "gmv")
		So(lp.DataSource, ShouldBeNil)
		So(lp.Parameters, ShouldBeNil)
		So(lp.AnalysisDims, ShouldBeNil)

		So(kn.RelationTypes[0].SourceObjectTypeID, ShouldEqual, "ot_customer")
		So(kn.RelationTypes[0].MappingRules, ShouldBeNil)

		cg := kn.ConceptGroups[0]
		So(cg.ObjectTypeIDs, ShouldResemble, []string{"ot_order"})
		So(cg.ObjectTypes, ShouldBeNil)
		So(cg.RelationTypes, ShouldBeNil)
		So(cg.ActionTypes, ShouldBeNil)
	})

	Convey("SlimForSummary tolerates nil receiver and nil elements", t, func() {
		var kn *KN
		So(func() { kn.SlimForSummary() }, ShouldNotPanic)

		kn2 := &KN{
			ObjectTypes:   []*ObjectType{nil},
			RelationTypes: []*RelationType{nil},
			ConceptGroups: []*ConceptGroup{nil},
		}
		So(func() { kn2.SlimForSummary() }, ShouldNotPanic)
	})
}
