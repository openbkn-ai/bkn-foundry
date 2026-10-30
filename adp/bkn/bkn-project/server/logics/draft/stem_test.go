// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// recordingCounter is a fakeCounter that records the joins it was asked to count.
type recordingCounter struct {
	*fakeCounter
	counted []string
}

func (r *recordingCounter) joinStats(ctx context.Context, source *interfaces.VegaTable, field string,
	target *interfaces.VegaTable, key string) (int64, int64, int64, int64, error) {
	r.counted = append(r.counted, source.ID+"."+field+">"+target.ID)
	return r.fakeCounter.joinStats(ctx, source, field, target, key)
}

func Test_Stems(t *testing.T) {
	Convey("Test stems of field names\n", t, func() {
		Convey("A field's stem is its name without the identifier word it ends in\n", func() {
			for _, c := range []struct {
				field string
				stem  []string
			}{
				{"supplier_number", []string{"supplier"}},
				{"materialNo", []string{"material"}},
				{"parent_order_id", []string{"parent", "order"}},
				{"supplier_name", nil},
				{"supplier", nil},
				{"number", nil},
				{"status_code", nil},
				{"type_id", nil},
				{"id_no", nil},
				{"remark_no", nil},
			} {
				So(fieldStem(c.field, testSetting), ShouldResemble, c.stem)
			}
		})

		Convey("A business name's stem is the name without the identifier it ends in\n", func() {
			for _, c := range []struct{ name, stem string }{
				{"供应商编号", "供应商"},
				{"物料编码", "物料"},
				{"供应商ID", "供应商"},
				{"Supplier number", "supplier"},
				{"供应商名称", ""},
				{"Casino", ""},
				{"号编号", ""},
				{"状态代码", ""},
				{"备注编号", ""},
				{"", ""},
			} {
				So(businessStem(c.name, testSetting), ShouldEqual, c.stem)
			}
		})

		Convey("The other table's name ending in the stem ranks above its key's stem\n", func() {
			supplier := table("r_s", "c1", "erp_supplier", 0, nil)
			product := table("r_p", "c1", "hd_product_view", 0, nil)
			So(stemMatch("supplier_number", supplier, "supplier_code", testSetting), ShouldEqual, 4)
			So(stemMatch("material_number", product, "material_code", testSetting), ShouldEqual, 3)
			So(stemMatch("supplier_name", supplier, "supplier_code", testSetting), ShouldEqual, 0)
			So(stemMatch("customer_number", supplier, "supplier_code", testSetting), ShouldEqual, 0)
		})
	})
}

func Test_StemRelations(t *testing.T) {
	Convey("Test drafting relations from stems, gated by the counts\n", t, func() {
		ctx := context.Background()
		// Purchase orders name the supplier and the material by number; neither table has a
		// foreign key or a key of that name. A product view also keys by material_code.
		tables := func(supplierType string) []*interfaces.VegaTable {
			po := table("r_po", "c1", "erp_purchase_order", 0, []string{"id"},
				col("id", "string", ""),
				col("supplier_number", supplierType, ""),
				col("supplier_name", "string", ""),
				col("material_number", "string", ""))
			supplier := table("r_supplier", "c1", "erp_supplier", 0, []string{"supplier_code"},
				col("supplier_code", "string", ""), col("supplier_name", "string", ""))
			material := table("r_material", "c1", "erp_material", 0, []string{"material_code"},
				col("material_code", "string", ""), col("material_name", "string", ""))
			// Its id sorts first, so only the rank puts erp_material before it.
			product := table("r_a_product", "c1", "hd_product_view", 0, []string{"material_code"},
				col("material_code", "string", ""), col("spec", "string", ""))
			return []*interfaces.VegaTable{po, supplier, material, product}
		}
		counts := func(supplier, material [4]int64) *recordingCounter {
			c := sampleCounter()
			c.joins = map[joinKey][4]int64{{"r_po", "supplier_number"}: supplier, {"r_po", "material_number"}: material}
			return &recordingCounter{fakeCounter: c}
		}
		run := func(c *recordingCounter, tables []*interfaces.VegaTable) interfaces.DraftItems {
			items, err := newDrafter(testSetting, c).draft(ctx, tables, nil)
			So(err, ShouldBeNil)
			return items
		}

		Convey("A stem the counts bear out is adopted with its evidence\n", func() {
			items := run(counts([4]int64{200, 0, 0, 120}, [4]int64{200, 10, 0, 90}), tables("string"))

			r := relation(items, "erp_purchase_order", "erp_supplier")
			So(r, ShouldNotBeNil)
			So(r.Origin, ShouldEqual, interfaces.DRAFT_ORIGIN_STEM_MATCH)
			So(r.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "supplier_number", TargetProperty: "supplier_code"}})
			So(r.Adopted, ShouldBeTrue)
			So(r.Evidence, ShouldResemble, interfaces.DraftJoinEvidence{
				Computed: true, Rows: 200, Hits: 200, HitRate: 1, SourceDistinct: 120,
			})
		})

		Convey("Of two tables a stem points at, the one named by it wins\n", func() {
			c := counts([4]int64{200, 0, 0, 120}, [4]int64{200, 10, 0, 90})
			items := run(c, tables("string"))

			So(relation(items, "erp_purchase_order", "erp_material"), ShouldNotBeNil)
			So(relation(items, "erp_purchase_order", "hd_product_view"), ShouldBeNil)
			So(c.counted, ShouldNotContain, "r_po.material_number>r_a_product")
		})

		Convey("A stem below the suggest rate is not drafted; between the rates it is proposed\n", func() {
			c := counts([4]int64{200, 0, 100, 120}, [4]int64{200, 0, 30, 90})
			items := run(c, tables("string"))

			So(relation(items, "erp_purchase_order", "erp_supplier"), ShouldBeNil)
			material := relation(items, "erp_purchase_order", "erp_material")
			So(material.Adopted, ShouldBeFalse)
			So(material.ProposedBecause, ShouldEqual, proposedBelowAdoptRate)
			// Drafted to the first table, the stem is not counted against the next.
			So(c.counted, ShouldNotContain, "r_po.material_number>r_a_product")
		})

		Convey("A field of another type than the key, or a name field, is never counted\n", func() {
			c := counts([4]int64{200, 0, 0, 120}, [4]int64{200, 0, 0, 90})
			items := run(c, tables("integer"))

			So(relation(items, "erp_purchase_order", "erp_supplier"), ShouldBeNil)
			for _, counted := range c.counted {
				So(counted, ShouldNotStartWith, "r_po.supplier_")
			}
		})

		Convey("A stem is counted against a few tables at most\n", func() {
			all := tables("string")
			for _, id := range []string{"r_x1", "r_x2", "r_x3"} {
				all = append(all, table(id, "c1", id+"_supplier", 0, []string{"supplier_code"}, col("supplier_code", "string", "")))
			}
			c := counts([4]int64{200, 0, 200, 120}, [4]int64{})
			run(c, all)

			n := 0
			for _, counted := range c.counted {
				if strings.HasPrefix(counted, "r_po.supplier_number>") {
					n++
				}
			}
			So(n, ShouldEqual, maxStemCandidates)
		})

		Convey("A stem never makes an enum, which would take a code table uncounted\n", func() {
			members := table("r_members", "c1", "members", 0, []string{"member_id"},
				col("member_id", "string", ""), col("member_level_no", "string", ""))
			levels := table("r_member_level", "c1", "member_level", 3, []string{"code"},
				col("code", "string", ""), col("name", "string", ""))
			c := sampleCounter()
			c.codes = map[string][]interfaces.DraftEnumValue{"r_member_level": {{Value: "A", Display: "Gold"}}}
			items := run(&recordingCounter{fakeCounter: c}, []*interfaces.VegaTable{members, levels})

			So(len(items.ValueTypes), ShouldEqual, 1)
			So(findProperty(objectType(items, "members"), "member_level_no").ValueTypeID, ShouldEqual, "")
		})

		Convey("A stem is only drafted counted: not to another catalog, nor when its count fails\n", func() {
			all := tables("string")
			// In another catalog, of ids that sort before erp_supplier's: enough to take every place.
			for _, id := range []string{"r_a_srm", "r_b_srm", "r_c_srm"} {
				all = append(all, table(id, "c2", "srm_supplier", 0, []string{"supplier_code"}, col("supplier_code", "string", "")))
			}
			c := counts([4]int64{200, 0, 0, 120}, [4]int64{})
			c.failed["r_po.material_number"] = true
			items := run(c, all)

			So(relation(items, "erp_purchase_order", "srm_supplier"), ShouldBeNil)
			So(relation(items, "erp_purchase_order", "erp_supplier").Adopted, ShouldBeTrue)
			So(c.counted, ShouldNotContain, "r_po.supplier_number>r_a_srm")
			// Both tables the material stem points at were tried; neither was drafted uncounted.
			So(relation(items, "erp_purchase_order", "erp_material"), ShouldBeNil)
			So(relation(items, "erp_purchase_order", "hd_product_view"), ShouldBeNil)
			So(c.counted, ShouldContain, "r_po.material_number>r_a_product")
		})

		Convey("A business name in words names a table by its whole name or its last words\n", func() {
			po := table("r_cg", "c1", "cg", 0, []string{"dd"}, col("dd", "string", ""), col("yh", "string", "User ID"))
			role := table("r_a_role", "c1", "js", 0, []string{"bm"}, col("bm", "string", ""))
			role.Description = "User role"
			superuser := table("r_b_su", "c1", "cjyh", 0, []string{"bm"}, col("bm", "string", ""))
			superuser.Description = "Superuser"
			user := table("r_c_user", "c1", "yh_b", 0, []string{"bm"}, col("bm", "string", ""))
			user.Description = "System user"
			exact := table("r_d_user", "c1", "yh_a", 0, []string{"bm"}, col("bm", "string", ""))
			exact.Description = "User"
			c := sampleCounter()
			c.joins = map[joinKey][4]int64{{"r_cg", "yh"}: {100, 0, 0, 40}}
			rc := &recordingCounter{fakeCounter: c}
			items := run(rc, []*interfaces.VegaTable{po, role, superuser, user, exact})

			// The table named User exactly comes first, though its id sorts last.
			So(rc.counted, ShouldResemble, []string{"r_cg.yh>r_d_user"})
			So(relation(items, "cg", "yh_a"), ShouldNotBeNil)

			// Refuted, the stem goes on to the name it ends, never to User role or Superuser.
			c.joins = map[joinKey][4]int64{{"r_cg", "yh"}: {100, 0, 100, 40}}
			rc.counted = nil
			run(rc, []*interfaces.VegaTable{po, role, superuser, user, exact})
			So(rc.counted, ShouldResemble, []string{"r_cg.yh>r_d_user", "r_cg.yh>r_c_user"})
		})

		Convey("A physical name standing in for a business name never matches by part of a word\n", func() {
			// Without business names, user_id and po_no are their own display names.
			orders := table("r_orders", "c1", "orders", 0, []string{"order_id"},
				col("order_id", "string", ""), col("user_id", "string", ""), col("po_no", "string", ""))
			superuser := table("r_superuser_log", "c1", "superuser_log", 0, []string{"code"}, col("code", "string", ""))
			userRole := table("r_user_role", "c1", "user_role", 0, []string{"code"}, col("code", "string", ""))
			report := table("r_report", "c1", "report", 0, []string{"code"}, col("code", "string", ""))
			deposit := table("r_deposit", "c1", "deposit", 0, []string{"code"}, col("code", "string", ""))
			c := sampleCounter()
			c.joins = map[joinKey][4]int64{{"r_orders", "user_id"}: {100, 0, 0, 40}, {"r_orders", "po_no"}: {100, 0, 0, 40}}
			rc := &recordingCounter{fakeCounter: c}
			items := run(rc, []*interfaces.VegaTable{orders, superuser, userRole, report, deposit})

			So(rc.counted, ShouldBeEmpty)
			So(items.RelationTypes, ShouldBeEmpty)
		})

		Convey("A field whose business name names the other table is a candidate\n", func() {
			po := table("r_cg", "c1", "cgdd", 0, []string{"dd_id"}, col("dd_id", "string", ""), col("gys_bh", "string", "供应商编号"))
			supplier := table("r_gys", "c1", "gys", 0, []string{"bm"}, col("bm", "string", ""), col("mc", "string", ""))
			supplier.Description = "供应商"
			c := sampleCounter()
			c.joins = map[joinKey][4]int64{{"r_cg", "gys_bh"}: {100, 0, 0, 40}}
			items := run(&recordingCounter{fakeCounter: c}, []*interfaces.VegaTable{po, supplier})

			r := relation(items, "cgdd", "gys")
			So(r, ShouldNotBeNil)
			So(r.Adopted, ShouldBeTrue)
			So(r.Evidence.HitRate, ShouldEqual, 1)
		})
	})
}
