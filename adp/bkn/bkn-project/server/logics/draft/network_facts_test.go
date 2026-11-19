// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// netCounter answers the scan's counts from a table of numbers: rows, nulls, orphans by
// "source.field>target.key", with "?value" for one type value. A pair it does not know finds nothing.
type netCounter struct {
	fakeCounter
	joinCounts map[string][3]int64
	types      map[string][]string
	calls      []string
}

func (n *netCounter) joinStats(_ context.Context, source *interfaces.VegaTable, field string,
	target *interfaces.VegaTable, key string) (int64, int64, int64, int64, error) {
	k := source.ID + "." + field + ">" + target.ID + "." + key
	n.calls = append(n.calls, k)
	c, ok := n.joinCounts[k]
	if !ok {
		// The field's rows and empties are the same whatever it is counted against.
		for known, counts := range n.joinCounts {
			if strings.HasPrefix(known, source.ID+"."+field+">") && !strings.Contains(known, "?") {
				return counts[0], counts[1], counts[0] - counts[1], 0, nil
			}
		}
		return 100, 0, 100, 0, nil
	}
	return c[0], c[1], c[2], 0, nil
}

func (n *netCounter) typeValues(_ context.Context, table *interfaces.VegaTable, field string, _ int) ([]string, error) {
	n.calls = append(n.calls, "types:"+table.ID+"."+field)
	return n.types[table.ID+"."+field], nil
}

func (n *netCounter) joinStatsWhere(ctx context.Context, source *interfaces.VegaTable, field string,
	target *interfaces.VegaTable, key string, _ string, value string) (int64, int64, int64, int64, error) {
	k := source.ID + "." + field + ">" + target.ID + "." + key + "?" + value
	n.calls = append(n.calls, k)
	c, ok := n.joinCounts[k]
	if !ok {
		return 10, 0, 10, 0, nil
	}
	return c[0], c[1], c[2], 0, nil
}

func netProp(name, display, typ string) *interfaces.DraftProperty {
	return &interfaces.DraftProperty{Name: name, DisplayName: display, Type: typ, MappedField: name}
}

func netObject(id, name string, adopted bool, key string, props ...*interfaces.DraftProperty) *interfaces.DraftObjectType {
	ot := &interfaces.DraftObjectType{ID: id, Name: name, Adopted: adopted, Properties: props,
		Table: interfaces.DraftTable{ResourceID: "r_" + id, CatalogID: "c1", Name: id, SourceName: "erp_" + id}}
	if key == "" {
		ot.NeedsIdentity = true
	} else {
		ot.PrimaryKeys = []string{key}
	}
	return ot
}

// diagnosisSample is shaped like the supply chain sample: a work order whose source type and source
// number refer to an MRP plan order, a plan order whose root demand number refers to a forecast for
// about 72% of its rows, products whose codes are all material codes, a production line with a
// parent line, a sample batch nothing refers to, and a purchase request waiting for its identity.
func diagnosisSample() *interfaces.Draft {
	material := netObject("material", "物料", true, "code", netProp("code", "物料编码", "string"), netProp("name", "物料名称", "string"))
	product := netObject("product", "产品", true, "code", netProp("code", "物料编码", "string"),
		netProp("line_code", "产品线编码", "string"))
	forecast := netObject("forecast", "物料需求预测", true, "billno", netProp("billno", "预测单号", "string"),
		netProp("material_no", "物料编号", "string"))
	mrp := netObject("mrp_planorder", "MRP 计划订单", true, "billno", netProp("billno", "计划订单编号", "string"),
		netProp("rootdemandbillno", "根需求单据编号", "string"), netProp("qty", "数量", "float"))
	order := netObject("work_order", "生产工单", true, "billno", netProp("billno", "工单编号", "string"),
		netProp("material", "物料编码", "string"), netProp("sourcebilltype", "源单类型", "string"),
		netProp("sourcebillno", "源单编号", "string"))
	line := netObject("line", "生产线", true, "code", netProp("code", "生产线编码", "string"),
		netProp("parent_code", "上级生产线编码", "string"))
	batch := netObject("sample_batch", "样本批次", true, "batch_id", netProp("batch_id", "批次ID", "string"))
	request := netObject("purchase_request", "采购申请", false, "", netProp("billno", "申请单号", "string"),
		netProp("entry_id", "明细行ID", "string"), netProp("sourcebillno", "源单编号", "string"))
	request.Key.Candidates = [][]string{{"billno", "entry_id"}}
	return &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: 100,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{material, product, forecast, mrp, order, line, batch, request},
			RelationTypes: []*interfaces.DraftRelationType{
				{ID: "forecast_material", Name: "物料", Source: "forecast", Target: "material", Adopted: true,
					Mapping:  []interfaces.DraftFieldMapping{{SourceProperty: "material_no", TargetProperty: "code"}},
					Evidence: interfaces.DraftJoinEvidence{Computed: true, Rows: 137, Hits: 137, HitRate: 1}},
				{ID: "order_material", Name: "物料", Source: "work_order", Target: "material", Adopted: true,
					Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "material", TargetProperty: "code"}}},
				{ID: "product_line", Name: "产品线", Source: "product", Target: "line", Adopted: false,
					Mapping:  []interfaces.DraftFieldMapping{{SourceProperty: "line_code", TargetProperty: "code"}},
					Evidence: interfaces.DraftJoinEvidence{Computed: true, Rows: 30, Hits: 30, HitRate: 1}},
				{ID: "request_mrp", Name: "来源计划订单", Source: "purchase_request", Target: "mrp_planorder",
					Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "sourcebillno", TargetProperty: "billno"}}},
			},
			Inputs: &interfaces.DraftInputs{NamingLanguage: interfaces.NAMING_LANGUAGE_ZH_CN},
		}}
}

func diagnosisCounts() *netCounter {
	return &netCounter{
		joinCounts: map[string][3]int64{
			"r_mrp_planorder.rootdemandbillno>r_forecast.billno":             {799, 0, 227},
			"r_work_order.sourcebillno>r_mrp_planorder.billno":               {605, 586, 0},
			"r_work_order.sourcebillno>r_mrp_planorder.billno?mrp_planorder": {19, 0, 0},
			"r_product.code>r_material.code":                                 {30, 0, 0},
			"r_material.code>r_product.code":                                 {200, 0, 170},
			"r_line.parent_code>r_line.code":                                 {12, 2, 0},
		},
		types: map[string][]string{"r_work_order.sourcebilltype": {"mrp_planorder"}},
	}
}

func findReference(refs []interfaces.DraftReference, source, property, target string) *interfaces.DraftReference {
	for i := range refs {
		if refs[i].Source == source && refs[i].SourceProperty == property && refs[i].Target == target {
			return &refs[i]
		}
	}
	return nil
}

func Test_NetworkFacts(t *testing.T) {
	Convey("Test the facts of a network diagnosis\n", t, func() {
		budget := scanBudget{pairs: 300, timeout: time.Minute}

		Convey("Components are the adopted object types joined by adopted relations, the largest first\n", func() {
			components := NetworkComponents(&diagnosisSample().Items)
			So(components, ShouldResemble, [][]string{{"material", "forecast", "work_order"}, {"product"},
				{"mrp_planorder"}, {"line"}, {"sample_batch"}})
		})

		Convey("An object type left out lists what its adoption brings in and its identity candidates\n", func() {
			pending := pendingObjects(&diagnosisSample().Items)
			So(pending, ShouldResemble, []interfaces.DraftPendingObject{{ObjectTypeID: "purchase_request",
				NeedsIdentity: true, Candidates: [][]string{{"billno", "entry_id"}}, Relations: []string{"request_mrp"}}})
		})

		Convey("A field whose values are contained in another identity is a reference, whatever its name\n", func() {
			facts := networkFacts(context.Background(), diagnosisSample(), diagnosisCounts(), budget)
			r := findReference(facts.References, "mrp_planorder", "rootdemandbillno", "forecast")
			So(r, ShouldNotBeNil)
			So(r.Counted, ShouldBeTrue)
			So(r.Hits, ShouldEqual, 572)
			So(r.Rate, ShouldAlmostEqual, 572.0/799, 0.0001)
			So(r.Name, ShouldEqual, "根需求单据")
			// A pair that finds nothing is no reference.
			So(findReference(facts.References, "mrp_planorder", "rootdemandbillno", "material"), ShouldBeNil)
			// A relation the draft has is not counted again.
			known := findReference(facts.References, "forecast", "material_no", "material")
			So(known.RelationTypeID, ShouldEqual, "forecast_material")
			So(known.Hits, ShouldEqual, 137)
		})

		Convey("A type field and a code field are a polymorphic reference counted per type value\n", func() {
			c := diagnosisCounts()
			facts := networkFacts(context.Background(), diagnosisSample(), c, budget)
			So(c.calls, ShouldContain, "types:r_work_order.sourcebilltype")
			refs := []interfaces.DraftReference{}
			for _, r := range facts.References {
				if r.Source == "work_order" && r.SourceProperty == "sourcebillno" {
					refs = append(refs, r)
				}
			}
			// The branch replaces the plain count of its field and target.
			So(len(refs), ShouldEqual, 1)
			So(refs[0].Target, ShouldEqual, "mrp_planorder")
			So(refs[0].Condition, ShouldResemble, &interfaces.DraftReferenceCondition{Property: "sourcebilltype", Value: "mrp_planorder"})
			So(refs[0].Hits, ShouldEqual, 19)
			So(refs[0].Rate, ShouldEqual, 1)
			// How far the data fills the reference in comes from the field over all records.
			So(refs[0].FieldRows, ShouldEqual, 605)
			So(refs[0].FieldNulls, ShouldEqual, 586)
		})

		Convey("A parent field is counted against the object type's own identity\n", func() {
			facts := networkFacts(context.Background(), diagnosisSample(), diagnosisCounts(), budget)
			r := findReference(facts.References, "line", "parent_code", "line")
			So(r, ShouldNotBeNil)
			So(r.Self, ShouldBeTrue)
			So(r.Hits, ShouldEqual, 10)
			// A code field that is no parent is never counted against its own object type.
			So(findReference(facts.References, "work_order", "material", "work_order"), ShouldBeNil)
		})

		Convey("An identity whose every value is another's is a subtype; not the other way round\n", func() {
			facts := networkFacts(context.Background(), diagnosisSample(), diagnosisCounts(), budget)
			So(facts.Subtypes, ShouldResemble, []interfaces.DraftSubtype{{Sub: "product", Super: "material",
				SubProperty: "code", SuperProperty: "code", Rows: 30, Hits: 30}})
		})

		Convey("Pairs beyond the budget are not counted and reported so\n", func() {
			c := diagnosisCounts()
			facts := networkFacts(context.Background(), diagnosisSample(), c, scanBudget{pairs: 3, timeout: time.Minute})
			counted := 0
			for _, call := range c.calls {
				if call[:6] != "types:" {
					counted++
				}
			}
			So(counted, ShouldBeLessThanOrEqualTo, 3)
			So(facts.NotCounted, ShouldBeGreaterThan, 0)
			So(facts.Pairs, ShouldBeGreaterThan, facts.NotCounted)
		})

		Convey("Out of time, the pairs left are not counted\n", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			facts := networkFacts(ctx, diagnosisSample(), diagnosisCounts(), budget)
			// The relations the draft counted already need no count.
			So(facts.NotCounted, ShouldEqual, facts.Pairs-2)
			for _, r := range facts.References {
				So(r.Counted, ShouldEqual, r.RelationTypeID == "forecast_material" || r.RelationTypeID == "product_line")
			}
		})
	})
}

func Test_CodeLikeFields(t *testing.T) {
	Convey("Code-like, type and parent fields are told by their names\n", t, func() {
		So(codeLike(netProp("rootdemandbillno", "根需求单据编号", "string")), ShouldBeTrue)
		So(codeLike(netProp("supplier_id", "", "integer")), ShouldBeTrue)
		So(codeLike(netProp("CustomerNo", "", "string")), ShouldBeTrue)
		So(codeLike(netProp("remark", "备注", "string")), ShouldBeFalse)
		So(codeLike(netProp("material_no", "物料编号", "float")), ShouldBeFalse)
		So(typeLike(netProp("sourcebilltype", "源单类型", "string")), ShouldBeTrue)
		So(parentLike(netProp("parent_code", "上级生产线编码", "string")), ShouldBeTrue)
		So(sameStem(netProp("sourcebilltype", "源单类型", "string"), netProp("sourcebillno", "源单编号", "string")), ShouldBeTrue)
		So(sameStem(netProp("resource_type", "资源类型", "string"), netProp("resource_code", "资源编码", "string")), ShouldBeTrue)
		So(sameStem(netProp("sourcebilltype", "源单类型", "string"), netProp("material", "物料编码", "string")), ShouldBeFalse)
	})
}

func Test_DismissSuggestion(t *testing.T) {
	Convey("Dismissing a suggestion records a decision of the draft and changes no item\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		ps := pmock.NewMockProjectService(mockCtrl)
		da := pmock.NewMockDraftAccess(mockCtrl)
		s := &draftService{setting: common.DraftSetting{}, da: da, ps: ps}
		caller := interfaces.AccountInfo{ID: "u1", Type: "user"}
		ps.EXPECT().GetProject(gomock.Any(), "t", "p1").AnyTimes().Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil)
		d := diagnosisSample()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
		da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), int64(100)).Return(true, nil)

		saved, err := s.DismissSuggestion(context.Background(), "t", "p1", "s1", " 巧合 ", caller)

		So(err, ShouldBeNil)
		So(saved.UpdateTime, ShouldBeGreaterThan, 100)
		last := saved.Items.Decisions[len(saved.Items.Decisions)-1]
		So(last.Kind, ShouldEqual, interfaces.DRAFT_KIND_DIAGNOSIS)
		So(last.ItemID, ShouldEqual, "s1")
		So(last.Action, ShouldEqual, interfaces.DRAFT_DECISION_DISMISS)
		So(last.Reason, ShouldEqual, "巧合")
		So(last.By, ShouldResemble, caller)

		Convey("A draft changed meanwhile is stale\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(diagnosisSample(), true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), int64(100)).Return(false, nil)
			_, err := s.DismissSuggestion(context.Background(), "t", "p1", "s1", "", caller)
			So(err, ShouldNotBeNil)
			So(errors.Unwrap(err), ShouldBeNil)
		})
	})
}

func Test_NetworkCounterSQL(t *testing.T) {
	Convey("Type values and a branch's join are counted in one query each, a value quoted as a literal\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		va := pmock.NewMockVegaAccess(mockCtrl)
		c := &vegaCounter{va: va, account: interfaces.AccountInfo{ID: "u1"}, timeoutSeconds: 30}
		va.EXPECT().Query(gomock.Any(), gomock.Any(),
			"SELECT `sourcebilltype` AS type_value, COUNT(*) AS n FROM {{r1}} WHERE `sourcebilltype` IS NOT NULL "+
				"GROUP BY `sourcebilltype` ORDER BY n DESC LIMIT 5", 30).
			Return([]map[string]any{{"type_value": "mrp_planorder", "n": 19}, {"type_value": " ", "n": 1}}, nil)
		values, err := c.typeValues(context.Background(), &interfaces.VegaTable{ID: "r1"}, "sourcebilltype", 5)
		So(err, ShouldBeNil)
		So(values, ShouldResemble, []string{"mrp_planorder"})

		va.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), 30).DoAndReturn(
			func(_ context.Context, _ interfaces.VegaAccount, sql string, _ int) ([]map[string]any, error) {
				So(sql, ShouldEndWith, "WHERE s.`sourcebilltype` = 'it''s'")
				So(sql, ShouldContainSubstring, "LEFT JOIN (SELECT DISTINCT `billno` FROM {{r2}}) t")
				return []map[string]any{{"total": 19, "nulls": 0, "orphans": 1, "distinct_values": 19}}, nil
			})
		rows, nulls, orphans, _, err := c.joinStatsWhere(context.Background(), &interfaces.VegaTable{ID: "r1"}, "sourcebillno",
			&interfaces.VegaTable{ID: "r2"}, "billno", "sourcebilltype", "it's")
		So(err, ShouldBeNil)
		So([]int64{rows, nulls, orphans}, ShouldResemble, []int64{19, 0, 1})

		va.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), 30).DoAndReturn(
			func(_ context.Context, _ interfaces.VegaAccount, sql string, _ int) ([]map[string]any, error) {
				So(sql, ShouldEndWith, `WHERE s.`+"`sourcebilltype`"+` = 'a\\'' OR 1=1 \\'`)
				return []map[string]any{{"total": 0}}, nil
			})
		_, _, _, _, err = c.joinStatsWhere(context.Background(), &interfaces.VegaTable{ID: "r1"}, "sourcebillno",
			&interfaces.VegaTable{ID: "r2"}, "billno", "sourcebilltype", `a\' OR 1=1 \`)
		So(err, ShouldBeNil)
	})
}

// realShaped is the sample as 8081 has it: a forecast identified by its record id whose bill number
// rows share; a plan order whose root demand number refers to that bill number; a purchase request
// and order waiting for their identities, the order's source number referring to the request's bill
// number; a work order whose source type and number refer to a plan order; a stoppage whose resource
// kind and code refer to a production line; a BOM whose row sequence matches inventory record ids.
func realShaped() *interfaces.Draft {
	integer := func(name, display string) *interfaces.DraftProperty {
		p := netProp(name, display, "integer")
		return p
	}
	forecast := netObject("erp_mds_forecast", "物料需求预测", true, "id", netProp("id", "预测记录ID", "string"),
		netProp("billno", "预测单号", "string"), netProp("material_number", "物料编号", "string"))
	material := netObject("erp_material", "物料", true, "material_code", netProp("material_code", "物料编码", "string"))
	mrp := netObject("erp_mrp_plan_order", "MRP计划订单", true, "billno", netProp("billno", "计划订单编号", "string"),
		netProp("materialplanid_number", "物料计划编号", "string"), netProp("rootdemandbillno", "根需求单据编号", "string"))
	order := netObject("erp_production_work_order", "生产工单", true, "id", integer("id", "工单ID"),
		netProp("billno", "工单编号", "string"), netProp("sourcebilltype", "源单类型", "text"),
		netProp("sourcebillnumber", "源单编号", "string"))
	request := netObject("erp_purchase_request", "采购申请", false, "", integer("id", "采购申请ID"),
		netProp("billno", "采购申请单号", "string"), integer("entry_id", "明细行ID"), netProp("srcbillnumber", "源单编号", "string"))
	request.Key.Candidates = [][]string{{"id", "entry_id"}}
	purchase := netObject("erp_purchase_order", "采购订单", false, "", integer("id", "采购订单ID"),
		netProp("billno", "订单编号", "string"), integer("entry_id", "分录ID"), netProp("srcbillnumber", "源单编号", "string"))
	purchase.Key.Candidates = [][]string{{"id", "entry_id"}}
	line := netObject("production_line", "生产线", true, "line_code", netProp("line_code", "生产线编码", "string"))
	stoppage := netObject("line_stoppage", "产线停机", true, "stoppage_id", netProp("stoppage_id", "停机事件ID", "string"),
		netProp("resource_code", "资源编码", "string"), netProp("resource_kind", "资源类型", "text"))
	bom := netObject("erp_material_bom", "物料清单", false, "", integer("seq_no", "BOM行序号"),
		netProp("bom_material_code", "BOM物料编码", "string"))
	inventory := netObject("erp_real_time_inventory", "实时库存", true, "id", integer("id", "库存记录ID"),
		integer("seq_no", "记录序号"), integer("warehouse_id", "仓库ID"))
	return &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: 100,
		Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{forecast, material, mrp, order, request, purchase,
			line, stoppage, bom, inventory},
			Inputs: &interfaces.DraftInputs{NamingLanguage: interfaces.NAMING_LANGUAGE_ZH_CN}}}
}

func realCounts() *netCounter {
	return &netCounter{
		joinCounts: map[string][3]int64{
			"r_erp_mrp_plan_order.rootdemandbillno>r_erp_mds_forecast.billno":                        {799, 0, 227},
			"r_erp_mrp_plan_order.materialplanid_number>r_erp_material.material_code":                {824, 0, 0},
			"r_erp_production_work_order.sourcebillnumber>r_erp_mrp_plan_order.billno":               {605, 586, 0},
			"r_erp_production_work_order.sourcebillnumber>r_erp_mrp_plan_order.billno?mrp_planorder": {19, 0, 0},
			"r_erp_purchase_order.srcbillnumber>r_erp_purchase_request.billno":                       {1865, 0, 80},
			"r_erp_purchase_request.srcbillnumber>r_erp_mrp_plan_order.billno":                       {19333, 19189, 0},
			"r_line_stoppage.resource_code>r_production_line.line_code?生产线":                          {40, 0, 0},
			"r_erp_material_bom.seq_no>r_erp_real_time_inventory.id":                                 {31423, 0, 12115},
			"r_erp_real_time_inventory.warehouse_id>r_erp_production_work_order.id":                  {500, 0, 150},
		},
		types: map[string][]string{"r_erp_production_work_order.sourcebilltype": {"mrp_planorder"},
			"r_line_stoppage.resource_kind": {"生产线"}},
	}
}

func Test_NetworkFactsOrder(t *testing.T) {
	Convey("The scan counts the document chain first, within a small budget\n", t, func() {
		c := realCounts()
		facts := networkFacts(context.Background(), realShaped(), c, scanBudget{pairs: 20, timeout: time.Minute, enough: 0.6})

		root := findReference(facts.References, "erp_mrp_plan_order", "rootdemandbillno", "erp_mds_forecast")
		So(root, ShouldNotBeNil)
		So(root.Counted, ShouldBeTrue)
		// A document number is a key a reference may point at although it is no identity.
		So(root.TargetProperty, ShouldEqual, "billno")
		So(root.Hits, ShouldEqual, 572)

		// An object type waiting for its identity is still counted against its document number.
		src := findReference(facts.References, "erp_purchase_order", "srcbillnumber", "erp_purchase_request")
		So(src, ShouldNotBeNil)
		So(src.Counted, ShouldBeTrue)
		So(src.Hits, ShouldEqual, 1785)

		branch := findReference(facts.References, "erp_production_work_order", "sourcebillnumber", "erp_mrp_plan_order")
		So(branch.Condition, ShouldResemble, &interfaces.DraftReferenceCondition{Property: "sourcebilltype", Value: "mrp_planorder"})
		So(branch.Hits, ShouldEqual, 19)

		// A type field of text and a code field are a polymorphic reference; the value names the target.
		stop := findReference(facts.References, "line_stoppage", "resource_code", "production_line")
		So(stop, ShouldNotBeNil)
		So(stop.Condition.Value, ShouldEqual, "生产线")
		So(c.calls, ShouldContain, "r_line_stoppage.resource_code>r_production_line.line_code?生产线")
		So(facts.NotCounted, ShouldBeGreaterThan, 0)
		So(facts.Uncounted, ShouldNotBeEmpty)
	})

	Convey("Row numbers refer to nothing, and small integers need a name or nearly every value\n", t, func() {
		c := realCounts()
		facts := networkFacts(context.Background(), realShaped(), c, scanBudget{pairs: 300, timeout: time.Minute, enough: 0.6})
		for _, call := range c.calls {
			So(strings.HasPrefix(call, "r_erp_material_bom.seq_no>"), ShouldBeFalse)
			So(strings.HasPrefix(call, "r_erp_real_time_inventory.seq_no>"), ShouldBeFalse)
		}
		So(findReference(facts.References, "erp_material_bom", "seq_no", "erp_real_time_inventory"), ShouldBeNil)
		// 350/500 of an integer field whose name says nothing of its target is a coincidence.
		So(findReference(facts.References, "erp_real_time_inventory", "warehouse_id", "erp_production_work_order"), ShouldBeNil)
	})

	Convey("A role that merely extends the target's name takes the target's name\n", t, func() {
		facts := networkFacts(context.Background(), realShaped(), realCounts(), scanBudget{pairs: 300, timeout: time.Minute, enough: 0.6})
		r := findReference(facts.References, "erp_mrp_plan_order", "materialplanid_number", "erp_material")
		So(r.Name, ShouldEqual, "物料")
		So(findReference(facts.References, "erp_mrp_plan_order", "rootdemandbillno", "erp_mds_forecast").Name, ShouldEqual, "根需求单据")
	})

	Convey("Sequence, source and document number fields are told by their names\n", t, func() {
		So(sequenceLike(netProp("seq_no", "BOM行序号", "integer")), ShouldBeTrue)
		So(sequenceLike(netProp("entry_id", "明细行ID", "integer")), ShouldBeTrue)
		So(sequenceLike(netProp("line_no", "", "integer")), ShouldBeTrue)
		So(sequenceLike(netProp("line_code", "生产线编码", "string")), ShouldBeFalse)
		So(sourceLike(netProp("srcbillnumber", "源单编号", "string")), ShouldBeTrue)
		So(sourceLike(netProp("rootdemandbillno", "根需求单据编号", "string")), ShouldBeTrue)
		So(sourceLike(netProp("material_number", "物料编号", "string")), ShouldBeFalse)
		So(documentNumber(netProp("billno", "预测单号", "string")), ShouldBeTrue)
		So(documentNumber(netProp("billno", "工单编号", "string")), ShouldBeTrue)
		So(documentNumber(netProp("material_code", "物料编码", "string")), ShouldBeFalse)
	})
}
