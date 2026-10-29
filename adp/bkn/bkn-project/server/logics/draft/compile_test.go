// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"sort"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A branch's network.bkn, as an export in BKN 3.0 writes it.
const branchNetwork = `---
type: knowledge_network
id: supply
name: Supply chain
bkn_spec: "3.0"
---

# Supply chain
`

func compileFixture() *interfaces.DraftItems {
	table := func(id string) interfaces.DraftTable {
		return interfaces.DraftTable{ResourceID: "res_" + id, Name: id, CatalogID: "cat"}
	}
	return &interfaces.DraftItems{
		ObjectTypes: []*interfaces.DraftObjectType{
			{
				ID: "orders", Name: "Orders", Description: "Sales orders.", Adopted: true, Table: table("orders"),
				PrimaryKeys: []string{"order_id"}, DisplayKey: "order_id",
				Properties: []*interfaces.DraftProperty{
					{Name: "order_id", DisplayName: "Order", Type: "string", MappedField: "order_id"},
					{Name: "customer_id", DisplayName: "Customer", Type: "string", MappedField: "customer_id"},
					{Name: "status", DisplayName: "Status", Type: "string", MappedField: "status", ValueTypeID: "order_status"},
					{Name: "channel", DisplayName: "Channel", Type: "string", MappedField: "channel", ValueTypeID: "channel"},
				},
			},
			{
				ID: "customers", Name: "Customers", Adopted: true, Table: table("customers"),
				PrimaryKeys: []string{"customer_id"}, DisplayKey: "customer_id",
				Properties: []*interfaces.DraftProperty{
					{Name: "customer_id", DisplayName: "Customer", Type: "string", MappedField: "customer_id"},
				},
			},
			{
				ID: "products", Name: "Products", Adopted: true, Table: table("products"),
				PrimaryKeys: []string{"product_id"}, DisplayKey: "product_id",
				Properties: []*interfaces.DraftProperty{
					{Name: "product_id", DisplayName: "Product", Type: "string", MappedField: "product_id"},
				},
			},
			{
				ID: "audit_log", Name: "Audit log", Adopted: false, Table: table("audit_log"),
				PrimaryKeys: []string{"log_id"}, DisplayKey: "log_id",
				Properties: []*interfaces.DraftProperty{
					{Name: "log_id", DisplayName: "Log", Type: "string", MappedField: "log_id"},
				},
			},
		},
		RelationTypes: []*interfaces.DraftRelationType{
			{
				ID: "orders_to_customers", Name: "Ordered by", Source: "orders", Target: "customers",
				Cardinality: "many_to_one", Adopted: true, Verification: "confirmed",
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "customer_id", TargetProperty: "customer_id"}},
			},
			{
				ID: "orders_to_products", Name: "Contains", Source: "orders", Target: "products",
				Cardinality: "many_to_many", Adopted: true, Verification: "confirmed",
				LinkTable:     &interfaces.DraftTable{ResourceID: "res_order_lines", Name: "order_lines"},
				SourceMapping: []interfaces.DraftFieldMapping{{SourceProperty: "order_id", TargetProperty: "order_id"}},
				TargetMapping: []interfaces.DraftFieldMapping{{SourceProperty: "product_id", TargetProperty: "product_id"}},
			},
			{
				// Adopted before #2577 while an end is not: a pending item, left out.
				ID: "audit_log_to_orders", Name: "Logs", Source: "audit_log", Target: "orders",
				Cardinality: "many_to_one", Adopted: true, Verification: "unconfirmed",
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "order_id", TargetProperty: "order_id"}},
			},
			{
				ID: "customers_to_products", Name: "Favours", Source: "customers", Target: "products",
				Cardinality: "many_to_one", Adopted: false, Verification: "unconfirmed",
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "customer_id", TargetProperty: "product_id"}},
			},
		},
		ValueTypes: []*interfaces.DraftValueType{
			{
				ID: "order_status", Name: "Order status", BaseType: "string", Adopted: true,
				Values: []interfaces.DraftEnumValue{{Value: "N", Display: "New"}, {Value: "S", Display: "Shipped"}},
			},
			{
				ID: "channel", Name: "Channel", BaseType: "string", Adopted: false,
				Values: []interfaces.DraftEnumValue{{Value: "web", Display: "Web"}},
			},
		},
	}
}

func loadWithBranch(files map[string][]byte) (*bkn.BknNetwork, error) {
	fsys := bkn.NewMemoryFileSystem()
	fsys.AddFiles(files)
	fsys.AddFile("network.bkn", []byte(branchNetwork))
	return bkn.LoadNetworkWithFS(fsys, ".")
}

func Test_CompileDraft(t *testing.T) {
	Convey("Test compileDraft\n", t, func() {
		Convey("Only adopted items are written, as definition files of a 3.0 package\n", func() {
			compiled, err := compileDraft(compileFixture())
			files := compiled.Files

			So(err, ShouldBeNil)
			paths := make([]string, 0, len(files))
			for p := range files {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			So(paths, ShouldResemble, []string{
				"object_types/customers.bkn",
				"object_types/orders.bkn",
				"object_types/products.bkn",
				"relation_types/orders_to_customers.bkn",
				"relation_types/orders_to_products.bkn",
				"value_types/order_status.bkn",
			})
		})

		Convey("The files read back under a branch's network.bkn as the draft's definitions\n", func() {
			compiled, err := compileDraft(compileFixture())
			files := compiled.Files
			So(err, ShouldBeNil)

			network, err := loadWithBranch(files)

			So(err, ShouldBeNil)
			So(network.BknSpec, ShouldEqual, bkn.BknSpecV3)
			byID := map[string]*bkn.BknObjectType{}
			for _, ot := range network.ObjectTypes {
				byID[ot.ID] = ot
			}
			orders := byID["orders"]
			So(orders, ShouldNotBeNil)
			So(orders.Name, ShouldEqual, "Orders")
			So(orders.Description, ShouldEqual, "Sales orders.")
			So(orders.DataSource.Type, ShouldEqual, bkn.DATA_SOURCE_TYPE_RESOURCE)
			So(orders.DataSource.ID, ShouldEqual, "res_orders")
			So(orders.PrimaryKeys, ShouldResemble, []string{"order_id"})
			So(orders.DisplayKey, ShouldEqual, "order_id")
			properties := map[string]*bkn.DataProperty{}
			for _, p := range orders.DataProperties {
				properties[p.Name] = p
			}
			So(properties["status"].MappedField, ShouldEqual, "status")
			So(properties["status"].Constraints.ValueType, ShouldEqual, "order_status")
			// Its value type is not adopted, so the property keeps its base type alone.
			So(properties["channel"].Constraints, ShouldBeNil)
			So(properties["channel"].Type, ShouldEqual, "string")

			So(network.ValueTypes, ShouldHaveLength, 1)
			So(network.ValueTypes[0].ID, ShouldEqual, "order_status")
			So(network.ValueTypes[0].Enum, ShouldHaveLength, 2)

			relations := map[string]*bkn.BknRelationType{}
			for _, rt := range network.RelationTypes {
				relations[rt.ID] = rt
			}
			So(relations, ShouldHaveLength, 2)
			direct := relations["orders_to_customers"]
			So(direct.Endpoint.Type, ShouldEqual, "direct")
			So(direct.Endpoint.Cardinality, ShouldEqual, "many_to_one")
			So(direct.Verification, ShouldEqual, "confirmed")
			So(direct.MappingRules, ShouldResemble, bkn.DirectMappingRule{{SourceProperty: "customer_id", TargetProperty: "customer_id"}})
			linked := relations["orders_to_products"]
			So(linked.Endpoint.Type, ShouldEqual, "indirect")
			rules, ok := linked.MappingRules.(*bkn.InDirectMappingRule)
			So(ok, ShouldBeTrue)
			So(rules.BackingDataSource.ID, ShouldEqual, "res_order_lines")
			So(rules.SourceMappingRules, ShouldResemble, []bkn.MappingRule{{SourceProperty: "order_id", TargetProperty: "order_id"}})
			So(rules.TargetMappingRules, ShouldResemble, []bkn.MappingRule{{SourceProperty: "product_id", TargetProperty: "product_id"}})
		})

		Convey("A draft as the drafter writes it reads back with everything adopted in it\n", func() {
			items, err := newDrafter(testSetting, sampleCounter()).draft(context.Background(), sample(), nil)
			So(err, ShouldBeNil)
			// What waits for a person is settled first, as confirming requires.
			for _, ot := range items.ObjectTypes {
				if ot.NeedsIdentity {
					ot.Adopted = false
				}
			}

			compiled, err := compileDraft(&items)
			So(err, ShouldBeNil)
			files := compiled.Files
			network, err := loadWithBranch(files)

			So(err, ShouldBeNil)
			adopted := 0
			for _, ot := range items.ObjectTypes {
				if ot.Adopted {
					adopted++
				}
			}
			So(adopted, ShouldBeGreaterThan, 0)
			So(network.ObjectTypes, ShouldHaveLength, adopted)
			So(network.RelationTypes, ShouldNotBeEmpty)
		})

		Convey("A value type BKN 3.0 refuses is left out, and its properties keep their base type\n", func() {
			items := compileFixture()
			items.ObjectTypes[0].Properties = append(items.ObjectTypes[0].Properties,
				&interfaces.DraftProperty{Name: "region", DisplayName: "Region", Type: "text", MappedField: "region", ValueTypeID: "region"},
				&interfaces.DraftProperty{Name: "priority", DisplayName: "Priority", Type: "string", MappedField: "priority", ValueTypeID: "priority"},
				&interfaces.DraftProperty{Name: "grade", DisplayName: "Grade", Type: "string", MappedField: "grade", ValueTypeID: "grade"})
			items.ValueTypes = append(items.ValueTypes,
				// A PostgreSQL text code table: text is no base type of a value type.
				&interfaces.DraftValueType{ID: "region", Name: "Region", BaseType: "text", Adopted: true,
					Values: []interfaces.DraftEnumValue{{Value: "N", Display: "North"}}},
				// A code without a name: its value stands for it.
				&interfaces.DraftValueType{ID: "priority", Name: "Priority", BaseType: "string", Adopted: true,
					Values: []interfaces.DraftEnumValue{{Value: "P1", Display: ""}}},
				// Named as an adopted one before it: names are unique in a network.
				&interfaces.DraftValueType{ID: "grade", Name: "Order status", BaseType: "string", Adopted: true,
					Values: []interfaces.DraftEnumValue{{Value: "A", Display: "Top"}}})

			compiled, err := compileDraft(items)

			So(err, ShouldBeNil)
			So(compiled.SkippedValueTypes, ShouldResemble, []string{"region", "grade"})
			network, err := loadWithBranch(compiled.Files)
			So(err, ShouldBeNil)
			ids := []string{}
			for _, vt := range network.ValueTypes {
				ids = append(ids, vt.ID)
			}
			sort.Strings(ids)
			So(ids, ShouldResemble, []string{"order_status", "priority"})
			properties := map[string]*bkn.DataProperty{}
			for _, ot := range network.ObjectTypes {
				if ot.ID == "orders" {
					for _, p := range ot.DataProperties {
						properties[p.Name] = p
					}
				}
			}
			So(properties["region"].Constraints, ShouldBeNil)
			So(properties["region"].Type, ShouldEqual, "text")
			So(properties["grade"].Constraints, ShouldBeNil)
			So(properties["priority"].Constraints.ValueType, ShouldEqual, "priority")
		})

		Convey("Nothing adopted writes no definitions\n", func() {
			compiled, err := compileDraft(&interfaces.DraftItems{})

			So(err, ShouldBeNil)
			So(compiled.Files, ShouldBeEmpty)
			// An empty list, not a missing one: the confirmation reads as skipping none.
			So(compiled.SkippedValueTypes, ShouldNotBeNil)
			So(compiled.SkippedValueTypes, ShouldBeEmpty)
		})

		Convey("An adopted object type without a table is refused\n", func() {
			items := compileFixture()
			items.ObjectTypes[0].Table.ResourceID = ""

			_, err := compileDraft(items)

			So(err, ShouldNotBeNil)
		})
	})
}

func Test_CompileDraftIDKeys(t *testing.T) {
	Convey("compileDraft names the registry keys of what it writes\n", t, func() {
		items := compileFixture()
		items.ValueTypes[0].CodeTable = interfaces.DraftTable{ResourceID: "res_order_status"}
		// Not written: channel is not adopted, audit_log is not, nor are relations to it.
		compiled, err := compileDraft(items)

		So(err, ShouldBeNil)
		key := func(kind, sourceKey string) interfaces.IDKey {
			return interfaces.IDKey{Kind: kind, SourceKey: sourceKey}
		}
		So(compiled.IDKeys, ShouldResemble, []interfaces.IDKey{
			key(interfaces.ID_KIND_VALUE_TYPES, "res_order_status"),
			key(interfaces.ID_KIND_OBJECT_TYPES, "res_orders"),
			key(interfaces.ID_KIND_PROPERTIES, "res_orders/order_id"),
			key(interfaces.ID_KIND_PROPERTIES, "res_orders/customer_id"),
			key(interfaces.ID_KIND_PROPERTIES, "res_orders/status"),
			key(interfaces.ID_KIND_PROPERTIES, "res_orders/channel"),
			key(interfaces.ID_KIND_OBJECT_TYPES, "res_customers"),
			key(interfaces.ID_KIND_PROPERTIES, "res_customers/customer_id"),
			key(interfaces.ID_KIND_OBJECT_TYPES, "res_products"),
			key(interfaces.ID_KIND_PROPERTIES, "res_products/product_id"),
			// Only a relation drafted from a link table has an id from a table name.
			key(interfaces.ID_KIND_RELATION_TYPES, "res_order_lines"),
		})
	})

	Convey("A value type BKN refuses is not written, so its id is not locked\n", t, func() {
		items := compileFixture()
		items.ValueTypes[0].CodeTable = interfaces.DraftTable{ResourceID: "res_order_status"}
		items.ValueTypes[0].BaseType = "geometry"
		compiled, err := compileDraft(items)

		So(err, ShouldBeNil)
		So(compiled.SkippedValueTypes, ShouldResemble, []string{"order_status"})
		So(compiled.IDKeys, ShouldNotContain, interfaces.IDKey{Kind: interfaces.ID_KIND_VALUE_TYPES, SourceKey: "res_order_status"})
	})
}

func Test_CompileAliases(t *testing.T) {
	Convey("Aliases are written into the definitions, as BKN 3.0 reads them\n", t, func() {
		items := compileFixture()
		items.ObjectTypes[0].Aliases = []string{"订单"}
		items.ObjectTypes[0].Properties[0].Aliases = []string{"订单号"}
		items.RelationTypes[1].Aliases = []string{"包含商品"}

		compiledOT := compileObjectType(items.ObjectTypes[0], map[string]bool{})
		So(compiledOT.Aliases, ShouldResemble, []string{"订单"})
		So(compiledOT.DataProperties[0].Aliases, ShouldResemble, []string{"订单号"})
		So(bkn.ValidateObjectTypeDeclarations(compiledOT), ShouldBeNil)
		compiledRT, err := compileRelationType(items.RelationTypes[1])
		So(err, ShouldBeNil)
		So(compiledRT.Aliases, ShouldResemble, []string{"包含商品"})
		So(bkn.ValidateRelationTypeDeclarations(compiledRT), ShouldBeNil)

		compiled, err := compileDraft(items)
		So(err, ShouldBeNil)
		So(string(compiled.Files["object_types/orders.bkn"]), ShouldContainSubstring, "订单号")
		So(string(compiled.Files["object_types/orders.bkn"]), ShouldContainSubstring, "aliases:")
		So(string(compiled.Files["relation_types/orders_to_products.bkn"]), ShouldContainSubstring, "包含商品")
	})
}

func Test_CompileDropsRefusedAliases(t *testing.T) {
	Convey("Aliases BKN would refuse after people renamed items are left out of the definitions\n", t, func() {
		items := compileFixture()
		orders, customers := items.ObjectTypes[0], items.ObjectTypes[1]
		// Renamed to its own alias, and an alias that is another object type's name.
		orders.Name, orders.Aliases = "订单", []string{"订单", "Customers", "销售单"}
		// A property renamed to its alias, and one whose alias is another property's display name.
		orders.Properties[0].DisplayName, orders.Properties[0].Aliases = "订单号", []string{"订单号", "单号"}
		orders.Properties[1].Aliases = []string{"Order", "订单号", "客户"}
		customers.Aliases = []string{"客户"}
		items.RelationTypes[1].Name, items.RelationTypes[1].Aliases = "包含", []string{"包含", "包含商品"}

		compiled, err := compileDraft(items)
		So(err, ShouldBeNil)
		network, err := loadWithBranch(compiled.Files)
		So(err, ShouldBeNil)
		So(bkn.ValidateNetworkDeclarations(network), ShouldBeNil)
		objectTypes := map[string]*bkn.BknObjectType{}
		for _, ot := range network.ObjectTypes {
			So(bkn.ValidateObjectTypeDeclarations(ot), ShouldBeNil)
			objectTypes[ot.ID] = ot
		}
		ot := objectTypes["orders"]
		So(ot.Aliases, ShouldResemble, []string{"销售单"})
		So(ot.DataProperties[0].Aliases, ShouldResemble, []string{"单号"})
		// 订单号 is now the first property's display name; Order and 客户 are no property's.
		So(ot.DataProperties[1].Aliases, ShouldResemble, []string{"Order", "客户"})
		So(objectTypes["customers"].Aliases, ShouldResemble, []string{"客户"})
		for _, rt := range network.RelationTypes {
			So(bkn.ValidateRelationTypeDeclarations(rt), ShouldBeNil)
			if rt.ID == "orders_to_products" {
				So(rt.Aliases, ShouldResemble, []string{"包含商品"})
			}
		}
		// The draft itself keeps them: people may rename the item back.
		So(orders.Aliases, ShouldResemble, []string{"订单", "Customers", "销售单"})
	})
}
