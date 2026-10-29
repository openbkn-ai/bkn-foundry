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

func registered(kind, sourceKey, id, origin string, locked bool) *interfaces.RegisteredID {
	return &interfaces.RegisteredID{ProjectID: "p1", IDKey: interfaces.IDKey{Kind: kind, SourceKey: sourceKey},
		ID: id, Origin: origin, Locked: locked}
}

func recordedIDs(d *drafter) map[interfaces.IDKey]string {
	ids := map[interfaces.IDKey]string{}
	for _, id := range d.recorded {
		ids[id.IDKey] = id.Origin + ":" + id.ID
	}
	return ids
}

func propertyNames(objectType *interfaces.DraftObjectType) []string {
	names := []string{}
	for _, p := range objectType.Properties {
		names = append(names, p.Name)
	}
	return names
}

// suppliers is a table named in Chinese, with the short names semantic understanding gave.
func suppliers() *interfaces.VegaTable {
	code, phone := col("编号", "string", ""), col("电话", "string", "")
	code.APIName = "supplier_code"
	t := table("r1", "c1", "供应商", 0, []string{"编号"}, code, phone)
	t.APIName = "supplier"
	return t
}

func Test_RegisteredIDs(t *testing.T) {
	Convey("Test ids of items drafted from names that are not ASCII\n", t, func() {
		ctx := context.Background()
		otKey := interfaces.IDKey{Kind: interfaces.ID_KIND_OBJECT_TYPES, SourceKey: "r1"}
		codeKey := interfaces.IDKey{Kind: interfaces.ID_KIND_PROPERTIES, SourceKey: "r1/编号"}
		phoneKey := interfaces.IDKey{Kind: interfaces.ID_KIND_PROPERTIES, SourceKey: "r1/电话"}
		draftWith := func(ids []*interfaces.RegisteredID, tables ...*interfaces.VegaTable) (*drafter, interfaces.DraftItems) {
			d := newDrafter(testSetting, sampleCounter()).withRegisteredIDs("p1", ids)
			items, err := d.draft(ctx, tables, nil)
			So(err, ShouldBeNil)
			return d, items
		}

		Convey("Short names give the ids and are registered; a field without one gets a placeholder\n", func() {
			d, items := draftWith(nil, suppliers())

			So(items.ObjectTypes[0].ID, ShouldEqual, "supplier")
			phone := idFrom("电话", idPrefixProperty)
			So(propertyNames(items.ObjectTypes[0]), ShouldResemble, []string{"supplier_code", phone})
			So(recordedIDs(d), ShouldResemble, map[interfaces.IDKey]string{
				otKey: "semantic:supplier", codeKey: "semantic:supplier_code", phoneKey: "fallback:" + phone,
			})
			for _, id := range d.recorded {
				So(id.ProjectID, ShouldEqual, "p1")
				So(id.Locked, ShouldBeFalse)
			}
			So(string(d.recorded[0].Basis), ShouldEqual, `{"physical_name":"供应商","api_name":"supplier"}`)
			So(d.named, ShouldResemble, map[interfaces.IDKey]bool{otKey: true, codeKey: true, phoneKey: true})
		})

		Convey("Registered ids are kept whatever the short names say now, and not recorded again\n", func() {
			d, items := draftWith([]*interfaces.RegisteredID{
				registered(otKey.Kind, otKey.SourceKey, "vendor", interfaces.ID_ORIGIN_SEMANTIC, false),
				registered(codeKey.Kind, codeKey.SourceKey, "vendor_code", interfaces.ID_ORIGIN_SEMANTIC, true),
				registered(phoneKey.Kind, phoneKey.SourceKey, "p_000000", interfaces.ID_ORIGIN_FALLBACK, false),
			}, suppliers())

			So(items.ObjectTypes[0].ID, ShouldEqual, "vendor")
			So(propertyNames(items.ObjectTypes[0]), ShouldResemble, []string{"vendor_code", "p_000000"})
			So(d.recorded, ShouldBeEmpty)
			// Registered ids it gave again are named too.
			So(d.named, ShouldResemble, map[interfaces.IDKey]bool{otKey: true, codeKey: true, phoneKey: true})
		})

		Convey("An unlocked placeholder gives way to a short name; a locked one does not\n", func() {
			d, items := draftWith([]*interfaces.RegisteredID{
				registered(otKey.Kind, otKey.SourceKey, "ot_000000", interfaces.ID_ORIGIN_FALLBACK, false),
				registered(codeKey.Kind, codeKey.SourceKey, "p_000000", interfaces.ID_ORIGIN_FALLBACK, true),
			}, suppliers())

			So(items.ObjectTypes[0].ID, ShouldEqual, "supplier")
			So(propertyNames(items.ObjectTypes[0])[0], ShouldEqual, "p_000000")
			So(recordedIDs(d)[otKey], ShouldEqual, "semantic:supplier")
			So(recordedIDs(d), ShouldNotContainKey, codeKey)
		})

		Convey("Registered ids take their names before a table that would sort first\n", func() {
			english := table("r2", "c1", "supplier", 0, []string{"id"}, col("id", "string", ""))

			// Without a registry the ASCII table sorts first and takes the plain name.
			d, items := draftWith(nil, suppliers(), english)
			So(objectType(items, "supplier").Table.ResourceID, ShouldEqual, "r2")
			So(recordedIDs(d)[otKey], ShouldEqual, "semantic:supplier_2")

			// Once registered, the table named in Chinese keeps it; the other takes the suffix.
			_, items = draftWith([]*interfaces.RegisteredID{
				registered(otKey.Kind, otKey.SourceKey, "supplier", interfaces.ID_ORIGIN_SEMANTIC, false),
			}, suppliers(), english)
			So(objectType(items, "supplier").Table.ResourceID, ShouldEqual, "r1")
			So(objectType(items, "supplier_2").Table.ResourceID, ShouldEqual, "r2")
		})

		Convey("Value types, link relations and properties take short names, and registered ids hold their names\n", func() {
			tables := sample()
			byID := map[string]*interfaces.VegaTable{}
			for _, t := range tables {
				byID[t.ID] = t
			}
			// The code table and the link table are named in Chinese now.
			byID["r_levels"].SourceName, byID["r_levels"].APIName = "客户等级", "customer_level"
			byID["r_customers"].ForeignKeys[0].RefTable = "客户等级"
			byID["r_order_tags"].SourceName, byID["r_order_tags"].APIName = "订单标签", "order_tag"
			vtKey := interfaces.IDKey{Kind: interfaces.ID_KIND_VALUE_TYPES, SourceKey: "r_levels"}
			rtKey := interfaces.IDKey{Kind: interfaces.ID_KIND_RELATION_TYPES, SourceKey: "r_order_tags"}

			d, items := draftWith(nil, tables...)
			So(items.ValueTypes[0].ID, ShouldEqual, "customer_level")
			So(relation(items, "orders", "tags").ID, ShouldEqual, "order_tag")
			So(recordedIDs(d), ShouldResemble, map[interfaces.IDKey]string{vtKey: "semantic:customer_level", rtKey: "semantic:order_tag"})

			// Items no longer drafted, a table or a field gone, keep their registered ids' names.
			wh := byID["r_wh"]
			wh.Columns[1].Name, wh.Columns[1].APIName = "城市", "city"
			d, items = draftWith([]*interfaces.RegisteredID{
				registered(interfaces.ID_KIND_VALUE_TYPES, "r_gone", "customer_level", interfaces.ID_ORIGIN_SEMANTIC, true),
				registered(interfaces.ID_KIND_RELATION_TYPES, "r_gone", "order_tag", interfaces.ID_ORIGIN_SEMANTIC, true),
				registered(interfaces.ID_KIND_PROPERTIES, "r_wh/旧城市", "city", interfaces.ID_ORIGIN_SEMANTIC, true),
				// Another table's property of the same name holds nothing here.
				registered(interfaces.ID_KIND_PROPERTIES, "r_wh2/城市", "warehouse_id", interfaces.ID_ORIGIN_SEMANTIC, true),
			}, tables...)
			So(items.ValueTypes[0].ID, ShouldEqual, "customer_level_2")
			So(relation(items, "orders", "tags").ID, ShouldEqual, "order_tag_2")
			So(propertyNames(objectType(items, "warehouse")), ShouldResemble, []string{"warehouse_id", "city_2"})
			So(recordedIDs(d)[vtKey], ShouldEqual, "semantic:customer_level_2")
		})

		Convey("Short names never take an id an ASCII name gives, whatever sorts first\n", func() {
			// ODS_客户 sorts before customer ('O' < 'c'), yet customer keeps the id it always had.
			ods := table("r1", "c1", "ODS_客户", 0, []string{"id"}, col("id", "string", ""))
			ods.APIName = "customer"
			customer := table("r2", "c1", "customer", 0, []string{"id"}, col("id", "string", ""))
			d, items := draftWith(nil, ods, customer)
			So(objectType(items, "customer").Table.ResourceID, ShouldEqual, "r2")
			So(recordedIDs(d)[otKey], ShouldEqual, "semantic:customer_2")

			// A field named in Chinese before an ASCII one: the ASCII one keeps its name, and the
			// properties stay in the order of the fields.
			code := col("编号", "string", "")
			code.APIName = "id"
			fields := table("r1", "c1", "供应商", 0, []string{"ID"}, code, col("ID", "string", ""))
			d, items = draftWith(nil, fields)
			So(propertyNames(items.ObjectTypes[0]), ShouldResemble, []string{"id_2", "id"})
			So(recordedIDs(d)[codeKey], ShouldEqual, "semantic:id_2")

			// The same for value types and for relations drafted from link tables, whose short names
			// come after foreign-key relations.
			tables := sample()
			byID := map[string]*interfaces.VegaTable{}
			for _, t := range tables {
				byID[t.ID] = t
			}
			cnLevels := table("r_cn_levels", "c1", "A等级", 3, []string{"code"}, col("code", "string", ""), col("name", "string", ""))
			cnLevels.APIName = "customer_levels"
			byID["r_customers"].ForeignKeys = append(byID["r_customers"].ForeignKeys, fk("level_code", "A等级", "code"))
			byID["r_order_tags"].SourceName, byID["r_order_tags"].APIName = "A订单标签", "orders_to_customers"
			c := sampleCounter()
			c.codes["r_cn_levels"] = c.codes["r_levels"]
			d = newDrafter(testSetting, c).withRegisteredIDs("p1", nil)
			items, err := d.draft(ctx, append(tables, cnLevels), nil)
			So(err, ShouldBeNil)
			valueTypes := map[string]string{}
			for _, vt := range items.ValueTypes {
				valueTypes[vt.CodeTable.ResourceID] = vt.ID
			}
			So(valueTypes, ShouldResemble, map[string]string{"r_levels": "customer_levels", "r_cn_levels": "customer_levels_2"})
			So(relation(items, "orders", "customers").ID, ShouldEqual, "orders_to_customers")
			So(relation(items, "orders", "tags").ID, ShouldEqual, "orders_to_customers_2")
		})

		Convey("Short names that clash take a numbered suffix, registered with it\n", func() {
			other := suppliers()
			other.ID, other.Name, other.SourceName = "r3", "供应商2", "供应商2"
			d, _ := draftWith(nil, suppliers(), other)

			So(recordedIDs(d)[otKey], ShouldEqual, "semantic:supplier")
			So(recordedIDs(d)[interfaces.IDKey{Kind: interfaces.ID_KIND_OBJECT_TYPES, SourceKey: "r3"}], ShouldEqual, "semantic:supplier_2")
		})

		Convey("A short name that is not a valid id is not used\n", func() {
			for _, name := range []string{"Supplier", "supplier-x", "1supplier", "供应商", "a_name_that_is_much_too_long_to_be_an_id_x"} {
				invalid := suppliers()
				invalid.APIName = name
				d, items := draftWith(nil, invalid)
				So(items.ObjectTypes[0].ID, ShouldEqual, idFrom("供应商", idPrefixObjectType))
				So(recordedIDs(d)[otKey], ShouldEqual, "fallback:"+idFrom("供应商", idPrefixObjectType))
			}
		})

		Convey("Tables and fields with ASCII physical names keep their ids and register nothing\n", func() {
			tables := sample()
			for _, t := range tables {
				t.APIName = "renamed"
				for i := range t.Columns {
					t.Columns[i].APIName = "renamed_field"
				}
			}
			plain, err := newDrafter(testSetting, sampleCounter()).draft(ctx, sample(), nil)
			So(err, ShouldBeNil)
			d, items := draftWith(nil, tables...)
			So(d.recorded, ShouldBeEmpty)
			want, _ := json.Marshal(plain)
			got, _ := json.Marshal(items)
			So(string(got), ShouldEqual, string(want))
		})
	})
}
