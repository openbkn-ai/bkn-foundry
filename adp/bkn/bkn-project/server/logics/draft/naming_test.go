// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_PickName(t *testing.T) {
	Convey("Test picking a name in the project's naming language\n", t, func() {
		zh, en := interfaces.NAMING_LANGUAGE_ZH_CN, interfaces.NAMING_LANGUAGE_EN_US

		Convey("A name holding a Han character reads as Chinese, anything else as English\n", func() {
			So(nameLanguage("客户ID"), ShouldEqual, zh)
			So(nameLanguage("Customer"), ShouldEqual, en)
			So(nameLanguage("kehu_bh"), ShouldEqual, en)
		})

		Convey("The first candidate in the naming language is the name; the others of the other language are aliases\n", func() {
			name, aliases := pickName(zh, "cust", "Customer", "客户")
			So(name, ShouldEqual, "客户")
			So(aliases, ShouldResemble, []string{"Customer"})

			name, aliases = pickName(en, "cust", "Customer", "客户")
			So(name, ShouldEqual, "Customer")
			So(aliases, ShouldResemble, []string{"客户"})
		})

		Convey("The physical name names an item only when there is no other name, and is never an alias\n", func() {
			// A name people read in the other language beats an abbreviation such as kh or cust_nm.
			name, aliases := pickName(en, "customer_name", "客户名称")
			So(name, ShouldEqual, "客户名称")
			So(aliases, ShouldBeEmpty)

			name, aliases = pickName(zh, "客户", "Customer")
			So(name, ShouldEqual, "Customer")
			So(aliases, ShouldBeEmpty)

			name, aliases = pickName(en, "customer_name")
			So(name, ShouldEqual, "customer_name")
			So(aliases, ShouldBeEmpty)
		})

		Convey("No candidate in the naming language: the first candidate, as before naming languages\n", func() {
			name, aliases := pickName(zh, "cust", "Customer", "Client")
			So(name, ShouldEqual, "Customer")
			So(aliases, ShouldBeEmpty)
			name, _ = pickName(zh, "cust")
			So(name, ShouldEqual, "cust")
		})

		Convey("Empty, too long and repeated candidates are skipped; aliases BKN would refuse are left out\n", func() {
			long := strings.Repeat("长", maxDisplayNameRunes+1)
			name, aliases := pickName(en, "cust", "", long, "Customer", "客户, 顾客", "客户，顾客", "客户", "客户")
			So(name, ShouldEqual, "Customer")
			So(aliases, ShouldResemble, []string{"客户"})
		})

		Convey("Same-language variants are not aliases: aliases carry the other language only\n", func() {
			_, aliases := pickName(zh, "cust", "客户", "顾客", "Customer")
			So(aliases, ShouldResemble, []string{"Customer"})
		})
	})
}

func Test_DraftNamingLanguage(t *testing.T) {
	Convey("Test drafting names in the project's naming language\n", t, func() {
		zh, en := interfaces.NAMING_LANGUAGE_ZH_CN, interfaces.NAMING_LANGUAGE_EN_US
		// Semantic understanding ran in English; the database comments are Chinese.
		customers := func() *interfaces.VegaTable {
			id := col("kh_id", "string", "Customer id")
			id.Comment = "客户编号"
			name := col("kh_mc", "string", "Name of the customer")
			name.DisplayName, name.Comment = "Customer name", "客户名称"
			// Named Customer id only, which in a Chinese project makes it kh_id's display name too: an
			// alias may not be another property's display name, or BKN refuses the package.
			level := col("kh_dj", "string", "")
			level.DisplayName = "Customer id"
			t := table("r_kh", "c1", "Customer master", 0, []string{"kh_id"}, id, name, level)
			t.SourceName, t.Description = "kh", "客户主数据。来自 CRM"
			return t
		}
		draftIn := func(language string) *interfaces.DraftObjectType {
			d := newDrafter(testSetting, sampleCounter()).withNamingLanguage(language)
			items, err := d.draft(context.Background(), []*interfaces.VegaTable{customers()}, nil)
			So(err, ShouldBeNil)
			return items.ObjectTypes[0]
		}
		names := func(ot *interfaces.DraftObjectType) map[string]string {
			out := map[string]string{}
			for _, p := range ot.Properties {
				out[p.MappedField] = p.DisplayName + " | " + strings.Join(p.Aliases, ",")
			}
			return out
		}

		Convey("A Chinese project takes the Chinese names, the English ones as aliases\n", func() {
			ot := draftIn(zh)
			So(ot.Name, ShouldEqual, "客户主数据")
			So(ot.Aliases, ShouldResemble, []string{"Customer master"})
			So(names(ot), ShouldResemble, map[string]string{
				"kh_id": "客户编号 | ",
				"kh_mc": "客户名称 | Customer name,Name of the customer",
				"kh_dj": "Customer id | ",
			})
		})

		Convey("An English project takes the English names, the Chinese ones as aliases\n", func() {
			ot := draftIn(en)
			So(ot.Name, ShouldEqual, "Customer master")
			So(ot.Aliases, ShouldResemble, []string{"客户主数据"})
			So(names(ot), ShouldResemble, map[string]string{
				"kh_id": "Customer id | 客户编号",
				"kh_mc": "Customer name | 客户名称",
				"kh_dj": "Customer id | ",
			})
		})

		Convey("Without a naming language the draft names in Chinese\n", func() {
			d := newDrafter(testSetting, sampleCounter())
			items, err := d.draft(context.Background(), []*interfaces.VegaTable{customers()}, nil)
			So(err, ShouldBeNil)
			So(items.ObjectTypes[0].Name, ShouldEqual, "客户主数据")
		})
	})
}

func Test_DraftTableName(t *testing.T) {
	Convey("Test naming an object type from its table\n", t, func() {
		zh, en := interfaces.NAMING_LANGUAGE_ZH_CN, interfaces.NAMING_LANGUAGE_EN_US
		orders := func(name string, description string) *interfaces.VegaTable {
			t := table("r_so", "c1", name, 0, []string{"id"}, col("id", "string", ""))
			t.SourceName, t.Description = "sales_order", description
			return t
		}
		nameIn := func(language string, t *interfaces.VegaTable) (string, []string) {
			d := newDrafter(testSetting, sampleCounter()).withNamingLanguage(language)
			items, err := d.draft(context.Background(), []*interfaces.VegaTable{t}, nil)
			So(err, ShouldBeNil)
			return items.ObjectTypes[0].Name, items.ObjectTypes[0].Aliases
		}

		Convey("The name semantic understanding or a person gave the table comes before its description\n", func() {
			// Semantic understanding writes a sentence as the description: its first clause names badly.
			name, aliases := nameIn(zh, orders("销售订单", "记录销售订单的签订、客户、产品、数量、交期及发货状态等信息"))
			So(name, ShouldEqual, "销售订单")
			So(aliases, ShouldBeNil)
		})

		Convey("A table nobody named takes the first clause of its description, as a database comment gives\n", func() {
			name, _ := nameIn(zh, orders("sales_order", "销售订单。来自 ERP"))
			So(name, ShouldEqual, "销售订单")
		})

		Convey("A name discovery gave, the physical name with its schema, is no name a person gave\n", func() {
			// Tables discovered before vega 0.2.1 kept their source identifier as their name.
			t := orders("erp.sales_order", "Sales orders. From ERP")
			t.SourceSchema = "erp"
			name, aliases := nameIn(en, t)
			So(name, ShouldEqual, "Sales orders")
			So(aliases, ShouldBeNil)
		})

		Convey("The naming language still comes first: a name in the other language is an alias\n", func() {
			name, aliases := nameIn(en, orders("销售订单", "Sales orders signed with customers"))
			So(name, ShouldEqual, "Sales orders signed with customers")
			So(aliases, ShouldResemble, []string{"销售订单"})
		})
	})
}

func Test_DraftRelationName(t *testing.T) {
	Convey("Test naming a relation drafted from a field referring to another table\n", t, func() {
		zh, en := interfaces.NAMING_LANGUAGE_ZH_CN, interfaces.NAMING_LANGUAGE_EN_US
		// A bill of materials refers to materials three times; only the referring fields tell the
		// relations apart.
		schema := func(bomName string, materialCode, parentCode, childCode string) []*interfaces.VegaTable {
			materials := table("r_mat", "c1", "material", 0, []string{"code"}, col("code", "string", "物料编码"))
			bom := table("r_bom", "c1", "material_bom", 0, []string{"id"}, col("id", "string", ""),
				col("material_code", "string", materialCode), col("parent_code", "string", parentCode),
				col("child_code", "string", childCode))
			bom.Description = bomName
			bom.ForeignKeys = []interfaces.VegaForeignKey{fk("material_code", "material", "code"),
				fk("parent_code", "material", "code"), fk("child_code", "material", "code")}
			return []*interfaces.VegaTable{materials, bom}
		}
		relationNames := func(language string, tables []*interfaces.VegaTable) map[string]string {
			d := newDrafter(testSetting, sampleCounter()).withNamingLanguage(language)
			items, err := d.draft(context.Background(), tables, nil)
			So(err, ShouldBeNil)
			names := map[string]string{}
			for _, r := range items.RelationTypes {
				names[r.Mapping[0].SourceProperty] = r.Name
			}
			return names
		}

		Convey("A Chinese project names it the source's referring field\n", func() {
			So(relationNames(zh, schema("物料BOM明细", "BOM所属物料编码", "父项物料编码", "子项物料编码")),
				ShouldResemble, map[string]string{
					"material_code": "物料BOM明细的BOM所属物料编码",
					"parent_code":   "物料BOM明细的父项物料编码",
					"child_code":    "物料BOM明细的子项物料编码",
				})
		})

		Convey("An English project names it the referring field of the source\n", func() {
			So(relationNames(en, schema("BOM detail", "Material code", "Parent material code", "Child material code")),
				ShouldResemble, map[string]string{
					"material_code": "Material code of BOM detail",
					"parent_code":   "Parent material code of BOM detail",
					"child_code":    "Child material code of BOM detail",
				})
		})

		Convey("A field nobody named keeps the relation's id: joining physical names reads no better\n", func() {
			names := relationNames(zh, schema("物料BOM明细", "", "父项物料编码", ""))
			So(names["material_code"], ShouldEqual, "material_bom_to_material")
			So(names["parent_code"], ShouldEqual, "物料BOM明细的父项物料编码")
			So(names["child_code"], ShouldEqual, "material_bom_to_material_3")
		})

		Convey("A field whose physical name is not ASCII reads as written: it names the relation\n", func() {
			materials := table("r_wl", "c1", "物料", 0, []string{"编码"}, col("编码", "string", ""))
			bom := table("r_bom", "c1", "物料清单", 0, []string{"id"}, col("id", "string", ""), col("父项编码", "string", ""))
			bom.ForeignKeys = []interfaces.VegaForeignKey{fk("父项编码", "物料", "编码")}
			names := relationNames(zh, []*interfaces.VegaTable{materials, bom})
			So(names, ShouldHaveLength, 1)
			for _, name := range names {
				So(name, ShouldEqual, "物料清单的父项编码")
			}
		})

		Convey("Too long a name keeps the referring field's name only, within BKN's 40-character display names\n", func() {
			// 36 characters: a name BKN takes, too long once the field is joined.
			long := "物料清单明细记录表包含父子项及用量与损耗率等全部信息字段以及其他扩展属性"
			names := relationNames(zh, schema(long, "BOM所属物料编码", "父项物料编码", "子项物料编码"))
			So(names["parent_code"], ShouldEqual, "父项物料编码")
			for _, name := range names {
				So(utf8.RuneCountInString(name), ShouldBeLessThanOrEqualTo, maxDisplayNameRunes)
			}
		})
	})
}
