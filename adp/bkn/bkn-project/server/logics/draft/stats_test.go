// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// recordingVega answers every query with rows and records the SQL.
type recordingVega struct {
	interfaces.VegaAccess
	sql     []string
	timeout int
	rows    []map[string]any
}

func (v *recordingVega) Query(_ context.Context, _ interfaces.VegaAccount, sql string, timeout int) ([]map[string]any, error) {
	v.sql = append(v.sql, sql)
	v.timeout = timeout
	return v.rows, nil
}

func Test_VegaCounter(t *testing.T) {
	Convey("Test vegaCounter\n", t, func() {
		ctx := context.Background()
		vega := &recordingVega{}
		c := &vegaCounter{va: vega, account: interfaces.VegaAccount{ID: "u1", Type: "user"}, timeoutSeconds: 60}
		orders := &interfaces.VegaTable{ID: "r_orders"}
		customers := &interfaces.VegaTable{ID: "r_customers"}

		Convey("keyStats counts rows, distinct keys and nulls of the key\n", func() {
			vega.rows = []map[string]any{{"total": float64(60), "distinct_values": "59", "nulls": float64(1)}}

			rows, distinct, nulls, err := c.keyStats(ctx, customers, "customer`id")

			So(err, ShouldBeNil)
			So([]int64{rows, distinct, nulls}, ShouldResemble, []int64{60, 59, 1})
			So(vega.sql[0], ShouldEqual, "SELECT COUNT(*) AS total, COUNT(DISTINCT `customer``id`) AS distinct_values, "+
				"SUM(CASE WHEN `customer``id` IS NULL THEN 1 ELSE 0 END) AS nulls FROM {{r_customers}}")
			So(vega.timeout, ShouldEqual, 60)
		})

		Convey("joinStats joins the distinct target keys so duplicates do not multiply rows\n", func() {
			vega.rows = []map[string]any{{"total": float64(800), "nulls": float64(0), "orphans": float64(4)}}

			rows, nulls, orphans, err := c.joinStats(ctx, orders, "customer_id", customers, "customer_id")

			So(err, ShouldBeNil)
			So([]int64{rows, nulls, orphans}, ShouldResemble, []int64{800, 0, 4})
			So(vega.sql[0], ShouldEqual, "SELECT COUNT(*) AS total, "+
				"SUM(CASE WHEN s.`customer_id` IS NULL THEN 1 ELSE 0 END) AS nulls, "+
				"SUM(CASE WHEN s.`customer_id` IS NOT NULL AND t.`customer_id` IS NULL THEN 1 ELSE 0 END) AS orphans "+
				"FROM {{r_orders}} s LEFT JOIN (SELECT DISTINCT `customer_id` FROM {{r_customers}}) t ON s.`customer_id` = t.`customer_id`")
		})

		Convey("An answer without exactly one row is an error\n", func() {
			vega.rows = nil

			_, _, _, err := c.keyStats(ctx, customers, "id")

			So(err, ShouldNotBeNil)
		})

		Convey("codeValues reads codes and names, at most the limit\n", func() {
			vega.rows = []map[string]any{{"code_value": "T1", "display_value": "Tier 1"}, {"code_value": float64(2), "display_value": nil}}

			values, err := c.codeValues(ctx, customers, "code", "name", 501)

			So(err, ShouldBeNil)
			So(values, ShouldResemble, []interfaces.DraftEnumValue{{Value: "T1", Display: "Tier 1"}, {Value: "2", Display: ""}})
			So(vega.sql[0], ShouldEqual, "SELECT `code` AS code_value, `name` AS display_value FROM {{r_customers}} ORDER BY `code` LIMIT 501")
		})
	})
}
