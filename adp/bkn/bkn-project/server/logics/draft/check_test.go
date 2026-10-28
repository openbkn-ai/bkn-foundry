// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// checkFixture is the compile fixture as drafting leaves it: the link table in the catalog of
// its ends.
func checkFixture() *interfaces.DraftItems {
	items := compileFixture()
	items.RelationTypes[1].LinkTable.CatalogID = "cat"
	return items
}

func Test_CheckInputOf(t *testing.T) {
	Convey("Test checkInputOf\n", t, func() {
		Convey("It keeps what confirming writes: adopted object types, and relations whose ends are\n", func() {
			input := checkInputOf(checkFixture())

			ids := []string{}
			for _, ot := range input.ObjectTypes {
				ids = append(ids, ot.ID)
			}
			So(ids, ShouldResemble, []string{"orders", "customers", "products"})
			So(input.ObjectTypes[0].Table.ResourceID, ShouldEqual, "res_orders")
			So(input.ObjectTypes[0].KeyFields, ShouldResemble, []string{"order_id"})
			So(len(input.RelationTypes), ShouldEqual, 2)
		})

		Convey("A direct relation joins its source field to the target key\n", func() {
			input := checkInputOf(checkFixture())

			rt := input.RelationTypes[0]
			So(rt.ID, ShouldEqual, "orders_to_customers")
			So(rt.Joins, ShouldResemble, []checkJoin{{
				From: interfaces.DraftTable{ResourceID: "res_orders", Name: "orders", CatalogID: "cat"}, Field: "customer_id",
				To: interfaces.DraftTable{ResourceID: "res_customers", Name: "customers", CatalogID: "cat"}, Key: "customer_id",
				ObjectType: "customers",
			}})
		})

		Convey("A relation through a link table joins the link table to each end\n", func() {
			input := checkInputOf(checkFixture())

			joins := input.RelationTypes[1].Joins
			So(len(joins), ShouldEqual, 2)
			So([]string{joins[0].From.ResourceID, joins[0].Field, joins[0].To.ResourceID, joins[0].Key, joins[0].ObjectType},
				ShouldResemble, []string{"res_order_lines", "order_id", "res_orders", "order_id", "orders"})
			So([]string{joins[1].From.ResourceID, joins[1].Field, joins[1].To.ResourceID, joins[1].Key, joins[1].ObjectType},
				ShouldResemble, []string{"res_order_lines", "product_id", "res_products", "product_id", "products"})
		})

		Convey("A relation mapped by several fields is kept, with why it is not counted\n", func() {
			items := checkFixture()
			items.RelationTypes[0].Mapping = append(items.RelationTypes[0].Mapping,
				interfaces.DraftFieldMapping{SourceProperty: "status", TargetProperty: "customer_id"})

			joins := checkInputOf(items).RelationTypes[0].Joins

			So(len(joins), ShouldEqual, 1)
			So(joins[0].Reason, ShouldEqual, checkReasonSeveralFields)
		})

		Convey("An object type keeps its properties' fields and its excluded fields\n", func() {
			items := checkFixture()
			items.Excluded = []*interfaces.DraftExcludedField{
				{ObjectType: "customers", Field: "phone"}, {ObjectType: "audit_log", Field: "ip"},
			}

			input := checkInputOf(items)

			So(input.ObjectTypes[1].Excluded, ShouldResemble, []string{"phone"})
			So(input.ObjectTypes[1].Properties, ShouldResemble, []checkProperty{{Name: "customer_id", Field: "customer_id"}})
		})
	})
}

func Test_RunCheck(t *testing.T) {
	Convey("Test runCheck\n", t, func() {
		ctx := context.Background()
		counter := &fakeCounter{
			keys: map[string][3]int64{},
			joins: map[joinKey][4]int64{
				{"res_orders", "customer_id"}:     {800, 0, 0, 60},
				{"res_order_lines", "order_id"}:   {900, 0, 0, 700},
				{"res_order_lines", "product_id"}: {900, 0, 0, 40},
			},
			failed: map[string]bool{},
		}
		input := checkInputOf(checkFixture())

		Convey("Unique, filled keys and relations that reach their ends pass\n", func() {
			results := runCheck(ctx, counter, input, nil)

			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_PASSED)
			So(len(results.Keys), ShouldEqual, 3)
			So(results.Keys[0], ShouldResemble, checkKeyResult{
				ObjectType: "orders", Fields: []string{"order_id"}, State: checkCounted, Rows: 10,
			})
			So(results.Relations[0].Joins[0].State, ShouldEqual, checkCounted)
			So(results.Relations[0].Joins[0].HitRate, ShouldEqual, 1)
			So(len(results.Relations[1].Joins), ShouldEqual, 2)
		})

		Convey("Duplicate identities are issues\n", func() {
			counter.keys["res_customers.customer_id"] = [3]int64{60, 58, 0}

			results := runCheck(ctx, counter, input, nil)

			So(results.Keys[1].Duplicates, ShouldEqual, 2)
			So(results.Keys[1].Nulls, ShouldEqual, 0)
			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_ISSUES)
		})

		Convey("Missing identities are issues\n", func() {
			counter.keys["res_customers.customer_id"] = [3]int64{60, 59, 1}

			results := runCheck(ctx, counter, input, nil)

			// The record without an identity is not a duplicate of another.
			So(results.Keys[1].Duplicates, ShouldEqual, 0)
			So(results.Keys[1].Nulls, ShouldEqual, 1)
			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_ISSUES)
		})

		Convey("Records that find no end are reported, but are not an issue\n", func() {
			counter.joins[joinKey{"res_orders", "customer_id"}] = [4]int64{800, 10, 40, 60}

			results := runCheck(ctx, counter, input, nil)

			join := results.Relations[0].Joins[0]
			So(join.Orphans, ShouldEqual, 40)
			So(join.Nulls, ShouldEqual, 10)
			So(join.HitRate, ShouldAlmostEqual, 750.0/790.0)
			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_PASSED)
		})

		Convey("A property mapped to an excluded field is an issue\n", func() {
			items := checkFixture()
			items.ObjectTypes[1].Properties = append(items.ObjectTypes[1].Properties,
				&interfaces.DraftProperty{Name: "phone", MappedField: "phone"})
			items.Excluded = []*interfaces.DraftExcludedField{{ObjectType: "customers", Field: "phone"}}

			results := runCheck(ctx, counter, checkInputOf(items), nil)

			So(results.ExcludedMapped, ShouldResemble, []checkExcludedResult{
				{ObjectType: "customers", Property: "phone", Field: "phone"},
			})
			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_ISSUES)
		})

		Convey("A failed query fails its item alone, and the check\n", func() {
			counter.failed["res_customers"] = true

			results := runCheck(ctx, counter, input, nil)

			So(results.Keys[1].State, ShouldEqual, checkFailed)
			So(results.Keys[1].Reason, ShouldEqual, checkReasonQueryFailed)
			So(results.Keys[0].State, ShouldEqual, checkCounted)
			So(results.Relations[0].Joins[0].State, ShouldEqual, checkCounted)
			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_FAILED)
		})

		Convey("A retry keeps what was counted and counts only what failed\n", func() {
			counter.failed["res_customers"] = true
			counter.failed["res_order_lines.product_id"] = true
			first := runCheck(ctx, counter, input, nil)
			// Counted again, these would differ: the first counts must be kept.
			counter.keys["res_orders.order_id"] = [3]int64{99, 1, 0}
			counter.joins[joinKey{"res_orders", "customer_id"}] = [4]int64{1, 1, 0, 0}
			counter.failed = map[string]bool{}

			results := runCheck(ctx, counter, input, first)

			So(results.Keys[0].Rows, ShouldEqual, 10)
			So(results.Relations[0].Joins[0].Rows, ShouldEqual, 800)
			So(results.Keys[1].State, ShouldEqual, checkCounted)
			So(results.Relations[1].Joins[1].State, ShouldEqual, checkCounted)
			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_PASSED)
		})

		Convey("Out of time, what is left fails as timed out\n", func() {
			done, cancel := context.WithCancel(ctx)
			cancel()

			results := runCheck(done, counter, input, nil)

			So(results.Keys[0].State, ShouldEqual, checkFailed)
			So(results.Keys[0].Reason, ShouldEqual, checkReasonTimeout)
			So(results.Relations[1].Joins[1].Reason, ShouldEqual, checkReasonTimeout)
			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_FAILED)
		})

		Convey("Ends in another catalog are not counted: no failure, but no pass either\n", func() {
			items := checkFixture()
			items.ObjectTypes[1].Table.CatalogID = "other"

			results := runCheck(ctx, counter, checkInputOf(items), nil)

			So(results.Relations[0].Joins[0].State, ShouldEqual, checkNotCounted)
			So(results.Relations[0].Joins[0].Reason, ShouldEqual, checkReasonCrossCatalog)
			So(results.Coverage, ShouldResemble, checkCoverage{Counted: 5, Total: 6})
			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_PARTIAL)
		})

		Convey("What is wrong outranks what was not counted\n", func() {
			items := checkFixture()
			items.ObjectTypes[1].Table.CatalogID = "other"
			counter.keys["res_orders.order_id"] = [3]int64{10, 9, 0}

			results := runCheck(ctx, counter, checkInputOf(items), nil)

			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_ISSUES)
		})

		Convey("Everything counted covers every item\n", func() {
			results := runCheck(ctx, counter, input, nil)

			So(results.Coverage, ShouldResemble, checkCoverage{Counted: 6, Total: 6})
		})

		Convey("An identity of several properties counts the fields together\n", func() {
			items := checkFixture()
			items.ObjectTypes[0].PrimaryKeys = []string{"order_id", "channel"}
			counter.keys["res_orders.order_id+channel"] = [3]int64{50, 48, 0}

			results := runCheck(ctx, counter, checkInputOf(items), nil)

			So(results.Keys[0].Fields, ShouldResemble, []string{"order_id", "channel"})
			So(results.Keys[0].Duplicates, ShouldEqual, 2)
		})

		Convey("Results list nothing as empty lists, not missing ones\n", func() {
			results := runCheck(ctx, counter, &checkInput{}, nil)

			out, err := json.Marshal(results)

			So(err, ShouldBeNil)
			So(string(out), ShouldEqual, `{"keys":[],"relations":[],"excluded_mapped":[],"coverage":{"counted":0,"total":0}}`)
			So(checkStatus(results), ShouldEqual, interfaces.CHECK_STATUS_PASSED)
		})
	})
}
