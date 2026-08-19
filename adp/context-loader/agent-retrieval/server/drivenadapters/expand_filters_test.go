// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// TestExpandFilters locks the equivalence between the flat `filters` shortcut
// and the nested `condition` it expands to. The "Messi" case mirrors the
// condition that was verified live to return the correct goal rows.
func TestExpandFilters(t *testing.T) {
	convey.Convey("expandFilters", t, func() {

		convey.Convey("filters 展开为等价的 and 嵌套 condition（梅西用例）", func() {
			req := &interfaces.QueryObjectInstancesReq{
				OtID: "goals",
				Filters: []interfaces.FlatFilter{
					{Field: "family_name", Op: interfaces.KnOperationTypeEqual, Value: "Messi"},
					{Field: "own_goal", Op: interfaces.KnOperationTypeEqual, Value: 0},
				},
			}

			expandFilters(req)

			// Clear syntactic-sugar fields and do not send them to ontology-query.
			convey.So(req.Filters, convey.ShouldBeNil)

			// Equivalent to a handwritten nested AND condition where each leaf has value_from=const.
			convey.So(req.Cond, convey.ShouldNotBeNil)
			convey.So(req.Cond.Operation, convey.ShouldEqual, interfaces.KnOperationTypeAnd)
			convey.So(req.Cond.SubConditions, convey.ShouldHaveLength, 2)

			s0 := req.Cond.SubConditions[0]
			convey.So(s0.Field, convey.ShouldEqual, "family_name")
			convey.So(s0.Operation, convey.ShouldEqual, interfaces.KnOperationTypeEqual)
			convey.So(s0.Value, convey.ShouldEqual, "Messi")
			convey.So(s0.ValueFrom, convey.ShouldEqual, interfaces.CondValueFromConst)

			s1 := req.Cond.SubConditions[1]
			convey.So(s1.Field, convey.ShouldEqual, "own_goal")
			convey.So(s1.Operation, convey.ShouldEqual, interfaces.KnOperationTypeEqual)
			convey.So(s1.Value, convey.ShouldEqual, 0)
			convey.So(s1.ValueFrom, convey.ShouldEqual, interfaces.CondValueFromConst)
		})

		convey.Convey("condition 与 filters 同传时 condition 优先，filters 被清空", func() {
			req := &interfaces.QueryObjectInstancesReq{
				Cond:    &interfaces.KnCondition{Operation: interfaces.KnOperationTypeOr},
				Filters: []interfaces.FlatFilter{{Field: "x", Op: interfaces.KnOperationTypeEqual, Value: 1}},
			}

			expandFilters(req)

			// The existing condition is not overwritten; it remains OR, not the AND expanded from filters.
			convey.So(req.Cond.Operation, convey.ShouldEqual, interfaces.KnOperationTypeOr)
			convey.So(req.Cond.SubConditions, convey.ShouldBeEmpty)
			convey.So(req.Filters, convey.ShouldBeNil)
		})

		convey.Convey("filters 为空时不改动 condition", func() {
			req := &interfaces.QueryObjectInstancesReq{OtID: "goals"}
			expandFilters(req)
			convey.So(req.Cond, convey.ShouldBeNil)
			convey.So(req.Filters, convey.ShouldBeNil)
		})

		convey.Convey("in 算子的数组值原样透传", func() {
			req := &interfaces.QueryObjectInstancesReq{
				Filters: []interfaces.FlatFilter{
					{Field: "tournament_name", Op: interfaces.KnOperationTypeIn, Value: []any{"2014", "2022"}},
				},
			}
			expandFilters(req)
			convey.So(req.Cond.SubConditions, convey.ShouldHaveLength, 1)
			leaf := req.Cond.SubConditions[0]
			convey.So(leaf.Operation, convey.ShouldEqual, interfaces.KnOperationTypeIn)
			convey.So(leaf.Value, convey.ShouldResemble, []any{"2014", "2022"})
		})
	})
}
