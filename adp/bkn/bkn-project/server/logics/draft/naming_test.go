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

			accepted := strings.Repeat("长", maxDisplayNameRunes)
			name, aliases = pickName(zh, "cust", accepted)
			So(name, ShouldEqual, accepted)
			So(aliases, ShouldBeEmpty)

			name, aliases = pickName(zh, "cust", "客户", strings.Repeat("a", maxAliasRunes+1))
			So(name, ShouldEqual, "客户")
			So(aliases, ShouldBeEmpty)
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

		Convey("A long description clause is not promoted to an object type name\n", func() {
			name, aliases := nameIn(zh, orders("sales_order", strings.Repeat("说明", maxInferredNameRunes/2)+"长"))
			So(name, ShouldEqual, "sales_order")
			So(aliases, ShouldBeNil)
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
			materials.Description = "物料"
			if nameLanguage(bomName) == interfaces.NAMING_LANGUAGE_EN_US {
				materials.Description = "Material"
			}
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

		Convey("A Chinese project names it by the role the target plays: the referring field without its code word\n", func() {
			So(relationNames(zh, schema("物料BOM明细", "BOM所属物料编码", "父项物料编码", "子项物料编码")),
				ShouldResemble, map[string]string{
					"material_code": "BOM所属物料",
					"parent_code":   "父项物料",
					"child_code":    "子项物料",
				})
		})

		Convey("An English project names it by the role the target plays: the referring field without its code word\n", func() {
			So(relationNames(en, schema("BOM detail", "Material code", "Parent material code", "Child material code")),
				ShouldResemble, map[string]string{
					"material_code": "Material",
					"parent_code":   "Parent material",
					"child_code":    "Child material",
				})
		})

		Convey("A field nobody named keeps the relation's id: joining physical names reads no better\n", func() {
			names := relationNames(zh, schema("物料BOM明细", "", "父项物料编码", ""))
			So(names["material_code"], ShouldEqual, "material_bom_to_material")
			So(names["parent_code"], ShouldEqual, "父项物料")
			So(names["child_code"], ShouldEqual, "material_bom_to_material_3")
		})

		Convey("A field whose physical name is not ASCII reads as written: it names the relation\n", func() {
			materials := table("r_wl", "c1", "物料", 0, []string{"编码"}, col("编码", "string", ""))
			bom := table("r_bom", "c1", "物料清单", 0, []string{"id"}, col("id", "string", ""), col("父项编码", "string", ""))
			bom.ForeignKeys = []interfaces.VegaForeignKey{fk("父项编码", "物料", "编码")}
			names := relationNames(zh, []*interfaces.VegaTable{materials, bom})
			So(names, ShouldHaveLength, 1)
			for _, name := range names {
				So(name, ShouldEqual, "父项")
			}
		})

		Convey("The name is the role whatever the naming language: no template is involved\n", func() {
			names := relationNames("fr-FR", schema("物料BOM明细", "BOM所属物料编码", "父项物料编码", "子项物料编码"))
			So(names["parent_code"], ShouldEqual, "父项物料")
		})

		Convey("The source's name is not in the name, however long it is\n", func() {
			long := strings.Repeat("物", maxDisplayNameRunes)
			tables := schema("", "BOM所属物料编码", "父项物料编码", "子项物料编码")
			tables[0].Name = long
			names := relationNames(zh, tables)
			So(names["parent_code"], ShouldEqual, "父项物料")
			for _, name := range names {
				So(utf8.RuneCountInString(name), ShouldBeLessThanOrEqualTo, maxDisplayNameRunes)
			}
		})

		Convey("Two fields of one source whose roles read alike: the second takes the field's whole name\n", func() {
			names := relationNames(zh, schema("物料BOM明细", "物料编码", "物料编号", "子项物料编码"))
			So(names["material_code"], ShouldEqual, "物料")
			So(names["parent_code"], ShouldEqual, "物料编号")
			So(names["child_code"], ShouldEqual, "子项物料")
		})
	})
}

func Test_RoleOfField(t *testing.T) {
	Convey("Test the role a referring field names\n", t, func() {
		for field, role := range map[string]string{
			"父项物料编码": "父项物料", "物料编号": "物料", "客户ID": "客户", "客户 id": "客户", "产品线代码": "产品线",
			"联系号码": "联系", "运单号": "运单", "下单客户": "下单客户",
			"Customer ID": "Customer", "CustomerID": "Customer", "Customer_id": "Customer", "Supplier No.": "Supplier",
			"Order number": "Order", "PartCode": "Part", "Account key": "Account", "Parent material code": "Parent material",
			// An English code word inside a word is no code word.
			"Barcode": "Barcode", "Monkey": "Monkey", "Paid": "Paid",
			// Nothing that names a role is left.
			"编码": "", "ID": "", "工号": "", "父编码": "", "001号": "",
		} {
			So(roleOfField(field), ShouldEqual, role)
		}
	})
}

// The default names of relations drafted from referring fields: the role, else the target's name.
func Test_DraftRelationRoleName(t *testing.T) {
	Convey("Test the default name of a relation drafted from a referring field\n", t, func() {
		source := &interfaces.DraftObjectType{ID: "s", Name: "物料BOM明细"}
		name := func(field string, target string) string {
			source.Properties = []*interfaces.DraftProperty{{Name: "f", DisplayName: field, MappedField: "f"}}
			return relationNameIn("zh-CN", source, &interfaces.DraftObjectType{ID: "t", Name: target}, "f", "s_to_t")
		}
		So(name("父项物料编码", "物料"), ShouldEqual, "父项物料")
		So(name("子项物料编码", "物料"), ShouldEqual, "子项物料")
		So(name("物料编号", "物料"), ShouldEqual, "物料")
		So(name("供应商编号", "供应商"), ShouldEqual, "供应商")
		So(name("客户ID", "客户"), ShouldEqual, "客户")
		So(name("产品线编码", "生产线"), ShouldEqual, "产品线")
		So(name("下单客户", "客户"), ShouldEqual, "下单客户")
		// Nothing left, or the field reads as the target's name: the target's name.
		So(name("编码", "物料"), ShouldEqual, "物料")
		So(name("工号", "员工"), ShouldEqual, "员工")
		So(name("customer", "Customer"), ShouldEqual, "Customer")
		So(name("产品型号", "产品型号"), ShouldEqual, "产品型号")
		// A field still named by its ASCII physical name keeps the relation's id.
		source.Properties = []*interfaces.DraftProperty{{Name: "customer_id", DisplayName: "customer_id", MappedField: "customer_id"}}
		So(relationNameIn("zh-CN", source, &interfaces.DraftObjectType{ID: "t", Name: "客户"}, "customer_id", "s_to_t"), ShouldEqual, "s_to_t")
	})
}

// Every name an earlier draft gave a relation drafted from a referring field still reads as drafted,
// so renames and translations follow it.
func Test_DraftedRelationNameRecognisesEarlierNames(t *testing.T) {
	Convey("Test recognising the names earlier drafts gave\n", t, func() {
		material := &interfaces.DraftObjectType{ID: "material", Name: "物料"}
		bom := &interfaces.DraftObjectType{ID: "bom", Name: "物料BOM明细",
			Properties: []*interfaces.DraftProperty{{Name: "parent_code", DisplayName: "父项物料编码", MappedField: "parent_code"}}}
		for _, name := range []string{"父项物料", "父项物料编码", "物料BOM明细的父项物料编码", "按父项物料编码关联物料",
			"物料BOM明细按父项物料编码关联物料"} {
			So(draftedRelationName("zh-CN", bom, material, "parent_code", "r", name), ShouldBeTrue)
		}
		So(draftedRelationName("zh-CN", bom, material, "parent_code", "r", "组成"), ShouldBeFalse)

		customer := &interfaces.DraftObjectType{ID: "customer", Name: "Customer"}
		order := &interfaces.DraftObjectType{ID: "order", Name: "Sales order",
			Properties: []*interfaces.DraftProperty{{Name: "customer", DisplayName: "Customer", MappedField: "cust"}}}
		for _, name := range []string{"Customer", "Customer of Sales order", "Links Customer", "Sales order links Customer by Customer"} {
			So(draftedRelationName("en-US", order, customer, "customer", "r", name), ShouldBeTrue)
		}
		zhCustomer := &interfaces.DraftObjectType{ID: "customer", Name: "客户"}
		zhOrder := &interfaces.DraftObjectType{ID: "order", Name: "销售订单",
			Properties: []*interfaces.DraftProperty{{Name: "customer", DisplayName: "客户", MappedField: "cust"}}}
		So(draftedRelationName("zh-CN", zhOrder, zhCustomer, "customer", "r", "关联客户"), ShouldBeTrue)

		// Under the former 40-character limit a long 「{对象}的{字段}」 fell back to the field's name.
		long := &interfaces.DraftObjectType{ID: "bom", Name: strings.Repeat("长", 40),
			Properties: bom.Properties}
		So(draftedRelationName("zh-CN", long, material, "parent_code", "r", "父项物料编码"), ShouldBeTrue)
		So(draftedRelationName("zh-CN", long, material, "parent_code", "r", strings.Repeat("长", 40)+"的父项物料编码"), ShouldBeTrue)
	})
}

func Test_RenameTableNameIn(t *testing.T) {
	Convey("Test a relation named after its end's table name following the end's business name\n", t, func() {
		items := &interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				{ID: "hd_product_view", Name: "hd_product_view", Table: interfaces.DraftTable{Name: "hd_product_view"}},
				{ID: "material_doc", Name: "物料（文档侧）"},
			},
			RelationTypes: []*interfaces.DraftRelationType{
				{ID: "material_doc_link", Name: "物料（文档侧）对应hd_product_view", Source: "material_doc", Target: "hd_product_view"},
				{ID: "renamed", Name: "看hd_product_view", Source: "material_doc", Target: "hd_product_view"},
				{ID: "longer", Name: "对应hd_product_view_v2", Source: "material_doc", Target: "hd_product_view"},
				{ID: "elsewhere", Name: "对应hd_product_view", Source: "material_doc", Target: "material_doc"},
			},
			Decisions: []*interfaces.DraftDecision{
				{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: "renamed", Action: interfaces.DRAFT_DECISION_RENAME},
			},
		}
		decided := []string{}
		decide := func(kind string, id string, action string, _ any, _ any) { decided = append(decided, id+":"+action) }

		renameTableNameIn(items, items.ObjectTypes[0], "产品", decide)

		So(items.RelationTypes[0].Name, ShouldEqual, "物料（文档侧）对应产品")
		So(items.RelationTypes[1].Name, ShouldEqual, "看hd_product_view")
		So(items.RelationTypes[2].Name, ShouldEqual, "对应hd_product_view_v2")
		So(items.RelationTypes[3].Name, ShouldEqual, "对应hd_product_view")
		So(decided, ShouldResemble, []string{"material_doc_link:" + interfaces.DRAFT_DECISION_RENAME})

		Convey("An object type named otherwise than its table changes no relation's name\n", func() {
			items.ObjectTypes[0].Name = "产品"
			decided = nil
			renameTableNameIn(items, items.ObjectTypes[0], "产品视图", decide)
			So(decided, ShouldBeEmpty)
		})
	})
}

// A relation's name differs from the other relations of its source only: two sources may name a
// relation alike, and a clash within one source is resolved by the field's whole name.
func Test_DraftRelationNameTakenWithinTheSource(t *testing.T) {
	Convey("A relation whose role name another relation of its source has takes the field's whole name\n", t, func() {
		material := &interfaces.DraftObjectType{ID: "material", Name: "物料"}
		order := &interfaces.DraftObjectType{ID: "work_order", Name: "生产工单",
			Properties: []*interfaces.DraftProperty{
				{Name: "material_code", DisplayName: "物料编码", MappedField: "material_code"},
				{Name: "material_no", DisplayName: "物料编号", MappedField: "material_no"}}}
		stock := &interfaces.DraftObjectType{ID: "stock", Name: "实时库存",
			Properties: []*interfaces.DraftProperty{{Name: "material_code", DisplayName: "物料编码", MappedField: "material_code"}}}
		first := relationNameIn("zh-CN", order, material, "material_code", "r1")
		So(first, ShouldEqual, "物料")
		taken := []*interfaces.DraftRelationType{{ID: "r1", Source: "work_order", Name: first}}
		// Another source may use the same name.
		So(relationNameTaken(taken, "stock", "r2", "物料"), ShouldBeFalse)
		So(relationNameAmong("zh-CN", taken, stock, material, "material_code", "r2"), ShouldEqual, "物料")
		// The same source may not.
		So(relationNameTaken(taken, "work_order", "r3", "物料"), ShouldBeTrue)
		So(relationNameTaken(taken, "work_order", "r1", first), ShouldBeFalse)
		second := relationNameAmong("zh-CN", taken, order, material, "material_no", "r3")
		So(second, ShouldEqual, "物料编号")
		So(draftedRelationName("zh-CN", order, material, "material_no", "r3", second), ShouldBeTrue)
		// When the field's whole name is taken too, the id keeps the two apart.
		taken = append(taken, &interfaces.DraftRelationType{ID: "r3", Source: "work_order", Name: second})
		So(relationNameAmong("zh-CN", taken, order, material, "material_no", "r4"), ShouldEqual, "r4")
	})
}
