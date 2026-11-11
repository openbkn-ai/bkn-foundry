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

// suppliers is a table named in Chinese.
func suppliers() *interfaces.VegaTable {
	return table("r1", "c1", "供应商", 0, []string{"编号"}, col("编号", "string", ""), col("电话", "string", ""))
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

		Convey("Placeholders give the ids and are registered\n", func() {
			d, items := draftWith(nil, suppliers())

			ot, code, phone := idFrom("供应商", idPrefixObjectType), idFrom("编号", idPrefixProperty), idFrom("电话", idPrefixProperty)
			So(items.ObjectTypes[0].ID, ShouldEqual, ot)
			So(propertyNames(items.ObjectTypes[0]), ShouldResemble, []string{code, phone})
			So(recordedIDs(d), ShouldResemble, map[interfaces.IDKey]string{
				otKey: "fallback:" + ot, codeKey: "fallback:" + code, phoneKey: "fallback:" + phone,
			})
			for _, id := range d.recorded {
				So(id.ProjectID, ShouldEqual, "p1")
				So(id.Locked, ShouldBeFalse)
			}
			So(string(d.recorded[0].Basis), ShouldEqual, `{"physical_name":"供应商"}`)
			So(d.named, ShouldResemble, map[interfaces.IDKey]bool{otKey: true, codeKey: true, phoneKey: true})
		})

		Convey("Registered ids are kept, short names registered earlier and unlocked placeholders too, and not recorded again\n", func() {
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

		Convey("Registered ids take their names before a table that would sort first\n", func() {
			english := table("r2", "c1", "supplier", 0, []string{"id"}, col("id", "string", ""))
			_, items := draftWith([]*interfaces.RegisteredID{
				registered(otKey.Kind, otKey.SourceKey, "supplier", interfaces.ID_ORIGIN_SEMANTIC, false),
			}, suppliers(), english)
			So(objectType(items, "supplier").Table.ResourceID, ShouldEqual, "r1")
			So(objectType(items, "supplier_2").Table.ResourceID, ShouldEqual, "r2")
		})

		Convey("Value types, link relations and properties take placeholders, and registered ids hold their names\n", func() {
			tables := sample()
			byID := map[string]*interfaces.VegaTable{}
			for _, t := range tables {
				byID[t.ID] = t
			}
			// The code table and the link table are named in Chinese now.
			byID["r_levels"].SourceName = "客户等级"
			byID["r_customers"].ForeignKeys[0].RefTable = "客户等级"
			byID["r_order_tags"].SourceName = "订单标签"
			vtKey := interfaces.IDKey{Kind: interfaces.ID_KIND_VALUE_TYPES, SourceKey: "r_levels"}
			rtKey := interfaces.IDKey{Kind: interfaces.ID_KIND_RELATION_TYPES, SourceKey: "r_order_tags"}
			vt, rt := idFrom("客户等级", idPrefixValueType), idFrom("订单标签", idPrefixRelationType)

			d, items := draftWith(nil, tables...)
			So(items.ValueTypes[0].ID, ShouldEqual, vt)
			So(relation(items, "orders", "tags").ID, ShouldEqual, rt)
			So(recordedIDs(d), ShouldResemble, map[interfaces.IDKey]string{vtKey: "fallback:" + vt, rtKey: "fallback:" + rt})

			// Items no longer drafted, a table or a field gone, keep their registered ids' names.
			wh := byID["r_wh"]
			wh.Columns[1].Name = "城市"
			city := idFrom("城市", idPrefixProperty)
			d, items = draftWith([]*interfaces.RegisteredID{
				registered(interfaces.ID_KIND_VALUE_TYPES, "r_gone", vt, interfaces.ID_ORIGIN_FALLBACK, true),
				registered(interfaces.ID_KIND_RELATION_TYPES, "r_gone", rt, interfaces.ID_ORIGIN_FALLBACK, true),
				registered(interfaces.ID_KIND_PROPERTIES, "r_wh/旧城市", city, interfaces.ID_ORIGIN_FALLBACK, true),
				// Another table's property of the same name holds nothing here.
				registered(interfaces.ID_KIND_PROPERTIES, "r_wh2/城市", "warehouse_id", interfaces.ID_ORIGIN_FALLBACK, true),
			}, tables...)
			So(items.ValueTypes[0].ID, ShouldEqual, vt+"_2")
			So(relation(items, "orders", "tags").ID, ShouldEqual, rt+"_2")
			So(propertyNames(objectType(items, "warehouse")), ShouldResemble, []string{"warehouse_id", city + "_2"})
			So(recordedIDs(d)[vtKey], ShouldEqual, "fallback:"+vt+"_2")
		})

		Convey("A field named in Chinese before an ASCII one: the properties stay in the order of the fields\n", func() {
			fields := table("r1", "c1", "供应商", 0, []string{"ID"}, col("编号", "string", ""), col("ID", "string", ""))
			_, items := draftWith(nil, fields)
			So(propertyNames(items.ObjectTypes[0]), ShouldResemble, []string{idFrom("编号", idPrefixProperty), "id"})
		})

		Convey("Placeholders that clash take a numbered suffix, registered with it\n", func() {
			other := suppliers()
			other.ID = "r3"
			d, _ := draftWith(nil, suppliers(), other)

			ot := idFrom("供应商", idPrefixObjectType)
			So(recordedIDs(d)[otKey], ShouldEqual, "fallback:"+ot)
			So(recordedIDs(d)[interfaces.IDKey{Kind: interfaces.ID_KIND_OBJECT_TYPES, SourceKey: "r3"}], ShouldEqual, "fallback:"+ot+"_2")
		})

		Convey("Tables and fields with ASCII physical names keep their ids and register nothing\n", func() {
			plain, err := newDrafter(testSetting, sampleCounter()).draft(ctx, sample(), nil)
			So(err, ShouldBeNil)
			d, items := draftWith(nil, sample()...)
			So(d.recorded, ShouldBeEmpty)
			want, _ := json.Marshal(plain)
			got, _ := json.Marshal(items)
			So(string(got), ShouldEqual, string(want))
		})
	})
}
