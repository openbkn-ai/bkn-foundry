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

		Convey("A candidate whose count fails keeps no counts of an earlier candidate\n", func() {
			// Candidates in order: id, then the first field billno.
			po := table("r_po", "c1", "erp_purchase_order", 0, nil, col("billno", "string", ""), col("id", "integer", ""))
			c := sampleCounter()
			c.keys["r_po.id"] = [3]int64{300, 290, 0}
			c.failed["r_po.billno"] = true

			o := objectType(draftOf(c, po), "erp_purchase_order")

			So(o.NeedsIdentity, ShouldBeTrue)
			So(o.Key.Field, ShouldEqual, "billno")
			So(o.Key.Computed, ShouldBeFalse)
			So(o.Key.Distinct, ShouldEqual, 0)
			So(o.Key.Reason, ShouldStartWith, reasonQueryFailed)
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

		Convey("A low-cardinality field matching by chance is proposed, not adopted, and says why\n", func() {
			c := sampleCounter()
			c.joins[joinKey{"r_orders", "salesperson_id"}] = [4]int64{800, 0, 0, 3}

			r := relation(draftOf(c, sample()...), "orders", "salesperson")

			So(r.Evidence.HitRate, ShouldEqual, 1)
			So(r.Evidence.SourceDistinct, ShouldEqual, 3)
			So(r.Adopted, ShouldBeFalse)
			So(r.ProposedBecause, ShouldEqual, proposedFewValues)
		})

		Convey("How few values are too few is a setting\n", func() {
			c := sampleCounter()
			c.joins[joinKey{"r_orders", "salesperson_id"}] = [4]int64{800, 0, 0, 3}
			setting := testSetting
			setting.MinInferredDistinct = 3

			items, err := newDrafter(setting, c).draft(ctx, sample(), nil)

			So(err, ShouldBeNil)
			r := relation(items, "orders", "salesperson")
			So(r.Adopted, ShouldBeTrue)
			So(r.ProposedBecause, ShouldBeEmpty)
		})

		Convey("An inferred relation below the adopt rate is proposed, and says why\n", func() {
			c := sampleCounter()
			c.joins[joinKey{"r_orders", "salesperson_id"}] = [4]int64{800, 0, 80, 60}

			r := relation(draftOf(c, sample()...), "orders", "salesperson")

			So(r.Adopted, ShouldBeFalse)
			So(r.ProposedBecause, ShouldEqual, proposedBelowAdoptRate)
		})

		Convey("A relation that could not be counted is proposed, and says why\n", func() {
			c := sampleCounter()
			c.failed["r_orders.salesperson_id"] = true

			r := relation(draftOf(c, sample()...), "orders", "salesperson")

			So(r.Adopted, ShouldBeFalse)
			So(r.ProposedBecause, ShouldEqual, proposedNotCounted)
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

		Convey("A privacy keyword still matches inside one word, with digits, or joined to others\n", func() {
			keywords := testSetting.PrivacyKeywords
			for name, keyword := range map[string]string{
				"birthday": "birth", "cellphone": "phone", "phone2": "phone", "email2": "email", "address1": "address",
			} {
				So(privacyKeyword(col(name, "string", ""), keywords), ShouldEqual, keyword)
			}
			// A multi-word keyword does not start inside a longer word.
			So(privacyKeyword(col("materialplanid_number", "string", ""), keywords), ShouldBeEmpty)
		})

		Convey("The database comment is checked for personal data next to the semantic description\n", func() {
			c := interfaces.VegaColumn{Name: "lxrsj", Type: "string", DisplayName: "Contact", Description: "Contact of the order", Comment: "联系人手机"}

			So(privacyKeyword(c, testSetting.PrivacyKeywords), ShouldEqual, "手机")
		})

		Convey("An exact name match beats a suffix match of an earlier table\n", func() {
			account := table("r_acc", "c1", "account", 0, []string{"account_id"}, col("account_id", "integer", ""))
			sub := table("r_sub", "c1", "sub_account", 0, []string{"sub_account_id"}, col("sub_account_id", "integer", ""))
			tx := table("r_tx", "c1", "transactions", 0, []string{"tx_id"}, col("tx_id", "integer", ""), col("sub_account_id", "integer", ""))
			c := sampleCounter()
			c.joins[joinKey{"r_tx", "sub_account_id"}] = [4]int64{100, 0, 0, 40}

			items := draftOf(c, account, sub, tx)

			So(relation(items, "transactions", "sub_account"), ShouldNotBeNil)
			So(relation(items, "transactions", "account"), ShouldBeNil)
		})

		Convey("A failed inference leaves no key source, with the counts of the field it tried\n", func() {
			po := table("r_po", "c1", "erp_purchase_order", 0, nil, col("id", "integer", ""), col("billno", "string", ""))
			c := sampleCounter()
			c.keys["r_po"] = [3]int64{300, 290, 0}

			o := objectType(draftOf(c, po), "erp_purchase_order")

			So(o.Key.Source, ShouldEqual, interfaces.DRAFT_KEY_NONE)
			So(o.Key.Field, ShouldEqual, "id")
			So(o.Key.Distinct, ShouldEqual, 290)
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
