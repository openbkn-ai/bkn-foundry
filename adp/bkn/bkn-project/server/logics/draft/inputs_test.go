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

		Convey("The fingerprint does not depend on the order indices and foreign keys are listed in\n", func() {
			listed := func(swap bool) *interfaces.VegaTable {
				t := customers()
				t.Indices = []interfaces.VegaIndex{{Columns: []string{"id"}, Primary: true}, {Columns: []string{"level"}}}
				t.ForeignKeys = []interfaces.VegaForeignKey{fk("level", "erp.levels", "code"), fk("id", "erp.people", "id")}
				if swap {
					t.Indices[0], t.Indices[1] = t.Indices[1], t.Indices[0]
					t.ForeignKeys[0], t.ForeignKeys[1] = t.ForeignKeys[1], t.ForeignKeys[0]
				}
				return t
			}
			a, b := listed(false), listed(true)

			So(fingerprint(a), ShouldEqual, fingerprint(b))
			// Hashing leaves the table as it was read.
			So(b.Indices[0].Columns, ShouldResemble, []string{"level"})
		})

		Convey("A draft missing tables it could not read is partial\n", func() {
			inputs := draftInputsOf(testSetting, []string{"c1"}, []*interfaces.VegaTable{orders},
				[]*interfaces.DraftTable{{ResourceID: "r_hidden", Reason: unreadableNotVisible}})

			So(inputs.Partial, ShouldBeTrue)
		})
	})
}

func Test_FingerprintShortNames(t *testing.T) {
	Convey("Short names change a table's fingerprint only when it has them\n", t, func() {
		supplier := func() *interfaces.VegaTable {
			return &interfaces.VegaTable{Name: "供应商", SourceName: "供应商", SourceSchema: "erp", PrimaryKeys: []string{"编号"},
				Columns: []interfaces.VegaColumn{{Name: "编号", Type: "string", DisplayName: "供应商编号", Comment: "c"}}}
		}
		// What the drafts before short names recorded, so their tables do not all read as changed.
		So(fingerprint(supplier()), ShouldEqual, "b6adb221e747f3ec43951ca3bb10786f7641981c9b376a95d1ed524c8fffec4d")

		// A short name changes the ids drafted from the table, so it changes the fingerprint.
		named := supplier()
		named.APIName = "supplier"
		So(fingerprint(named), ShouldNotEqual, fingerprint(supplier()))
		field := supplier()
		field.Columns[0].APIName = "supplier_code"
		So(fingerprint(field), ShouldNotEqual, fingerprint(supplier()))
		So(fingerprint(field), ShouldNotEqual, fingerprint(named))
	})
}

func Test_RegistryInputsOf(t *testing.T) {
	Convey("The registry as a draft leaves it is summed up in its inputs\n", t, func() {
		ot := registered(interfaces.ID_KIND_OBJECT_TYPES, "r1", "ot_000000", interfaces.ID_ORIGIN_FALLBACK, false)
		p := registered(interfaces.ID_KIND_PROPERTIES, "r1/编号", "supplier_code", interfaces.ID_ORIGIN_SEMANTIC, true)

		named := map[interfaces.IDKey]bool{ot.IDKey: true, p.IDKey: true}

		// Nothing registered: inputs say nothing, as for catalogs named in ASCII.
		So(registryInputsOf(nil, nil, nil), ShouldBeNil)

		before := registryInputsOf([]*interfaces.RegisteredID{ot, p}, nil, named)
		So(before.Count, ShouldEqual, 2)
		So(before.Placeholders, ShouldEqual, 1)
		So(before.Fingerprint, ShouldHaveLength, 64)
		// The order the registry was read in does not matter.
		So(registryInputsOf([]*interfaces.RegisteredID{p, ot}, nil, named), ShouldResemble, before)

		// What the draft recorded takes the place of what it replaced.
		replaced := registered(interfaces.ID_KIND_OBJECT_TYPES, "r1", "supplier", interfaces.ID_ORIGIN_SEMANTIC, false)
		after := registryInputsOf([]*interfaces.RegisteredID{ot, p}, []*interfaces.RegisteredID{replaced}, named)
		So(after.Count, ShouldEqual, 2)
		So(after.Placeholders, ShouldEqual, 0)
		So(after.Fingerprint, ShouldNotEqual, before.Fingerprint)

		// Locking changes it too: a later draft can tell the ids were confirmed meanwhile.
		lockedOT := *ot
		lockedOT.Locked = true
		So(registryInputsOf([]*interfaces.RegisteredID{&lockedOT, p}, nil, named).Fingerprint, ShouldNotEqual, before.Fingerprint)

		// Placeholders count only what a later draft can still replace: not one a confirmation locked,
		// nor one of an item this draft did not name, such as a table gone from the catalog.
		So(registryInputsOf([]*interfaces.RegisteredID{&lockedOT, p}, nil, named).Placeholders, ShouldEqual, 0)
		gone := registryInputsOf([]*interfaces.RegisteredID{ot, p}, nil, map[interfaces.IDKey]bool{p.IDKey: true})
		So(gone.Placeholders, ShouldEqual, 0)
		So(gone.Count, ShouldEqual, 2)
	})
}
