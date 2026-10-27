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

// The cases below come from drafting the local sample catalogs (retail-gs1, supply-chain):
// tables without keys or foreign keys, role-prefixed references, a small entity table that
// looks like a code table, and a field name that only contains a privacy keyword.
func Test_DraftingFloor(t *testing.T) {
	Convey("Test the rules drafting falls back on\n", t, func() {
		ctx := context.Background()
		draftOf := func(c *fakeCounter, tables ...*interfaces.VegaTable) interfaces.DraftItems {
			items, err := newDrafter(testSetting, c).draft(ctx, tables, nil)
			So(err, ShouldBeNil)
			return items
		}

		Convey("A table without a key takes an identity-like field that counts unique and non-null\n", func() {
			material := table("r_mat", "c1", "erp_material", 0, nil,
				col("material_code", "string", ""), col("material_name", "string", ""))
			c := sampleCounter()
			c.keys["r_mat"] = [3]int64{120, 120, 0}

			o := objectType(draftOf(c, material), "erp_material")

			So(o.PrimaryKeys, ShouldResemble, []string{"material_code"})
			So(o.Key.Source, ShouldEqual, interfaces.DRAFT_KEY_INFERRED)
			So(o.Key.Computed, ShouldBeTrue)
			So(o.NeedsIdentity, ShouldBeFalse)
			So(o.Adopted, ShouldBeTrue)
		})

		Convey("An inferred identity that is not unique leaves the decision to a person\n", func() {
			po := table("r_po", "c1", "erp_purchase_order", 0, nil, col("id", "integer", ""), col("billno", "string", ""))
			c := sampleCounter()
			c.keys["r_po"] = [3]int64{300, 290, 0}

			o := objectType(draftOf(c, po), "erp_purchase_order")

			So(o.NeedsIdentity, ShouldBeTrue)
			So(o.Adopted, ShouldBeFalse)
		})

		Convey("A role-prefixed field refers to the table whose key it ends with\n", func() {
			lots := table("r_lots", "c1", "lots", 0, []string{"lot_id"}, col("lot_id", "string", ""))
			links := table("r_links", "c1", "lot_links", 0, []string{"link_id"},
				col("link_id", "string", ""), col("parent_lot_id", "string", ""), col("child_lot_id", "string", ""))
			c := sampleCounter()
			c.joins[joinKey{"r_links", "parent_lot_id"}] = [4]int64{51, 0, 0, 40}
			c.joins[joinKey{"r_links", "child_lot_id"}] = [4]int64{51, 0, 0, 51}

			items := draftOf(c, lots, links)

			mappings := []string{}
			for _, r := range items.RelationTypes {
				if r.Source == "lot_links" && r.Target == "lots" {
					mappings = append(mappings, r.Mapping[0].SourceProperty)
					So(r.Adopted, ShouldBeTrue)
				}
			}
			So(mappings, ShouldResemble, []string{"parent_lot_id", "child_lot_id"})
		})

		Convey("A field named for a key without its own prefix refers to a key found by inference\n", func() {
			material := table("r_mat", "c1", "erp_material", 0, nil, col("material_code", "string", ""))
			bom := table("r_bom", "c1", "erp_material_bom", 0, nil,
				col("seq_no", "integer", ""), col("bom_material_code", "string", ""))
			c := sampleCounter()
			c.keys["r_mat"] = [3]int64{120, 120, 0}
			c.keys["r_bom"] = [3]int64{500, 500, 0}
			c.joins[joinKey{"r_bom", "bom_material_code"}] = [4]int64{500, 0, 0, 90}

			r := relation(draftOf(c, material, bom), "erp_material_bom", "erp_material")

			So(r, ShouldNotBeNil)
			So(r.Mapping[0], ShouldResemble, interfaces.DraftFieldMapping{SourceProperty: "bom_material_code", TargetProperty: "material_code"})
			So(r.Adopted, ShouldBeTrue)
		})

		Convey("A low-cardinality field matching by chance is proposed, not adopted\n", func() {
			c := sampleCounter()
			c.joins[joinKey{"r_orders", "salesperson_id"}] = [4]int64{800, 0, 0, 3}

			r := relation(draftOf(c, sample()...), "orders", "salesperson")

			So(r.Evidence.HitRate, ShouldEqual, 1)
			So(r.Evidence.SourceDistinct, ShouldEqual, 3)
			So(r.Adopted, ShouldBeFalse)
		})

		Convey("A small entity whose key is no code is not drafted as an enum\n", func() {
			orgs := table("r_orgs", "c1", "organizations", 50, []string{"organization_id"},
				col("organization_id", "string", ""), col("name", "string", ""), col("role", "string", ""), col("role_code", "string", ""))
			c := sampleCounter()

			items := draftOf(c, orgs)

			So(items.ValueTypes, ShouldBeEmpty)
			So(objectType(items, "organizations"), ShouldNotBeNil)
		})

		Convey("A privacy keyword matches whole words of a name, not parts of one\n", func() {
			keywords := testSetting.PrivacyKeywords
			So(privacyKeyword(col("materialplanid_number", "string", ""), keywords), ShouldBeEmpty)
			So(privacyKeyword(col("contact_phone", "string", ""), keywords), ShouldEqual, "phone")
			So(privacyKeyword(col("contactEmail", "string", ""), keywords), ShouldEqual, "email")
			So(privacyKeyword(col("id_number", "string", ""), keywords), ShouldEqual, "id_number")
			So(privacyKeyword(col("c1", "string", "联系人手机"), keywords), ShouldEqual, "手机")
		})

		Convey("Names and descriptions from the catalog's semantic understanding come first\n", func() {
			material := table("r_mat", "c1", "erp_material", 0, []string{"matnr"},
				interfaces.VegaColumn{Name: "matnr", Type: "string", DisplayName: "物料编码", Description: "物料的唯一编码"})

			p := objectType(draftOf(sampleCounter(), material), "erp_material").Properties[0]

			So(p.DisplayName, ShouldEqual, "物料编码")
			So(p.Description, ShouldEqual, "物料的唯一编码")
			So(p.MissingDescription, ShouldBeFalse)
		})
	})
}
