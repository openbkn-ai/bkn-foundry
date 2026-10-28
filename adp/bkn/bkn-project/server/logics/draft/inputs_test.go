// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_DraftInputsOf(t *testing.T) {
	Convey("Test draftInputsOf\n", t, func() {
		customers := func() *interfaces.VegaTable {
			return table("r_customers", "c1", "customers", 60, []string{"id"},
				col("id", "string", ""), col("level", "string", "customer level"))
		}
		orders := table("r_orders", "c1", "orders", 800, []string{"order_id"}, col("order_id", "string", ""))

		Convey("Tables are listed by resource ID, whatever order they were read in\n", func() {
			a := draftInputsOf(testSetting, []string{"c1"}, []*interfaces.VegaTable{orders, customers()}, nil)
			b := draftInputsOf(testSetting, []string{"c1"}, []*interfaces.VegaTable{customers(), orders}, nil)

			So(a, ShouldResemble, b)
			So(a.Tables[0].ResourceID, ShouldEqual, "r_customers")
			So(a.Partial, ShouldBeFalse)
		})

		Convey("The fingerprint changes with the structure, not with the row count\n", func() {
			before := draftInputsOf(testSetting, []string{"c1"}, []*interfaces.VegaTable{customers()}, nil).Tables[0]
			grown := customers()
			grown.Rows = 61
			retyped := customers()
			retyped.Columns[1].Type = "integer"
			described := customers()
			described.Columns[1].Description = "tier"

			So(draftInputsOf(testSetting, nil, []*interfaces.VegaTable{grown}, nil).Tables[0].Fingerprint,
				ShouldEqual, before.Fingerprint)
			So(draftInputsOf(testSetting, nil, []*interfaces.VegaTable{retyped}, nil).Tables[0].Fingerprint,
				ShouldNotEqual, before.Fingerprint)
			// Names and descriptions from the catalog's semantic understanding name the drafted items.
			So(draftInputsOf(testSetting, nil, []*interfaces.VegaTable{described}, nil).Tables[0].Fingerprint,
				ShouldNotEqual, before.Fingerprint)
		})

		Convey("A draft missing tables it could not read is partial\n", func() {
			inputs := draftInputsOf(testSetting, []string{"c1"}, []*interfaces.VegaTable{orders},
				[]*interfaces.DraftTable{{ResourceID: "r_hidden", Reason: unreadableNotVisible}})

			So(inputs.Partial, ShouldBeTrue)
		})
	})
}
