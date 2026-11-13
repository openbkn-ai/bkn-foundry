// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_TextLanguage(t *testing.T) {
	Convey("Test reading a text's content language by its script\n", t, func() {
		So(textLanguage("销售订单"), ShouldEqual, interfaces.NAMING_LANGUAGE_ZH_CN)
		So(textLanguage("SKU 编码"), ShouldEqual, interfaces.NAMING_LANGUAGE_ZH_CN)
		So(textLanguage("Sales order"), ShouldEqual, interfaces.NAMING_LANGUAGE_EN_US)
		So(textLanguage("2026-11"), ShouldEqual, "")
		So(inOtherLanguage(interfaces.NAMING_LANGUAGE_EN_US, "订单"), ShouldBeTrue)
		So(inOtherLanguage(interfaces.NAMING_LANGUAGE_ZH_CN, "订单"), ShouldBeFalse)
		So(inOtherLanguage(interfaces.NAMING_LANGUAGE_EN_US, ""), ShouldBeFalse)
	})
}

func Test_NameInLanguage(t *testing.T) {
	Convey("Test naming drafted items in the project's language when the catalog's are in the other\n", t, func() {
		zh, en := interfaces.NAMING_LANGUAGE_ZH_CN, interfaces.NAMING_LANGUAGE_EN_US
		caller := interfaces.AccountInfo{ID: "u1", Type: "user"}
		// The catalog was understood in Chinese, for another project that shares it.
		catalog := func() []*interfaces.VegaTable {
			customerID := col("customer_id", "string", "客户的唯一编号")
			customerID.DisplayName = "客户编号"
			customerName := col("customer_name", "string", "")
			customerName.DisplayName = "客户名称"
			customers := table("r_customers", "c1", "客户", 60, []string{"customer_id"}, customerID, customerName)
			customers.SourceName, customers.Description = "customers", "购买产品的客户"

			orderID := col("order_id", "string", "")
			orderID.DisplayName = "订单编号"
			orderCustomer := col("customer_id", "string", "")
			orderCustomer.DisplayName = "下单客户"
			untouched := col("amount", "decimal", "")
			orders := table("r_orders", "c1", "订单", 800, []string{"order_id"}, orderID, orderCustomer, untouched)
			orders.SourceName, orders.Description = "orders", "客户下的销售订单"
			orders.ForeignKeys = []interfaces.VegaForeignKey{fk("customer_id", "customers", "customer_id")}
			return []*interfaces.VegaTable{customers, orders}
		}
		english := map[string][2]string{
			"客户": {"Customer", "A customer who buys products"}, "订单": {"Sales order", "A sales order a customer places"},
			"客户编号": {"Customer ID", "The unique number of a customer"}, "客户名称": {"Customer name", ""},
			"订单编号": {"Order ID", ""}, "下单客户": {"Ordering customer", ""},
		}
		draftIn := func(language string) interfaces.DraftItems {
			d := newDrafter(testSetting, sampleCounter()).withNamingLanguage(language)
			items, err := d.draft(context.Background(), catalog(), nil)
			So(err, ShouldBeNil)
			return items
		}
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &draftService{setting: testSetting, ba: ba}
		var asked []namingInput
		translate := func(_ context.Context, _ interfaces.AccountInfo, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
			var in namingInput
			if req.AgentID != namingAgentID || json.Unmarshal([]byte(req.Message), &in) != nil {
				return nil, errors.New("not a naming request")
			}
			asked = append(asked, in)
			out := struct {
				Items []namingOutput `json:"items"`
			}{}
			for _, item := range in.Items {
				out.Items = append(out.Items, namingOutput{Key: item.Key, Name: english[item.Name][0],
					Description: english[item.Name][1]})
			}
			raw, _ := json.Marshal(out)
			return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: string(raw)}, nil
		}

		Convey("An English project drafts English names and descriptions, the catalog's names as aliases\n", func() {
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(translate)
			items := draftIn(en)
			svc.nameInLanguage(context.Background(), caller, &items, en, catalog())
			So(asked, ShouldHaveLength, 1)
			So(asked[0].Language, ShouldEqual, en)

			customers, orders := objectType(items, "customers"), objectType(items, "orders")
			So(customers.Name, ShouldEqual, "Customer")
			So(customers.Aliases, ShouldContain, "客户")
			So(customers.Description, ShouldEqual, "A customer who buys products")
			So(orders.Name, ShouldEqual, "Sales order")
			So(orders.Description, ShouldEqual, "A sales order a customer places")
			So(findProperty(customers, "customer_id").DisplayName, ShouldEqual, "Customer ID")
			So(findProperty(customers, "customer_id").Aliases, ShouldContain, "客户编号")
			So(findProperty(customers, "customer_id").Description, ShouldEqual, "The unique number of a customer")
			So(findProperty(orders, "customer_id").DisplayName, ShouldEqual, "Ordering customer")
			// A field still named by its physical name reads in no language: it is not asked about.
			So(findProperty(orders, "amount").DisplayName, ShouldEqual, "amount")
			for _, item := range asked[0].Items {
				So(item.Source, ShouldNotEqual, "amount")
			}
			// The relation drafted from the referring field is named from the new names.
			r := relation(items, orders.ID, customers.ID)
			So(r, ShouldNotBeNil)
			So(r.Name, ShouldEqual, relationNameIn(en, orders, "customer_id", r.ID))
			So(textLanguage(r.Name), ShouldEqual, en)
		})

		Convey("A Chinese project on the same catalog keeps its names and asks nothing\n", func() {
			items := draftIn(zh)
			before, _ := json.Marshal(items)
			svc.nameInLanguage(context.Background(), caller, &items, zh, catalog())
			after, _ := json.Marshal(items)
			So(string(after), ShouldEqual, string(before))
			So(objectType(items, "customers").Name, ShouldEqual, "客户")
		})

		Convey("Items the network already has, and those of tables not new, stay as they are\n", func() {
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(translate)
			items := draftIn(en)
			customers := objectType(items, "customers")
			customers.Existing = true
			tables := catalog()
			svc.nameInLanguage(context.Background(), caller, &items, en, tables[1:])
			So(customers.Name, ShouldEqual, "客户")
			So(findProperty(customers, "customer_id").DisplayName, ShouldEqual, "客户编号")
			So(objectType(items, "orders").Name, ShouldEqual, "Sales order")
			for _, item := range asked[0].Items {
				So(item.Key, ShouldNotStartWith, "o:"+customers.ID)
				So(item.Key, ShouldNotStartWith, "p:"+customers.ID+":")
			}
		})

		Convey("A name given in the other language still, or a failed task, leaves the catalog's names\n", func() {
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED,
				Output: `{"items":[{"key":"o:customers","name":"顾客","description":"顾客"}]}`}, nil)
			items := draftIn(en)
			svc.nameInLanguage(context.Background(), caller, &items, en, catalog())
			So(objectType(items, "customers").Name, ShouldEqual, "客户")

			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(nil, errors.New("unavailable"))
			items = draftIn(en)
			svc.nameInLanguage(context.Background(), caller, &items, en, catalog())
			So(objectType(items, "orders").Name, ShouldEqual, "订单")
		})

		Convey("Naming leaves drafting the time it needs to finish: with too little left it asks nothing\n", func() {
			items := draftIn(en)
			ctx, cancel := context.WithTimeout(context.Background(), namingReserve/2)
			defer cancel()
			svc.nameInLanguage(ctx, caller, &items, en, catalog())
			So(objectType(items, "customers").Name, ShouldEqual, "客户")
			So(ctx.Err(), ShouldBeNil)
		})

		Convey("Without the Agent drafting names as before\n", func() {
			items := draftIn(en)
			(&draftService{setting: testSetting}).nameInLanguage(context.Background(), caller, &items, en, catalog())
			So(objectType(items, "customers").Name, ShouldEqual, "客户")
		})
	})
}

func Test_NamingBatches(t *testing.T) {
	Convey("Test batching the items to name: an object type with its properties goes together\n", t, func() {
		group := func(n int) []namingItem { return make([]namingItem, n) }
		sizes := func(batches [][]namingItem) []int {
			out := []int{}
			for _, b := range batches {
				out = append(out, len(b))
			}
			return out
		}
		So(sizes(batchesOf([][]namingItem{group(10), group(20), group(15)})), ShouldResemble, []int{30, 15})
		So(sizes(batchesOf([][]namingItem{group(namingBatchMax + 5), group(3)})), ShouldResemble, []int{namingBatchMax, 8})
	})
}

func Test_UnderstoodConceptLanguage(t *testing.T) {
	Convey("Test a table's concept named by the catalog's understanding in the other language\n", t, func() {
		zh, en := interfaces.NAMING_LANGUAGE_ZH_CN, interfaces.NAMING_LANGUAGE_EN_US
		understood := func() *interfaces.VegaTable {
			t := table("r_orders", "c1", "订单", 0, nil, col("id", "string", ""))
			t.SourceName, t.Description = "orders", "客户下的销售订单"
			return t
		}
		concept := func(language string) interfaces.DraftConcept {
			c := interfaces.DraftConcept{Name: "orders", Sources: []interfaces.DraftConceptSource{{}}}
			So(understandConcept(&c, &interfaces.DraftObjectType{Name: "orders"}, understood(), language), ShouldBeTrue)
			return c
		}

		Convey("An English project keeps the concept's name, the catalog's Chinese name an alias\n", func() {
			c := concept(en)
			So(c.Name, ShouldEqual, "orders")
			So(c.Aliases, ShouldResemble, []string{"订单"})
			So(c.Description, ShouldEqual, "")
		})

		Convey("A Chinese project takes the catalog's name and description as before\n", func() {
			c := concept(zh)
			So(c.Name, ShouldEqual, "订单")
			So(c.Aliases, ShouldResemble, []string{"orders"})
			So(c.Description, ShouldEqual, "客户下的销售订单")
		})
	})
}
