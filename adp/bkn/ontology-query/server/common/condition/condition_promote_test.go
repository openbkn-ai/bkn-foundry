// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func Test_PromoteLegacyLeafWithSubConds(t *testing.T) {
	Convey("PromoteLegacyLeafWithSubConds", t, func() {
		Convey("promotes leaf with nested sub_conditions into and", func() {
			cfg := &CondCfg{
				Name:      "product_name",
				Operation: OperationEq,
				ValueOptCfg: ValueOptCfg{
					Value: "UAV-BF-IND-H30",
				},
				SubConds: []*CondCfg{
					{
						Name:      "customer_name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "Acme",
						},
					},
				},
			}

			got := PromoteLegacyLeafWithSubConds(cfg)
			So(got, ShouldNotBeNil)
			So(got.Operation, ShouldEqual, OperationAnd)
			So(len(got.SubConds), ShouldEqual, 2)
			So(got.SubConds[0].Name, ShouldEqual, "product_name")
			So(got.SubConds[0].Operation, ShouldEqual, OperationEq)
			So(got.SubConds[1].Name, ShouldEqual, "customer_name")
			So(got.SubConds[1].Operation, ShouldEqual, OperationEq)
		})

		Convey("leaves proper and trees unchanged structurally", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{Name: "a", Operation: OperationEq, ValueOptCfg: ValueOptCfg{Value: 1}},
					{Name: "b", Operation: OperationEq, ValueOptCfg: ValueOptCfg{Value: 2}},
				},
			}
			got := PromoteLegacyLeafWithSubConds(cfg)
			So(got.Operation, ShouldEqual, OperationAnd)
			So(len(got.SubConds), ShouldEqual, 2)
		})
	})
}
