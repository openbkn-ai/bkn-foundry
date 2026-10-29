// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func objectType(id string, name string) interfaces.ReleaseItem {
	return interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: id, Name: name}
}

func Test_CompareNames(t *testing.T) {
	Convey("Test compareNames\n", t, func() {
		Convey("Names that differ only in case, width or surrounding space are the same\n", func() {
			for _, other := range []string{"order", " Order ", "Ｏｒｄｅｒ"} {
				added := objectType("order", "Order")
				same, similar := compareNames([]interfaces.ReleaseItem{added},
					[]interfaces.ReleaseItem{added, objectType("o2", other)})
				So(same, ShouldResemble, []interfaces.SimilarNames{{Added: added, Other: objectType("o2", other)}})
				So(similar, ShouldBeEmpty)
			}
		})

		Convey("Names alike but for separators, or one character apart, are similar\n", func() {
			for _, pair := range [][2]string{
				{"Sales Order", "sales_order"},
				{"采购入库单", "采购出库单"},
				{"Order", "Orders"},
				{"Part", "Parts"},
				{"Purchase Order", "Purchase-Orders"},
			} {
				added, other := objectType("a", pair[0]), objectType("b", pair[1])
				same, similar := compareNames([]interfaces.ReleaseItem{added}, []interfaces.ReleaseItem{added, other})
				So(same, ShouldBeEmpty)
				So(similar, ShouldResemble, []interfaces.SimilarNames{{Added: added, Other: other}})
			}
		})

		Convey("Short names one character apart and different names are not similar\n", func() {
			for _, pair := range [][2]string{
				{"订单", "订单项"},
				{"Bin", "Bins"},
				{"Purchase", "Purchaser1"},
				{"Supplier", "Customer"},
			} {
				added, other := objectType("a", pair[0]), objectType("b", pair[1])
				same, similar := compareNames([]interfaces.ReleaseItem{added}, []interfaces.ReleaseItem{added, other})
				So(same, ShouldBeEmpty)
				So(similar, ShouldBeEmpty)
			}
		})

		Convey("Names of different kinds are not compared\n", func() {
			added := objectType("order", "Order")
			relation := interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_RELATION_TYPE, ID: "r", Name: "Order"}
			same, similar := compareNames([]interfaces.ReleaseItem{added}, []interfaces.ReleaseItem{added, relation})
			So(same, ShouldBeEmpty)
			So(similar, ShouldBeEmpty)
		})

		Convey("Two added items alike are listed once, the first by id as the added one\n", func() {
			a, b := objectType("a", "Sales Order"), objectType("b", "Sales Orders")
			same, similar := compareNames([]interfaces.ReleaseItem{b, a}, []interfaces.ReleaseItem{a, b})
			So(same, ShouldBeEmpty)
			So(similar, ShouldResemble, []interfaces.SimilarNames{{Added: a, Other: b}})
		})

		Convey("Pairs are listed in the order of the added items' ids, then the others'\n", func() {
			a, b := objectType("a", "Order"), objectType("b", "Part Number")
			x, y := objectType("x", "Orders"), objectType("y", "Part Numbers")
			_, similar := compareNames([]interfaces.ReleaseItem{b, a}, []interfaces.ReleaseItem{y, x, b, a})
			So(similar, ShouldResemble, []interfaces.SimilarNames{{Added: a, Other: x}, {Added: b, Other: y}})
		})
	})
}
