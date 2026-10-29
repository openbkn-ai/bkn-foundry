// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var testSetting = common.DraftSetting{
	CodeTableMaxRows:    500,
	CodeTableMaxColumns: 3,
	AdoptHitRate:        0.95,
	SuggestHitRate:      0.8,
	MinInferredDistinct: 10,
	PrivacyKeywords:     append([]string{"手机"}, common.DefaultPrivacyKeywords...),
	NameFieldKeywords:   append([]string{"名称"}, common.DefaultNameFieldKeywords...),
}

type joinKey struct{ source, field string }

// fakeCounter answers counts from tables of numbers instead of SQL.
type fakeCounter struct {
	keys   map[string][3]int64  // table id -> rows, distinct, nulls
	joins  map[joinKey][4]int64 // rows, nulls, orphans, distinct source values
	codes  map[string][]interfaces.DraftEnumValue
	failed map[string]bool // table id or table.field whose queries fail
}

func (f *fakeCounter) keyStats(_ context.Context, table *interfaces.VegaTable, field string) (int64, int64, int64, error) {
	if f.failed[table.ID] || f.failed[table.ID+"."+field] {
		return 0, 0, 0, errors.New("timeout")
	}
	s, ok := f.keys[table.ID+"."+field]
	if !ok {
		s, ok = f.keys[table.ID]
	}
	if !ok {
		s = [3]int64{10, 10, 0}
	}
	return s[0], s[1], s[2], nil
}

func (f *fakeCounter) keyTupleStats(ctx context.Context, table *interfaces.VegaTable,
	fields []string) (int64, int64, int64, error) {
	return f.keyStats(ctx, table, strings.Join(fields, "+"))
}

func (f *fakeCounter) joinStats(_ context.Context, source *interfaces.VegaTable, field string,
	_ *interfaces.VegaTable, _ string) (int64, int64, int64, int64, error) {
	if f.failed[source.ID+"."+field] {
		return 0, 0, 0, 0, errors.New("timeout")
	}
	s := f.joins[joinKey{source.ID, field}]
	return s[0], s[1], s[2], s[3], nil
}

func (f *fakeCounter) codeValues(_ context.Context, table *interfaces.VegaTable, _ string, _ string,
	_ int64) ([]interfaces.DraftEnumValue, error) {
	return f.codes[table.ID], nil
}

func col(name, typ, comment string) interfaces.VegaColumn {
	return interfaces.VegaColumn{Name: name, Type: typ, Description: comment}
}

func fk(field, table, ref string) interfaces.VegaForeignKey {
	return interfaces.VegaForeignKey{Columns: []string{field}, RefTable: table, RefColumns: []string{ref}}
}

func table(id, catalog, name string, rows int64, pk []string, columns ...interfaces.VegaColumn) *interfaces.VegaTable {
	return &interfaces.VegaTable{
		ID: id, CatalogID: catalog, Name: name, SourceName: name, SourceSchema: "erp",
		Rows: rows, RowsCounted: rows > 0, PrimaryKeys: pk, Columns: columns,
	}
}

// sample is a small sales schema: customers with a level from a code table, orders of customers
// placed by a salesperson, tags on orders through a link table, events without a key, and
// invoices in another catalog.
func sample() []*interfaces.VegaTable {
	customers := table("r_customers", "c1", "customers", 60, []string{"customer_id"},
		col("customer_id", "string", "Customer number"),
		col("customer_name", "string", "Customer name"),
		col("phone", "string", "Contact phone"),
		col("level_code", "string", ""),
		col("location", "other", "Geometry"))
	customers.ForeignKeys = []interfaces.VegaForeignKey{fk("level_code", "customer_levels", "code")}
	customers.Description = "Customers; master data"

	levels := table("r_levels", "c1", "customer_levels", 3, []string{"code"},
		col("code", "string", ""), col("name", "string", ""))

	orders := table("r_orders", "c1", "orders", 800, []string{"order_id"},
		col("order_id", "string", "Order number"),
		col("customer_id", "string", "Customer"),
		col("salesperson_id", "integer", "Sales rep"),
		col("warehouse_id", "integer", ""),
		col("code", "string", "Order code"),
		col("amount", "decimal", "Amount"))
	orders.ForeignKeys = []interfaces.VegaForeignKey{fk("customer_id", "customers", "customer_id")}

	salesperson := table("r_sales", "c1", "salesperson", 0, []string{"salesperson_id"},
		col("salesperson_id", "integer", ""), col("full_name", "string", "Full name"))
	warehouse := table("r_wh", "c1", "warehouse", 0, []string{"warehouse_id"},
		col("warehouse_id", "integer", ""), col("city", "string", ""))

	tags := table("r_tags", "c1", "tags", 0, []string{"tag_id"}, col("tag_id", "string", ""), col("label", "string", ""))
	orderTags := table("r_order_tags", "c1", "order_tags", 0, nil, col("order_id", "string", ""), col("tag_id", "string", ""))
	orderTags.ForeignKeys = []interfaces.VegaForeignKey{fk("order_id", "orders", "order_id"), fk("tag_id", "tags", "tag_id")}

	events := table("r_events", "c1", "events", 0, nil, col("happened_at", "datetime", ""), col("note", "text", ""))

	invoices := table("r_invoices", "c2", "invoices", 0, []string{"invoice_id"},
		col("invoice_id", "string", ""), col("customer_id", "string", ""))
	invoices.ForeignKeys = []interfaces.VegaForeignKey{fk("customer_id", "customers", "customer_id")}

	return []*interfaces.VegaTable{customers, levels, orders, salesperson, warehouse, tags, orderTags, events, invoices}
}

func sampleCounter() *fakeCounter {
	return &fakeCounter{
		keys: map[string][3]int64{"r_customers": {60, 60, 0}, "r_orders": {800, 800, 0}},
		joins: map[joinKey][4]int64{
			{"r_orders", "customer_id"}:    {800, 0, 4, 60}, // 796 / 800 = 99.5%
			{"r_orders", "salesperson_id"}: {800, 0, 80, 40},
			{"r_orders", "warehouse_id"}:   {800, 100, 400, 30},
		},
		codes: map[string][]interfaces.DraftEnumValue{
			"r_levels": {{Value: "T1", Display: "Tier 1"}, {Value: "T2", Display: "Tier 2"}, {Value: "T3", Display: "Tier 3"}},
		},
		failed: map[string]bool{},
	}
}

func objectType(items interfaces.DraftItems, id string) *interfaces.DraftObjectType {
	for _, o := range items.ObjectTypes {
		if o.ID == id {
			return o
		}
	}
	return nil
}

func relation(items interfaces.DraftItems, source, target string) *interfaces.DraftRelationType {
	for _, r := range items.RelationTypes {
		if r.Source == source && r.Target == target {
			return r
		}
	}
	return nil
}

func Test_Drafter(t *testing.T) {
	Convey("Test drafting from table structure\n", t, func() {
		ctx := context.Background()
		run := func(c *fakeCounter) interfaces.DraftItems {
			items, err := newDrafter(testSetting, c).draft(ctx, sample(), nil)
			So(err, ShouldBeNil)
			return items
		}

		Convey("Entity tables become object types; code and link tables do not\n", func() {
			items := run(sampleCounter())

			ids := []string{}
			for _, o := range items.ObjectTypes {
				ids = append(ids, o.ID)
			}
			So(ids, ShouldResemble, []string{"customers", "events", "invoices", "orders", "salesperson", "tags", "warehouse"})
		})

		Convey("An object type maps its fields, names from comments, and keys checked by counts\n", func() {
			customers := objectType(run(sampleCounter()), "customers")

			So(customers.Name, ShouldEqual, "Customers")
			So(customers.PrimaryKeys, ShouldResemble, []string{"customer_id"})
			So(customers.DisplayKey, ShouldEqual, "customer_name")
			So(customers.Adopted, ShouldBeTrue)
			So(customers.Key, ShouldResemble, interfaces.DraftKeyEvidence{
				Source: interfaces.DRAFT_KEY_PRIMARY_KEY, Computed: true, Rows: 60, Distinct: 60,
			})
			So(customers.Table, ShouldResemble, interfaces.DraftTable{ResourceID: "r_customers", Name: "customers", CatalogID: "c1"})
			id := customers.Properties[0]
			So(id.DisplayName, ShouldEqual, "Customer number")
			So(id.MappedField, ShouldEqual, "customer_id")
			So(id.SourceOfRecord, ShouldEqual, interfaces.SOURCE_OF_RECORD_TABLE)
			So(id.MissingDescription, ShouldBeFalse)
			level := findProperty(customers, "level_code")
			So(level.DisplayName, ShouldEqual, "level_code")
			So(level.MissingDescription, ShouldBeTrue)
		})

		Convey("Privacy rules exclude personal fields but never keys; unsupported types are left out\n", func() {
			items := run(sampleCounter())

			excluded := map[string]string{}
			for _, e := range items.Excluded {
				excluded[e.ObjectType+"."+e.Field] = e.Reason
			}
			So(excluded, ShouldResemble, map[string]string{
				"customers.phone":       excludedPrivacy,
				"customers.location":    excludedUnsupported,
				"salesperson.full_name": excludedPrivacy,
			})
			So(findProperty(objectType(items, "customers"), "phone"), ShouldBeNil)
		})

		Convey("A table without a key, or with duplicates in it, waits for a person to decide\n", func() {
			c := sampleCounter()
			c.keys["r_orders"] = [3]int64{800, 798, 0}
			items := run(c)

			events := objectType(items, "events")
			So(events.NeedsIdentity, ShouldBeTrue)
			So(events.Adopted, ShouldBeFalse)
			So(events.Key.Source, ShouldEqual, interfaces.DRAFT_KEY_NONE)
			orders := objectType(items, "orders")
			So(orders.NeedsIdentity, ShouldBeTrue)
			So(orders.Key.Distinct, ShouldEqual, 798)
			// Its relations wait with it, and say why.
			r := relation(items, "orders", "customers")
			So(r.Adopted, ShouldBeFalse)
			So(r.ProposedBecause, ShouldEqual, proposedEndNotAdopted)
		})

		Convey("A code table becomes an enum on the fields that refer to it\n", func() {
			items := run(sampleCounter())

			So(len(items.ValueTypes), ShouldEqual, 1)
			levels := items.ValueTypes[0]
			So(levels.ID, ShouldEqual, "customer_levels")
			So(levels.BaseType, ShouldEqual, "string")
			So(levels.Values[0], ShouldResemble, interfaces.DraftEnumValue{Value: "T1", Display: "Tier 1"})
			So(levels.UsedBy, ShouldResemble, []interfaces.DraftPropertyRef{{ObjectType: "customers", Property: "level_code"}})
			So(levels.Adopted, ShouldBeTrue)
			So(findProperty(objectType(items, "customers"), "level_code").ValueTypeID, ShouldEqual, "customer_levels")
		})

		Convey("A declared foreign key is a confirmed relation with its full-table hit rate\n", func() {
			r := relation(run(sampleCounter()), "orders", "customers")

			So(r.Origin, ShouldEqual, interfaces.DRAFT_ORIGIN_FOREIGN_KEY)
			So(r.Verification, ShouldEqual, "confirmed")
			So(r.Cardinality, ShouldEqual, interfaces.DRAFT_CARDINALITY_MANY_TO_ONE)
			So(r.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "customer_id", TargetProperty: "customer_id"}})
			So(r.Evidence, ShouldResemble, interfaces.DraftJoinEvidence{Computed: true, Rows: 800, Hits: 796, Orphans: 4, HitRate: 0.995, SourceDistinct: 60})
			So(r.Adopted, ShouldBeTrue)
		})

		Convey("A foreign key below the suggest rate is kept, not adopted\n", func() {
			c := sampleCounter()
			c.joins[joinKey{"r_orders", "customer_id"}] = [4]int64{800, 0, 400, 60}
			r := relation(run(c), "orders", "customers")

			So(r.Evidence.HitRate, ShouldEqual, 0.5)
			So(r.Adopted, ShouldBeFalse)
			So(r.ProposedBecause, ShouldEqual, proposedBelowSuggestRate)
		})

		Convey("A relation inferred from names is proposed from the suggest rate and adopted from the adopt rate\n", func() {
			items := run(sampleCounter())

			sales := relation(items, "orders", "salesperson")
			So(sales.Origin, ShouldEqual, interfaces.DRAFT_ORIGIN_NAME_MATCH)
			So(sales.Verification, ShouldEqual, "unconfirmed")
			So(sales.Evidence.HitRate, ShouldEqual, 0.9)
			So(sales.Adopted, ShouldBeFalse)
			// 300 hits of 700 non-null rows: below the suggest rate, not proposed.
			So(relation(items, "orders", "warehouse"), ShouldBeNil)
		})

		Convey("A generic field name does not infer a relation to a table keyed by it\n", func() {
			items := run(sampleCounter())

			for _, r := range items.RelationTypes {
				So(r.Mapping, ShouldNotContain, interfaces.DraftFieldMapping{SourceProperty: "code", TargetProperty: "code"})
			}
			So(findProperty(objectType(items, "orders"), "code").ValueTypeID, ShouldBeEmpty)
		})

		Convey("An inference to another catalog is kept unverified, not dropped\n", func() {
			r := relation(run(sampleCounter()), "invoices", "customers")

			// invoices (c2) names a field for customers' key in c1; vega-backend cannot count
			// across catalogs, so the inference stays as an unverified proposal.
			So(r, ShouldNotBeNil)
			So(r.Origin, ShouldEqual, interfaces.DRAFT_ORIGIN_NAME_MATCH)
			So(r.Evidence.Computed, ShouldBeFalse)
			So(r.Evidence.Reason, ShouldEqual, reasonCrossCatalog)
			So(r.Adopted, ShouldBeFalse)
		})

		Convey("A failed count is recorded on its item and drafting goes on\n", func() {
			c := sampleCounter()
			c.failed["r_orders.customer_id"] = true
			c.failed["r_customers"] = true
			items := run(c)

			r := relation(items, "orders", "customers")
			So(r.Evidence.Reason, ShouldStartWith, reasonQueryFailed)
			So(r.Adopted, ShouldBeFalse)
			So(objectType(items, "customers").Key.Reason, ShouldStartWith, reasonQueryFailed)
			So(objectType(items, "customers").Adopted, ShouldBeTrue)
		})

		Convey("A link table becomes a many-to-many relation backed by it\n", func() {
			r := relation(run(sampleCounter()), "orders", "tags")

			So(r.Origin, ShouldEqual, interfaces.DRAFT_ORIGIN_LINK_TABLE)
			So(r.Cardinality, ShouldEqual, interfaces.DRAFT_CARDINALITY_MANY_TO_MANY)
			So(r.LinkTable.ResourceID, ShouldEqual, "r_order_tags")
			So(r.SourceMapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "order_id", TargetProperty: "order_id"}})
			So(r.TargetMapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "tag_id", TargetProperty: "tag_id"}})
			So(r.Adopted, ShouldBeTrue)
		})

		Convey("A relation of an object type that is not adopted is not adopted either\n", func() {
			c := sampleCounter()
			c.keys["r_orders"] = [3]int64{800, 790, 0}
			items := run(c)

			So(objectType(items, "orders").Adopted, ShouldBeFalse)
			// The counts stay; only adopting waits for the object type.
			fk := relation(items, "orders", "customers")
			So(fk.Evidence.HitRate, ShouldEqual, 0.995)
			So(fk.Adopted, ShouldBeFalse)
			So(relation(items, "orders", "tags").Adopted, ShouldBeFalse)
		})

		Convey("A PostgreSQL foreign key names its table with the schema\n", func() {
			customers := table("r_c", "c1", "customers", 0, []string{"id"}, col("id", "string", ""))
			customers.SourceSchema = "public"
			orders := table("r_o", "c1", "orders", 0, []string{"order_id"}, col("order_id", "string", ""), col("customer_ref", "string", ""))
			orders.SourceSchema = "public"
			orders.ForeignKeys = []interfaces.VegaForeignKey{fk("customer_ref", "public.customers", "id")}
			c := sampleCounter()
			c.joins[joinKey{"r_o", "customer_ref"}] = [4]int64{10, 0, 0, 10}

			items, err := newDrafter(testSetting, c).draft(ctx, []*interfaces.VegaTable{customers, orders}, nil)

			So(err, ShouldBeNil)
			So(relation(items, "orders", "customers"), ShouldNotBeNil)
		})

		Convey("The same tables in any order draft the same items, with the same IDs\n", func() {
			// Tables of one name in two catalogs, and in two schemas of one catalog.
			tables := func() []*interfaces.VegaTable {
				a := table("r_a", "c2", "customers", 0, []string{"id"}, col("id", "string", ""))
				b := table("r_b", "c1", "customers", 0, []string{"id"}, col("id", "string", ""))
				c := table("r_c", "c1", "customers", 0, []string{"id"}, col("id", "string", ""))
				c.SourceSchema = "crm"
				return []*interfaces.VegaTable{a, b, c}
			}
			drafted := func(order []int) string {
				in := tables()
				ordered := []*interfaces.VegaTable{in[order[0]], in[order[1]], in[order[2]]}
				items, err := newDrafter(testSetting, sampleCounter()).draft(ctx, ordered, nil)
				So(err, ShouldBeNil)
				out, err := json.Marshal(items)
				So(err, ShouldBeNil)
				return string(out)
			}

			first := drafted([]int{0, 1, 2})
			for _, order := range [][]int{{2, 1, 0}, {1, 0, 2}, {2, 0, 1}} {
				So(drafted(order), ShouldEqual, first)
			}
			// Ordered by name, then catalog and schema: the first takes the plain ID.
			var items interfaces.DraftItems
			So(json.Unmarshal([]byte(first), &items), ShouldBeNil)
			ids := map[string]string{}
			for _, ot := range items.ObjectTypes {
				ids[ot.Table.ResourceID] = ot.ID
			}
			So(ids["r_c"], ShouldEqual, "customers")
		})

		Convey("Which of two tables of one physical name takes the plain id does not follow their names\n", func() {
			// ods.orders and dw.orders; semantic understanding named them, in either language.
			drafted := func(odsName, dwName string) map[string]string {
				ods := table("r_ods", "c1", odsName, 0, []string{"order_id"}, col("order_id", "string", ""))
				ods.SourceName, ods.SourceSchema = "orders", "ods"
				dw := table("r_dw", "c1", dwName, 0, []string{"order_id"}, col("order_id", "string", ""))
				dw.SourceName, dw.SourceSchema = "orders", "dw"
				items, err := newDrafter(testSetting, sampleCounter()).draft(ctx, []*interfaces.VegaTable{ods, dw}, nil)
				So(err, ShouldBeNil)
				ids := map[string]string{}
				for _, ot := range items.ObjectTypes {
					ids[ot.Table.ResourceID] = ot.ID
				}
				return ids
			}

			// Names that sort ods first, then dw first: the ids must not swap.
			So(drafted("A orders", "B orders"), ShouldResemble, drafted("B orders", "A orders"))
		})

		Convey("A keyless table's field named id in another script is still an identity candidate\n", func() {
			// The field is not named for its table (日记账), so only reading it as id makes it a candidate.
			ledger := table("r_ledger", "c1", "日记账", 0, nil, col("流水ID", "string", ""), col("金额", "decimal", ""))
			c := sampleCounter()
			c.keys["r_ledger.流水ID"] = [3]int64{10, 10, 0}

			items, err := newDrafter(testSetting, c).draft(ctx, []*interfaces.VegaTable{ledger}, nil)

			So(err, ShouldBeNil)
			So(items.ObjectTypes[0].NeedsIdentity, ShouldBeFalse)
			So(items.ObjectTypes[0].Key.Field, ShouldEqual, "流水ID")
		})

		Convey("Ids come from the physical name; the name people read from semantic understanding\n", func() {
			// Semantic understanding renamed the resource; the source table is still gys_info.
			suppliers := table("r_gys", "c1", "供应商", 0, []string{"gys_id"}, col("gys_id", "string", ""))
			suppliers.SourceName = "gys_info"
			orders := table("r_po", "c1", "采购单", 0, []string{"po_id"}, col("po_id", "string", ""), col("gys_info_id", "string", ""))
			orders.SourceName = "po"
			c := sampleCounter()
			c.joins[joinKey{"r_po", "gys_info_id"}] = [4]int64{10, 0, 0, 10}

			items, err := newDrafter(testSetting, c).draft(ctx, []*interfaces.VegaTable{suppliers, orders}, nil)

			So(err, ShouldBeNil)
			ot := objectType(items, "gys_info")
			So(ot, ShouldNotBeNil)
			So(ot.Name, ShouldEqual, "供应商")
			// Name matching reads the physical name too: gys_info_id refers to gys_info.
			So(relation(items, "po", "gys_info"), ShouldNotBeNil)
		})

		Convey("A small table of people is not a code table, so no names are read as enum values\n", func() {
			staff := table("r_staff", "c1", "staff", 30, []string{"id"}, col("id", "string", ""), col("full_name", "string", ""))
			c := sampleCounter()
			c.codes["r_staff"] = []interfaces.DraftEnumValue{{Value: "1", Display: "Alice"}}

			items, err := newDrafter(testSetting, c).draft(ctx, []*interfaces.VegaTable{staff}, nil)

			So(err, ShouldBeNil)
			So(items.ValueTypes, ShouldBeEmpty)
			So(objectType(items, "staff"), ShouldNotBeNil)
		})

		Convey("A foreign key resolves in its own catalog when another has a table of the same name\n", func() {
			customers1 := table("r_c1", "c1", "customers", 0, []string{"id"}, col("id", "string", ""))
			orders1 := table("r_o1", "c1", "orders", 0, []string{"order_id"}, col("order_id", "string", ""), col("cust", "string", ""))
			orders1.ForeignKeys = []interfaces.VegaForeignKey{fk("cust", "customers", "id")}
			customers2 := table("r_c2", "c2", "customers", 0, []string{"id"}, col("id", "string", ""))
			c := sampleCounter()
			c.joins[joinKey{"r_o1", "cust"}] = [4]int64{10, 0, 0, 10}

			items, err := newDrafter(testSetting, c).draft(ctx,
				[]*interfaces.VegaTable{customers1, orders1, customers2}, nil)

			So(err, ShouldBeNil)
			r := relation(items, "orders", "customers")
			So(r, ShouldNotBeNil)
			So(r.Evidence.Computed, ShouldBeTrue)
			So(r.Adopted, ShouldBeTrue)
		})

		Convey("Unreadable tables are carried into the draft\n", func() {
			items, err := newDrafter(testSetting, sampleCounter()).draft(ctx, sample(),
				[]*interfaces.DraftTable{{ResourceID: "r_x", Name: "secret", Reason: "not_visible"}})

			So(err, ShouldBeNil)
			So(items.Unreadable[0].Name, ShouldEqual, "secret")
		})

		Convey("A cancelled drafting stops\n", func() {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			c := sampleCounter()
			c.failed["r_customers"] = true

			_, err := newDrafter(testSetting, c).draft(cancelled, sample(), nil)

			So(err, ShouldEqual, context.Canceled)
		})
	})
}

func Test_Rules(t *testing.T) {
	Convey("Test naming and matching rules\n", t, func() {
		Convey("idFrom makes a valid BKN id from a physical name\n", func() {
			So(idFrom("Customer Orders", idPrefixObjectType), ShouldEqual, "customer_orders")
			// camelCase is split as words are.
			So(idFrom("customerId", idPrefixProperty), ShouldEqual, "customer_id")
			So(idFrom("orderID", idPrefixProperty), ShouldEqual, "order_id")
			So(idFrom("2024_sales", idPrefixObjectType), ShouldEqual, "f_2024_sales")
			So(idFrom("###", idPrefixProperty), ShouldEqual, "field")
			So(len(idFrom("a_very_long_table_name_that_goes_on_and_on_and_on", idPrefixObjectType)),
				ShouldBeLessThanOrEqualTo, maxIDLength)
		})

		Convey("A physical name that is not ASCII stands as its kind and a short hash of the name\n", func() {
			supplier := idFrom("供应商", idPrefixObjectType)
			So(supplier, ShouldStartWith, "ot_")
			So(supplier, ShouldHaveLength, len("ot_")+6)
			// The same name always gives the same id; another gives another.
			So(idFrom("供应商", idPrefixObjectType), ShouldEqual, supplier)
			So(idFrom("采购单", idPrefixObjectType), ShouldNotEqual, supplier)
			So(idFrom("供应商编号", idPrefixProperty), ShouldStartWith, "p_")
			// Part ASCII is not cut out: order alone would clash with a table named order.
			So(idFrom("订单order", idPrefixObjectType), ShouldStartWith, "ot_")
		})

		Convey("Names are matched as written, so names that are not ASCII still match\n", func() {
			So(matchName("customerId"), ShouldEqual, "customer_id")
			So(matchName("Customer_ID"), ShouldEqual, "customer_id")
			So(matchName("供应商编号"), ShouldEqual, "供应商编号")
			So(matchName("供应商编号"), ShouldNotEqual, matchName("采购单号"))
			// Words break where the script changes: 客户ID refers to 客户 as customer_id to customer.
			So(matchName("客户ID"), ShouldEqual, "客户_id")
		})

		Convey("uniqueName adds a suffix to a taken name\n", func() {
			taken := map[string]bool{}
			So(uniqueName("orders", taken), ShouldEqual, "orders")
			So(uniqueName("orders", taken), ShouldEqual, "orders_2")
		})

		Convey("displayName takes a short first clause of the comment\n", func() {
			So(displayName("客户编号，唯一", "customer_id"), ShouldEqual, "客户编号")
			So(displayName("", "customer_id"), ShouldEqual, "customer_id")
			So(displayName("This comment is far too long to serve as a display name of a field", "f"), ShouldEqual, "f")
		})

		Convey("privacyKeyword matches names and comments, in either language\n", func() {
			keywords := testSetting.PrivacyKeywords
			So(privacyKeyword(col("mobile", "string", ""), keywords), ShouldEqual, "mobile")
			So(privacyKeyword(col("c3", "string", "联系人手机"), keywords), ShouldEqual, "手机")
			So(privacyKeyword(col("amount", "decimal", "金额"), keywords), ShouldBeEmpty)
		})

		Convey("A code table needs a known small row count and a name field\n", func() {
			levels := table("r", "c", "levels", 3, []string{"code"}, col("code", "string", ""), col("name", "string", ""))
			So(codeTableName(levels, testSetting), ShouldEqual, "name")
			levels.Rows = 0
			So(codeTableName(levels, testSetting), ShouldBeEmpty)
			big := table("r", "c", "levels", 501, []string{"code"}, col("code", "string", ""), col("name", "string", ""))
			So(codeTableName(big, testSetting), ShouldBeEmpty)
		})

		Convey("A unique index stands in for a missing primary key\n", func() {
			t := table("r", "c", "t", 0, nil, col("a", "string", ""))
			t.Indices = []interfaces.VegaIndex{{Columns: []string{"a"}, Unique: true}}
			key, source := tableKey(t)
			So(key, ShouldResemble, []string{"a"})
			So(source, ShouldEqual, interfaces.DRAFT_KEY_UNIQUE_INDEX)
		})
	})
}
