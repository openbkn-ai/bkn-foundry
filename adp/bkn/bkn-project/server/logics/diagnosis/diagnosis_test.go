// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package diagnosis

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func prop(name, display string) *interfaces.DraftProperty {
	return &interfaces.DraftProperty{Name: name, DisplayName: display, Type: "string", MappedField: name}
}

func object(id, name string, adopted bool, key string, props ...*interfaces.DraftProperty) *interfaces.DraftObjectType {
	ot := &interfaces.DraftObjectType{ID: id, Name: name, Adopted: adopted, Properties: props,
		Table: interfaces.DraftTable{ResourceID: "r_" + id, CatalogID: "c1"}}
	if key == "" {
		ot.NeedsIdentity = true
	} else {
		ot.PrimaryKeys = []string{key}
	}
	return ot
}

// sampleDraft is shaped like the supply chain sample: see the draft package's diagnosisSample.
func sampleDraft() *interfaces.Draft {
	request := object("purchase_request", "采购申请", false, "", prop("billno", "申请单号"), prop("entry_id", "明细行ID"),
		prop("sourcebillno", "源单编号"))
	request.Key.Candidates = [][]string{{"billno", "entry_id"}}
	return &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: 100,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				object("material", "物料", true, "code", prop("code", "物料编码"), prop("name", "物料名称")),
				object("product", "产品", true, "code", prop("code", "物料编码"), prop("line_code", "产品线编码")),
				object("forecast", "物料需求预测", true, "billno", prop("billno", "预测单号"), prop("material_no", "物料编号"),
					prop("status", "状态")),
				object("mrp_planorder", "MRP 计划订单", true, "billno", prop("billno", "计划订单编号"),
					prop("rootdemandbillno", "根需求单据编号")),
				object("work_order", "生产工单", true, "billno", prop("billno", "工单编号"), prop("material", "物料编码"),
					prop("sourcebilltype", "源单类型"), prop("sourcebillno", "源单编号")),
				object("line", "生产线", true, "code", prop("code", "生产线编码"), prop("parent_code", "上级生产线编码")),
				object("sample_batch", "样本批次", true, "batch_id", prop("batch_id", "批次ID")),
				request,
			},
			RelationTypes: []*interfaces.DraftRelationType{
				{ID: "forecast_material", Name: "物料", Source: "forecast", Target: "material", Adopted: true,
					Mapping:  []interfaces.DraftFieldMapping{{SourceProperty: "material_no", TargetProperty: "code"}},
					Evidence: interfaces.DraftJoinEvidence{Computed: true, Rows: 137, Hits: 137, HitRate: 1}},
				{ID: "order_material", Name: "物料", Source: "work_order", Target: "material", Adopted: true,
					Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "material", TargetProperty: "code"}}},
				{ID: "product_line", Name: "产品线", Source: "product", Target: "line",
					Mapping:  []interfaces.DraftFieldMapping{{SourceProperty: "line_code", TargetProperty: "code"}},
					Evidence: interfaces.DraftJoinEvidence{Computed: true, Rows: 30, Hits: 30, HitRate: 1}},
				{ID: "request_mrp", Name: "来源计划订单", Source: "purchase_request", Target: "mrp_planorder",
					Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "sourcebillno", TargetProperty: "billno"}}},
			},
			Inputs: &interfaces.DraftInputs{NamingLanguage: interfaces.NAMING_LANGUAGE_ZH_CN},
		}}
}

// sampleFacts are the facts the scan finds on sampleDraft.
func sampleFacts(d *interfaces.Draft) *interfaces.DraftNetworkFacts {
	return &interfaces.DraftNetworkFacts{Draft: d,
		Components: [][]string{{"material", "forecast", "work_order"}, {"product"}, {"mrp_planorder"}, {"line"}, {"sample_batch"}},
		Pending: []interfaces.DraftPendingObject{{ObjectTypeID: "purchase_request", NeedsIdentity: true,
			Candidates: [][]string{{"billno", "entry_id"}}, Relations: []string{"request_mrp"}}},
		References: []interfaces.DraftReference{
			{Source: "mrp_planorder", SourceProperty: "rootdemandbillno", Target: "forecast", TargetProperty: "billno",
				Counted: true, Rows: 799, Hits: 572, Rate: 572.0 / 799, Name: "根需求单据", FieldRows: 799},
			{Source: "work_order", SourceProperty: "sourcebillno", Target: "mrp_planorder", TargetProperty: "billno",
				Condition: &interfaces.DraftReferenceCondition{Property: "sourcebilltype", Value: "mrp_planorder"},
				Counted:   true, Rows: 19, Hits: 19, Rate: 1, Name: "源单", FieldRows: 605, FieldNulls: 586},
			{Source: "line", SourceProperty: "parent_code", Target: "line", TargetProperty: "code", Self: true,
				Counted: true, Rows: 12, Nulls: 2, Hits: 10, Rate: 1, Name: "上级生产线", FieldRows: 12, FieldNulls: 2},
			{Source: "forecast", SourceProperty: "material_no", Target: "material", TargetProperty: "code",
				Counted: true, Rows: 137, Hits: 137, Rate: 1, RelationTypeID: "forecast_material", Name: "物料"},
			{Source: "mrp_planorder", SourceProperty: "rootdemandbillno", Target: "work_order", TargetProperty: "billno",
				Counted: true, Rows: 799, Hits: 30, Rate: 30.0 / 799, Name: "根需求单据"},
		},
		Subtypes: []interfaces.DraftSubtype{{Sub: "product", Super: "material", SubProperty: "code",
			SuperProperty: "code", Rows: 30, Hits: 30}},
		Pairs: 60, NotCounted: 4,
	}
}

func sampleQuestions() *interfaces.ScenarioQuestions {
	return &interfaces.ScenarioQuestions{Questions: []interfaces.ScenarioQuestion{
		{ID: "q1", Text: "这个产品的需求预测有哪些？", Unconnected: []interfaces.ScenarioUnconnected{{From: "product", To: "forecast"}}},
		{ID: "q2", Text: "这张预测单驱动了哪些计划订单？", Unconnected: []interfaces.ScenarioUnconnected{{From: "forecast", To: "mrp_planorder"}}},
		{ID: "q3", Text: "这个物料有哪些预测？"},
	}}
}

func sampleConcepts() []conceptInput {
	return []conceptInput{
		{ID: "c1", Term: "监测任务", Category: "object", Definition: "计划员对一张预测单的跟踪",
			Quotes: []interfaces.DiagnosisQuote{{DocumentID: "d1", Text: "计划员为每张预测单创建监测任务，并跟踪到完成"}}},
		{ID: "c2", Term: "采购进度状态", Category: "object", Definition: "采购单据的进度",
			Quotes: []interfaces.DiagnosisQuote{{DocumentID: "d1", Text: "按采购进度状态分组显示，根需求单据要能追到预测"}}},
	}
}

var setting = common.DraftSetting{DiagnosisHitRate: 0.6, DiagnosisMinRows: 10, DiagnosisTimeout: time.Minute}

func sampleJudge() *judge {
	return newJudge(setting, sampleFacts(sampleDraft()), sampleQuestions(), sampleConcepts())
}

func byKindTitle(list []*interfaces.DiagnosisSuggestion, kind string, title string) *interfaces.DiagnosisSuggestion {
	for _, s := range list {
		if s.Kind == kind && strings.Contains(s.Title, title) {
			return s
		}
	}
	return nil
}

func applyOf[T any](s *interfaces.DiagnosisSuggestion) T {
	var v T
	_ = json.Unmarshal(s.Apply, &v)
	return v
}

func index(i int) *int { return &i }

func goodWriteback() *interfaces.DiagnosisWriteback {
	return &interfaces.DiagnosisWriteback{Name: "监测任务", Description: "计划员对一张预测单的跟踪记录",
		TrackedObjectTypeID: "forecast",
		Properties: []interfaces.DiagnosisWritebackProperty{
			{Name: "task_id", DisplayName: "任务编号", Type: "string", Role: interfaces.WRITEBACK_ROLE_ID},
			{Name: "forecast_billno", DisplayName: "预测单", Type: "string", Role: interfaces.WRITEBACK_ROLE_TRACKED},
			{Name: "created_at", DisplayName: "创建时间", Type: "datetime", Role: interfaces.WRITEBACK_ROLE_TIME},
			{Name: "closed_at", DisplayName: "结束时间", Type: "datetime", Role: interfaces.WRITEBACK_ROLE_TIME},
			{Name: "planner", DisplayName: "计划员", Type: "string", Role: interfaces.WRITEBACK_ROLE_ACTOR},
			{Name: "decision", DisplayName: "决定", Type: "string", Role: interfaces.WRITEBACK_ROLE_DECISION},
			{Name: "basis_qty", DisplayName: "当时预测数量", Type: "decimal", Role: interfaces.WRITEBACK_ROLE_SNAPSHOT},
			{Name: "outcome", DisplayName: "结果", Type: "text", Role: interfaces.WRITEBACK_ROLE_OUTCOME},
		},
		Actions: []interfaces.DiagnosisWritebackAction{
			{Kind: interfaces.WRITEBACK_ACTION_ADD, Name: "创建监测任务", Parameters: []string{"task_id", "forecast_billno", "planner", "created_at"}},
			{Kind: interfaces.WRITEBACK_ACTION_MODIFY, Name: "结束监测任务", Parameters: []string{"decision", "outcome", "closed_at"}},
		}}
}

func Test_Judge(t *testing.T) {
	Convey("Test the suggestions of a network diagnosis\n", t, func() {
		Convey("Without Ask AI the suggestions come from the counts\n", func() {
			list, ai := sampleJudge().suggestions(nil)
			So(ai, ShouldBeFalse)

			root := byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "根需求单据")
			So(root, ShouldNotBeNil)
			So(root.Title, ShouldEqual, "MRP 计划订单 —根需求单据→ 物料需求预测")
			So(root.Group, ShouldEqual, interfaces.DIAGNOSIS_GROUP_CONNECT)
			So(root.Evidence.Hits, ShouldEqual, 572)
			So(root.Evidence.SourceField, ShouldEqual, "rootdemandbillno")
			So(root.Evidence.TargetField, ShouldEqual, "billno")
			So(root.Reason, ShouldContainSubstring, "572/799（72%）")
			So(root.Impact.Connects, ShouldResemble, []string{"mrp_planorder"})
			So(root.Impact.Questions, ShouldResemble, []string{"q2"})
			create := applyOf[interfaces.DiagnosisCreateRelation](root)
			So(create.Cardinality, ShouldEqual, interfaces.DRAFT_CARDINALITY_MANY_TO_ONE)
			So(create.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "rootdemandbillno", TargetProperty: "billno"}})

			// A polymorphic branch is one plain relation, its condition in the description.
			branch := byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "源单类型 = mrp_planorder")
			So(branch, ShouldNotBeNil)
			So(branch.Evidence.Condition, ShouldResemble, &interfaces.DiagnosisCondition{Field: "sourcebilltype", Value: "mrp_planorder"})
			So(applyOf[interfaces.DiagnosisCreateRelation](branch).Description, ShouldContainSubstring, "仅当「源单类型」为 mrp_planorder 时成立")

			// A subtype is a one-to-one relation to its supertype.
			sub := byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "产品 —对应物料→ 物料")
			So(sub, ShouldNotBeNil)
			So(applyOf[interfaces.DiagnosisCreateRelation](sub).Cardinality, ShouldEqual, interfaces.DRAFT_CARDINALITY_ONE_TO_ONE)
			So(sub.Impact.Connects, ShouldResemble, []string{"product"})
			So(sub.Impact.Questions, ShouldResemble, []string{"q1"})

			// A parent reference is a hierarchy relation.
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "生产线 —上级生产线→ 生产线"), ShouldNotBeNil)
			// A reference below the threshold is no suggestion without a quote.
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "→ 生产工单"), ShouldBeNil)

			adopt := byKindTitle(list, interfaces.DIAGNOSIS_KIND_ADOPT_RELATION, "产品线")
			So(adopt, ShouldNotBeNil)
			So(applyOf[interfaces.DiagnosisAdoptRelation](adopt).RelationTypeID, ShouldEqual, "product_line")

			identity := byKindTitle(list, interfaces.DIAGNOSIS_KIND_CHOOSE_IDENTITY, "采购申请")
			So(identity, ShouldNotBeNil)
			So(applyOf[interfaces.DiagnosisChooseIdentity](identity).Candidates, ShouldResemble, [][]string{{"billno", "entry_id"}})
			So(identity.Reason, ShouldContainSubstring, "申请单号 + 明细行ID")
			So(identity.Impact.Connects, ShouldContain, "purchase_request")

			// Only the isolated object type nothing touches is offered to leave out.
			excluded := []string{}
			for _, s := range list {
				if s.Kind == interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT {
					excluded = append(excluded, applyOf[interfaces.DiagnosisObject](s).ObjectTypeID)
				}
			}
			So(excluded, ShouldResemble, []string{"sample_batch"})

			quality := byKindTitle(list, interfaces.DIAGNOSIS_KIND_DATA_QUALITY, "586/605 条生产工单")
			So(quality, ShouldNotBeNil)
			So(string(quality.Apply), ShouldEqual, "null")
			So(quality.Rank, ShouldEqual, len(list))

			// Ranked by connections, questions and evidence; ranks run from 1.
			for i, s := range list {
				So(s.Rank, ShouldEqual, i+1)
				So(s.Status, ShouldEqual, interfaces.DIAGNOSIS_STATUS_OPEN)
			}
			So(list[0].Impact.Questions, ShouldNotBeEmpty)
		})

		Convey("Metrics count the network, the references, the questions and the concepts\n", func() {
			m := sampleJudge().metrics()
			So(m.Connectivity, ShouldResemble, interfaces.DiagnosisConnectivity{Largest: 3, Objects: 7, Components: 5})
			// Four references at the threshold, one an adopted relation, and one adopted relation the
			// scan did not see.
			So(m.References, ShouldResemble, interfaces.DiagnosisReferences{Covered: 2, Total: 5, NotCounted: 4})
			So(m.Reachability, ShouldResemble, interfaces.DiagnosisReachability{Reachable: 1, Total: 3})
			So(m.PendingConcepts, ShouldEqual, 2)
		})

		Convey("Ask AI's suggestions are kept only as far as the facts bear them out\n", func() {
			j := sampleJudge()
			refIndex := func(source, property, target string) int {
				for i, r := range j.refs {
					if r.Source == source && r.SourceProperty == property && r.Target == target {
						return i
					}
				}
				return -1
			}
			weak := refIndex("mrp_planorder", "rootdemandbillno", "work_order")
			out := &agentOutput{Suggestions: []agentSuggestion{
				{Kind: interfaces.DIAGNOSIS_KIND_CREATE_RELATION, Title: "MRP 计划订单 —根需求→ 物料需求预测", Reason: "根需求追到预测单。",
					Reference: index(refIndex("mrp_planorder", "rootdemandbillno", "forecast")), Name: "根需求",
					Description: "每张计划订单追到驱动它的预测单。", Questions: []string{"q2", "q9"}},
				// An object type no one drafted.
				{Kind: interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT, Title: "x", Reason: "x", ObjectTypeID: "invented"},
				// Below the threshold without a quote.
				{Kind: interfaces.DIAGNOSIS_KIND_CREATE_RELATION, Title: "弱", Reason: "弱", Reference: index(weak), Name: "工单"},
				// A quote that is no passage given.
				{Kind: interfaces.DIAGNOSIS_KIND_CREATE_RELATION, Title: "编", Reason: "编", Reference: index(weak), Name: "工单",
					Quotes: []interfaces.DiagnosisQuote{{DocumentID: "d1", Text: "文档里没有这句话"}}},
				{Kind: interfaces.DIAGNOSIS_KIND_WRITEBACK_OBJECT, Title: "监测任务建为决策跟踪对象", Reason: "需要写回。",
					ConceptID: "c1", Writeback: goodWriteback(), Questions: []string{"q1"}},
				{Kind: interfaces.DIAGNOSIS_KIND_CONCEPT_DISPOSITION, Title: "采购进度状态是取值", Reason: "是状态。",
					ConceptID: "c2", Disposition: interfaces.DIAGNOSIS_DISPOSITION_PROPERTY, OwnerObjectTypeID: "work_order"},
				// A subtype by its index, named by Ask AI.
				{Kind: interfaces.DIAGNOSIS_KIND_CREATE_RELATION, Title: "产品 是一种 物料", Reason: "产品是成品物料。",
					Subtype: index(0), Name: "对应物料"},
			}}
			list, ai := j.suggestions(out)
			So(ai, ShouldBeTrue)

			root := byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "—根需求→")
			So(root, ShouldNotBeNil)
			So(applyOf[interfaces.DiagnosisCreateRelation](root).Name, ShouldEqual, "根需求")
			// Impact is the server's, not Ask AI's.
			So(root.Impact.Questions, ShouldResemble, []string{"q2"})
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT, "x"), ShouldBeNil)
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "弱"), ShouldBeNil)
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "编"), ShouldBeNil)

			wb := byKindTitle(list, interfaces.DIAGNOSIS_KIND_WRITEBACK_OBJECT, "监测任务")
			So(wb, ShouldNotBeNil)
			So(applyOf[interfaces.DiagnosisWriteback](wb).TrackedObjectTypeID, ShouldEqual, "forecast")
			So(wb.Impact.Questions, ShouldResemble, []string{"q1"})
			So(wb.Evidence.Quotes, ShouldNotBeEmpty)

			disposition := byKindTitle(list, interfaces.DIAGNOSIS_KIND_CONCEPT_DISPOSITION, "采购进度状态")
			So(applyOf[interfaces.DiagnosisConceptDisposition](disposition), ShouldResemble, interfaces.DiagnosisConceptDisposition{
				ConceptID: "c2", Disposition: interfaces.DIAGNOSIS_DISPOSITION_PROPERTY, OwnerObjectTypeID: "work_order"})

			sub := byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "产品 是一种 物料")
			So(applyOf[interfaces.DiagnosisCreateRelation](sub).Cardinality, ShouldEqual, interfaces.DRAFT_CARDINALITY_ONE_TO_ONE)
			// The server still reports data quality.
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_DATA_QUALITY, "586/605"), ShouldNotBeNil)
		})

		Convey("A reference below the threshold holds with a requirement quote\n", func() {
			j := sampleJudge()
			weak := -1
			for i, r := range j.refs {
				if r.Target == "work_order" {
					weak = i
				}
			}
			list, _ := j.suggestions(&agentOutput{Suggestions: []agentSuggestion{{Kind: interfaces.DIAGNOSIS_KIND_CREATE_RELATION,
				Title: "引", Reason: "文档写明。", Reference: index(weak), Name: "根需求工单",
				Quotes: []interfaces.DiagnosisQuote{{DocumentID: "d1", Text: "根需求单据要能追到预测"}}}}})
			s := byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "引")
			So(s, ShouldNotBeNil)
			So(s.Evidence.Quotes, ShouldResemble, []interfaces.DiagnosisQuote{{DocumentID: "d1", Text: "根需求单据要能追到预测"}})
		})

		Convey("A write-back object follows the decision-tracking pattern\n", func() {
			j := sampleJudge()
			So(j.writebackHolds(goodWriteback()), ShouldBeTrue)

			copied := goodWriteback()
			// Copying the tracked object's attributes is refused.
			copied.Properties = append(copied.Properties, interfaces.DiagnosisWritebackProperty{Name: "status",
				DisplayName: "状态", Type: "string", Role: interfaces.WRITEBACK_ROLE_OUTCOME})
			So(j.writebackHolds(copied), ShouldBeFalse)

			noActor := goodWriteback()
			noActor.Properties = slicesWithout(noActor.Properties, "planner")
			noActor.Actions[0].Parameters = []string{"task_id"}
			So(j.writebackHolds(noActor), ShouldBeFalse)

			unknownParameter := goodWriteback()
			unknownParameter.Actions[1].Parameters = []string{"overdue"}
			So(j.writebackHolds(unknownParameter), ShouldBeFalse)

			untracked := goodWriteback()
			untracked.TrackedObjectTypeID = "purchase_request"
			So(j.writebackHolds(untracked), ShouldBeFalse)

			twoAdds := goodWriteback()
			twoAdds.Actions[1].Kind = interfaces.WRITEBACK_ACTION_ADD
			So(j.writebackHolds(twoAdds), ShouldBeFalse)
		})

		Convey("Counted facts Ask AI leaves out are still suggested; leaving objects out stays Ask AI's\n", func() {
			j := sampleJudge()
			root := -1
			for i, r := range j.refs {
				if r.Source == "mrp_planorder" && r.SourceProperty == "rootdemandbillno" && r.Target == "forecast" {
					root = i
				}
			}
			list, ai := j.suggestions(&agentOutput{Suggestions: []agentSuggestion{{Kind: interfaces.DIAGNOSIS_KIND_CREATE_RELATION,
				Title: "MRP 计划订单 —根需求→ 物料需求预测", Reason: "根需求追到预测单。", Reference: index(root), Name: "根需求"}}})
			So(ai, ShouldBeTrue)
			// Ask AI's wording wins over the server's for the same suggestion.
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "根需求单据"), ShouldBeNil)
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "—根需求→"), ShouldNotBeNil)
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "产品 —对应物料→ 物料"), ShouldNotBeNil)
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "生产线 —上级生产线→ 生产线"), ShouldNotBeNil)
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CHOOSE_IDENTITY, "采购申请"), ShouldNotBeNil)
			for _, s := range list {
				So(s.Kind, ShouldNotEqual, interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT)
			}
		})

		Convey("A result of which nothing holds falls back to the server's own\n", func() {
			list, ai := sampleJudge().suggestions(&agentOutput{Suggestions: []agentSuggestion{
				{Kind: interfaces.DIAGNOSIS_KIND_ADOPT_RELATION, Title: "x", Reason: "x", RelationTypeID: "invented"}}})
			So(ai, ShouldBeFalse)
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "根需求单据"), ShouldNotBeNil)
		})

		Convey("A suggestion a person dismissed is not made again\n", func() {
			first, _ := sampleJudge().suggestions(nil)
			root := byKindTitle(first, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "根需求单据")
			d := sampleDraft()
			d.Items.Decisions = append(d.Items.Decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_DIAGNOSIS,
				ItemID: root.ID, Action: interfaces.DRAFT_DECISION_DISMISS})
			j := newJudge(setting, sampleFacts(d), sampleQuestions(), sampleConcepts())
			again, _ := j.suggestions(nil)
			So(byKindTitle(again, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "根需求单据"), ShouldBeNil)
			// The same suggestion keeps its id across runs; a dismissed reference counts as handled.
			So(j.metrics().References.Covered, ShouldEqual, 3)
		})

		Convey("A field that already reaches a subtype or supertype of the target takes no second relation\n", func() {
			d := sampleDraft()
			d.Items.ObjectTypes = append(d.Items.ObjectTypes, object("sales_order", "销售订单", true, "id", prop("id", "销售订单ID"),
				prop("product_code", "产品编码")))
			d.Items.RelationTypes = append(d.Items.RelationTypes, &interfaces.DraftRelationType{ID: "sales_product", Name: "产品",
				Source: "sales_order", Target: "product", Adopted: true,
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "product_code", TargetProperty: "code"}}})
			facts := sampleFacts(d)
			facts.Components[1] = []string{"product", "sales_order"}
			facts.References = append(facts.References,
				// Through the relation to 产品, which is a kind of 物料.
				interfaces.DraftReference{Source: "sales_order", SourceProperty: "product_code", Target: "material",
					TargetProperty: "code", Counted: true, Rows: 800, Hits: 800, Rate: 1, Name: "产品"},
				// Through the relation to 物料, of which 产品 is a kind.
				interfaces.DraftReference{Source: "work_order", SourceProperty: "material", Target: "product",
					TargetProperty: "code", Counted: true, Rows: 605, Hits: 605, Rate: 1, Name: "物料"})
			j := newJudge(setting, facts, sampleQuestions(), sampleConcepts())
			list, _ := j.suggestions(nil)
			subtypes := 0
			for _, s := range list {
				if s.Kind != interfaces.DIAGNOSIS_KIND_CREATE_RELATION {
					continue
				}
				a := applyOf[interfaces.DiagnosisCreateRelation](s)
				So(a.Source == "sales_order" && a.Target == "material", ShouldBeFalse)
				So(a.Source == "work_order" && a.Target == "product", ShouldBeFalse)
				if a.Source == "product" && a.Target == "material" {
					subtypes++
					So(a.Cardinality, ShouldEqual, interfaces.DRAFT_CARDINALITY_ONE_TO_ONE)
				}
			}
			So(subtypes, ShouldEqual, 1)
			// Ask AI cannot bring it back either.
			index := -1
			for i, r := range j.refs {
				if r.Source == "sales_order" {
					index = i
				}
			}
			So(j.checked(agentSuggestion{Kind: interfaces.DIAGNOSIS_KIND_CREATE_RELATION, Title: "x", Reason: "x",
				Reference: &index, Name: "产品"}), ShouldBeNil)

			// A subtype the draft already joins is not suggested again.
			d.Items.RelationTypes = append(d.Items.RelationTypes, &interfaces.DraftRelationType{ID: "product_material",
				Name: "对应物料", Source: "product", Target: "material", Adopted: true,
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "code", TargetProperty: "code"}}})
			list, _ = newJudge(setting, facts, sampleQuestions(), sampleConcepts()).suggestions(nil)
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "对应物料"), ShouldBeNil)
		})

		Convey("An object type whose references are not all counted is never offered to leave out\n", func() {
			facts := sampleFacts(sampleDraft())
			facts.Uncounted = []string{"sample_batch"}
			j := newJudge(setting, facts, sampleQuestions(), sampleConcepts())
			list, _ := j.suggestions(nil)
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT, "样本批次"), ShouldBeNil)
			So(j.checked(agentSuggestion{Kind: interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT, Title: "x", Reason: "x",
				ObjectTypeID: "sample_batch"}), ShouldBeNil)

			// Nor one a reference reaches, counted or only named.
			facts = sampleFacts(sampleDraft())
			facts.References = append(facts.References, interfaces.DraftReference{Source: "sample_batch",
				SourceProperty: "batch_id", Target: "line", TargetProperty: "code", Name: "生产线"})
			list, _ = newJudge(setting, facts, sampleQuestions(), sampleConcepts()).suggestions(nil)
			So(byKindTitle(list, interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT, "样本批次"), ShouldBeNil)
		})

		Convey("The message to Ask AI carries the facts and no row data\n", func() {
			in := sampleJudge().agentInput()
			So(in.Kind, ShouldEqual, "diagnose")
			So(len(in.ObjectTypes), ShouldEqual, 8)
			So(len(in.References), ShouldEqual, 5)
			So(in.References[0].Counted, ShouldBeTrue)
			So(len(in.UnreachableQuestions), ShouldEqual, 2)
			So(in.Subtypes[0].Sub, ShouldEqual, "product")
			raw, _ := json.Marshal(agentFormat())
			So(string(raw), ShouldContainSubstring, `"maxItems":15`)
		})
	})
}

func slicesWithout(props []interfaces.DiagnosisWritebackProperty, name string) []interfaces.DiagnosisWritebackProperty {
	out := []interfaces.DiagnosisWritebackProperty{}
	for _, p := range props {
		if p.Name != name {
			out = append(out, p)
		}
	}
	return out
}

// memoryAccess keeps diagnoses in memory as the table does.
type memoryAccess struct {
	rows map[string]interfaces.Diagnosis
}

func (m *memoryAccess) GetDiagnosis(_ context.Context, projectID string) (*interfaces.Diagnosis, bool, error) {
	d, ok := m.rows[projectID]
	if !ok {
		return nil, false, nil
	}
	raw, _ := json.Marshal(d.Suggestions)
	copied := d
	copied.Suggestions = nil
	_ = json.Unmarshal(raw, &copied.Suggestions)
	return &copied, true, nil
}

func (m *memoryAccess) StartDiagnosis(_ context.Context, d *interfaces.Diagnosis, staleBefore int64) (bool, error) {
	if old, ok := m.rows[d.ProjectID]; ok && old.State == interfaces.DIAGNOSIS_STATE_RUNNING && old.StartTime >= staleBefore {
		return false, nil
	}
	m.rows[d.ProjectID] = *d
	return true, nil
}

func (m *memoryAccess) SaveDiagnosis(_ context.Context, d *interfaces.Diagnosis) (bool, error) {
	if old, ok := m.rows[d.ProjectID]; !ok || old.RunID != d.RunID {
		return false, nil
	}
	m.rows[d.ProjectID] = *d
	return true, nil
}

type writebackHook struct {
	specs []projecthooks.WritebackSpec
}

func (w *writebackHook) Create(_ context.Context, _ string, _ string, spec projecthooks.WritebackSpec,
	_ interfaces.AccountInfo) (string, error) {
	w.specs = append(w.specs, spec)
	return "monitoring_task", nil
}

func httpCode(err error) (int, string) {
	if e, ok := err.(*httperrors.HTTPError); ok {
		return e.HTTPCode, e.BaseError.ErrorCode
	}
	return 0, ""
}

// The diagnose pass gets the Ontology Assistant's whole limit (timeout_s 180, migration 0.2.2/21),
// unlike an answer on a page; a run is stale only after the scan and that wait.
func Test_AgentWait(t *testing.T) {
	Convey("The server waits for the diagnose pass as long as the Agent may take\n", t, func() {
		So(agentWait, ShouldBeGreaterThanOrEqualTo, 180*time.Second)
		s := &diagnosisService{setting: common.DraftSetting{DiagnosisTimeout: 2 * time.Minute}, agentWait: agentWait,
			now: func() time.Time { return time.UnixMilli(10_000_000) }}
		So(s.staleBefore(), ShouldEqual, time.UnixMilli(10_000_000).Add(-(2*time.Minute + agentWait + time.Minute)).UnixMilli())
	})
}

func Test_DiagnosisService(t *testing.T) {
	Convey("Test the network diagnosis service\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		ps := pmock.NewMockProjectService(mockCtrl)
		drs := pmock.NewMockDraftService(mockCtrl)
		rqs := pmock.NewMockRequirementService(mockCtrl)
		rqa := pmock.NewMockRequirementAccess(mockCtrl)
		dga := &memoryAccess{rows: map[string]interfaces.Diagnosis{}}
		now := time.UnixMilli(1_000_000)
		s := &diagnosisService{setting: setting, dga: dga, drs: drs, rqs: rqs, rqa: rqa, ps: ps,
			run: func(fn func()) { fn() }, agentWait: time.Second, now: func() time.Time { return now }}
		ctx := context.Background()
		owner := interfaces.AccountInfo{ID: "u1", Type: "user"}
		entry := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: owner}}
		ps.EXPECT().GetProject(gomock.Any(), "t", "p1").AnyTimes().Return(entry, nil)

		ready := func() {
			d := sampleDraft()
			drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Return(d, nil)
			rqs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(sampleQuestions(), nil)
			rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)
			drs.EXPECT().NetworkFacts(gomock.Any(), "t", "p1", owner).Return(sampleFacts(d), nil)
		}

		Convey("Only the owner runs a diagnosis\n", func() {
			_, err := s.Run(ctx, "t", "p1", interfaces.AccountInfo{ID: "u2", Type: "user"})
			code, name := httpCode(err)
			So(code, ShouldEqual, http.StatusForbidden)
			So(name, ShouldEqual, perrors.BknProject_Diagnosis_NotOwner)
		})

		Convey("A draft not ready is not diagnosed\n", func() {
			d := sampleDraft()
			d.Status = interfaces.DRAFT_STATUS_DRAFTING
			drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Return(d, nil)
			_, err := s.Run(ctx, "t", "p1", owner)
			code, name := httpCode(err)
			So(code, ShouldEqual, http.StatusConflict)
			So(name, ShouldEqual, perrors.BknProject_Draft_NotReady)
		})

		Convey("None run yet is not found\n", func() {
			_, err := s.Get(ctx, "t", "p1")
			code, name := httpCode(err)
			So(code, ShouldEqual, http.StatusNotFound)
			So(name, ShouldEqual, perrors.BknProject_Diagnosis_NotFound)
		})

		Convey("A run while another runs is refused; one left running past its time is taken over\n", func() {
			dga.rows["p1"] = interfaces.Diagnosis{ProjectID: "p1", State: interfaces.DIAGNOSIS_STATE_RUNNING, RunID: "r0",
				StartTime: now.UnixMilli()}
			drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Return(sampleDraft(), nil)
			rqs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(sampleQuestions(), nil)
			rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)
			_, err := s.Run(ctx, "t", "p1", owner)
			code, name := httpCode(err)
			So(code, ShouldEqual, http.StatusConflict)
			So(name, ShouldEqual, perrors.BknProject_Diagnosis_Running)

			// A running diagnosis is shown without reading the draft.
			got, err := s.Get(ctx, "t", "p1")
			So(err, ShouldBeNil)
			So(got.State, ShouldEqual, interfaces.DIAGNOSIS_STATE_RUNNING)

			now = now.Add(time.Hour)
			got, _ = s.Get(ctx, "t", "p1")
			So(got.State, ShouldEqual, interfaces.DIAGNOSIS_STATE_FAILED)
			So(got.Error, ShouldEqual, interfaces.DIAGNOSIS_ERROR_INTERRUPTED)
			ready()
			run, err := s.Run(ctx, "t", "p1", owner)
			So(err, ShouldBeNil)
			So(run.RunID, ShouldNotEqual, "r0")
		})

		Convey("A run without Ask AI records the server's suggestions\n", func() {
			ready()
			run, err := s.Run(ctx, "t", "p1", owner)
			So(err, ShouldBeNil)
			So(run.State, ShouldEqual, interfaces.DIAGNOSIS_STATE_RUNNING)

			drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Return(sampleDraft(), nil)
			got, err := s.Get(ctx, "t", "p1")
			So(err, ShouldBeNil)
			So(got.State, ShouldEqual, interfaces.DIAGNOSIS_STATE_READY)
			So(got.RunID, ShouldEqual, run.RunID)
			So(got.AI, ShouldBeFalse)
			So(got.Stale, ShouldBeFalse)
			So(got.DraftUpdateTime, ShouldEqual, 100)
			So(got.Metrics.Connectivity.Largest, ShouldEqual, 3)
			So(got.Suggestions, ShouldNotBeEmpty)

			Convey("A change made elsewhere makes it stale\n", func() {
				changed := sampleDraft()
				changed.UpdateTime = 105
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Return(changed, nil)
				got, _ := s.Get(ctx, "t", "p1")
				So(got.Stale, ShouldBeTrue)
			})
		})

		Convey("A run asks Ask AI and keeps what holds\n", func() {
			ba := pmock.NewMockBknAgentAccess(mockCtrl)
			s.ba = ba
			ready()
			ba.EXPECT().Chat(gomock.Any(), owner, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ interfaces.AccountInfo, req *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					So(req.AgentID, ShouldEqual, "bkn-project-ontology-ask")
					So(req.Message, ShouldContainSubstring, `"kind":"diagnose"`)
					on(interfaces.BknAgentChatEvent{Name: interfaces.BKN_AGENT_CHAT_STRUCTURED,
						Data: json.RawMessage(`{"content":{"suggestions":[{"kind":"exclude_object","title":"样本批次不纳入",` +
							`"reason":"它是样例数据的元信息。","object_type_id":"sample_batch"}]}}`)})
					return nil
				})
			_, err := s.Run(ctx, "t", "p1", owner)
			So(err, ShouldBeNil)
			saved := dga.rows["p1"]
			So(saved.AI, ShouldBeTrue)
			excluded := byKindTitle(saved.Suggestions, interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT, "样本批次不纳入")
			So(excluded, ShouldNotBeNil)
			So(excluded.Reason, ShouldEqual, "它是样例数据的元信息。")
			// The counted references Ask AI left out are still there.
			So(byKindTitle(saved.Suggestions, interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "mrp_planorder"), ShouldNotBeNil)
		})

		Convey("Applying a suggestion\n", func() {
			ready()
			_, _ = s.Run(ctx, "t", "p1", owner)
			find := func(kind string, title string) *interfaces.DiagnosisSuggestion {
				return byKindTitle(dga.rows["p1"].Suggestions, kind, title)
			}

			Convey("adopts the relation, and keeps the diagnosis current\n", func() {
				adopt := find(interfaces.DIAGNOSIS_KIND_ADOPT_RELATION, "产品线")
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Times(2).Return(sampleDraft(), nil)
				drs.EXPECT().EditDraftItem(gomock.Any(), "t", "p1", interfaces.DRAFT_KIND_RELATION_TYPES, "product_line",
					gomock.Any(), owner).DoAndReturn(func(_ context.Context, _, _, _, _ string, edit interfaces.DraftItemEdit,
					_ interfaces.AccountInfo) (*interfaces.Draft, error) {
					So(edit.UpdateTime, ShouldEqual, 100)
					So(*edit.Adopted, ShouldBeTrue)
					d := sampleDraft()
					d.UpdateTime = 101
					projecthooks.FindRelationType(&d.Items, "product_line").Adopted = true
					return d, nil
				})

				applied, err := s.Apply(ctx, "t", "p1", adopt.ID, interfaces.DiagnosisApply{DraftUpdateTime: 100}, owner)

				So(err, ShouldBeNil)
				So(applied.DraftUpdateTime, ShouldEqual, 101)
				So(applied.Suggestion.Status, ShouldEqual, interfaces.DIAGNOSIS_STATUS_APPLIED)
				saved := dga.rows["p1"]
				So(saved.DraftUpdateTime, ShouldEqual, 101)
				So(saved.Components[1], ShouldResemble, []string{"product", "line"})
				So(saved.Metrics.Connectivity.Components, ShouldEqual, 4)

				// The change the diagnosis made itself does not make it stale.
				after := sampleDraft()
				after.UpdateTime = 101
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Return(after, nil)
				got, _ := s.Get(ctx, "t", "p1")
				So(got.Stale, ShouldBeFalse)
				So(byKindTitle(got.Suggestions, interfaces.DIAGNOSIS_KIND_ADOPT_RELATION, "产品线").Status, ShouldEqual,
					interfaces.DIAGNOSIS_STATUS_APPLIED)

				// An applied suggestion is not applied again.
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Return(after, nil)
				_, err = s.Apply(ctx, "t", "p1", adopt.ID, interfaces.DiagnosisApply{DraftUpdateTime: 101}, owner)
				code, name := httpCode(err)
				So(code, ShouldEqual, http.StatusConflict)
				So(name, ShouldEqual, perrors.BknProject_Diagnosis_SuggestionStale)
			})

			Convey("on a diagnosis already stale keeps it stale\n", func() {
				create := find(interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "根需求单据")
				changed := sampleDraft()
				changed.UpdateTime = 105
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Times(2).Return(changed, nil)
				drs.EXPECT().CreateRelationType(gomock.Any(), "t", "p1", gomock.Any(), owner).DoAndReturn(
					func(_ context.Context, _, _ string, save interfaces.DraftRelationTypeSave, _ interfaces.AccountInfo) (*interfaces.Draft, error) {
						So(save.UpdateTime, ShouldEqual, 105)
						So(save.Name, ShouldEqual, "根需求单据")
						So(save.SourceObjectTypeID, ShouldEqual, "mrp_planorder")
						So(save.Type, ShouldEqual, interfaces.DRAFT_RELATION_DIRECT)
						So(string(save.MappingRules), ShouldEqual,
							`[{"source_property":{"name":"rootdemandbillno"},"target_property":{"name":"billno"}}]`)
						d := sampleDraft()
						d.UpdateTime = 106
						return d, nil
					})
				_, err := s.Apply(ctx, "t", "p1", create.ID, interfaces.DiagnosisApply{DraftUpdateTime: 105}, owner)
				So(err, ShouldBeNil)
				So(dga.rows["p1"].DraftUpdateTime, ShouldEqual, 100)
			})

			Convey("on a draft read before a change is stale\n", func() {
				create := find(interfaces.DIAGNOSIS_KIND_CREATE_RELATION, "根需求单据")
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Times(2).Return(sampleDraft(), nil)
				_, err := s.Apply(ctx, "t", "p1", create.ID, interfaces.DiagnosisApply{DraftUpdateTime: 99}, owner)
				code, name := httpCode(err)
				So(code, ShouldEqual, http.StatusConflict)
				So(name, ShouldEqual, perrors.BknProject_Draft_Stale)
			})

			Convey("that no longer holds is refused\n", func() {
				adopt := find(interfaces.DIAGNOSIS_KIND_ADOPT_RELATION, "产品线")
				d := sampleDraft()
				projecthooks.FindRelationType(&d.Items, "product_line").Adopted = true
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Times(2).Return(d, nil)
				_, err := s.Apply(ctx, "t", "p1", adopt.ID, interfaces.DiagnosisApply{DraftUpdateTime: 100}, owner)
				code, name := httpCode(err)
				So(code, ShouldEqual, http.StatusConflict)
				So(name, ShouldEqual, perrors.BknProject_Diagnosis_SuggestionStale)
			})

			Convey("that opens the identity chooser, or has no operation, is not applicable\n", func() {
				for _, sg := range []*interfaces.DiagnosisSuggestion{find(interfaces.DIAGNOSIS_KIND_CHOOSE_IDENTITY, "采购申请"),
					find(interfaces.DIAGNOSIS_KIND_DATA_QUALITY, "586")} {
					drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Return(sampleDraft(), nil)
					_, err := s.Apply(ctx, "t", "p1", sg.ID, interfaces.DiagnosisApply{DraftUpdateTime: 100}, owner)
					code, name := httpCode(err)
					So(code, ShouldEqual, http.StatusBadRequest)
					So(name, ShouldEqual, perrors.BknProject_Diagnosis_NotApplicable)
				}
			})

			Convey("leaves an object type out with the reason\n", func() {
				exclude := find(interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT, "样本批次")
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Times(2).Return(sampleDraft(), nil)
				drs.EXPECT().EditDraftItem(gomock.Any(), "t", "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "sample_batch",
					gomock.Any(), owner).DoAndReturn(func(_ context.Context, _, _, _, _ string, edit interfaces.DraftItemEdit,
					_ interfaces.AccountInfo) (*interfaces.Draft, error) {
					So(*edit.Adopted, ShouldBeFalse)
					So(edit.Reason, ShouldEqual, exclude.Reason)
					d := sampleDraft()
					d.UpdateTime = 101
					projecthooks.FindObjectType(&d.Items, "sample_batch").Adopted = false
					return d, nil
				})
				_, err := s.Apply(ctx, "t", "p1", exclude.ID, interfaces.DiagnosisApply{DraftUpdateTime: 100}, owner)
				So(err, ShouldBeNil)
				So(dga.rows["p1"].Metrics.Connectivity.Objects, ShouldEqual, 6)
			})

			Convey("A dismissal is recorded on the draft and keeps the diagnosis current\n", func() {
				quality := find(interfaces.DIAGNOSIS_KIND_DATA_QUALITY, "586")
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Times(2).Return(sampleDraft(), nil)
				after := sampleDraft()
				after.UpdateTime = 102
				drs.EXPECT().DismissSuggestion(gomock.Any(), "t", "p1", quality.ID, "已知", owner).Return(after, nil)
				dismissed, err := s.Dismiss(ctx, "t", "p1", quality.ID, interfaces.DiagnosisDismiss{Reason: "已知"}, owner)
				So(err, ShouldBeNil)
				So(dismissed.Status, ShouldEqual, interfaces.DIAGNOSIS_STATUS_DISMISSED)
				So(dga.rows["p1"].DraftUpdateTime, ShouldEqual, 102)
			})
		})

		Convey("A write-back object goes through the edition's hook, and without one is not applicable\n", func() {
			d := sampleDraft()
			wb := newSuggestion(interfaces.DIAGNOSIS_KIND_WRITEBACK_OBJECT, "wb1", "监测任务", "需要写回。", *goodWriteback())
			disposition := newSuggestion(interfaces.DIAGNOSIS_KIND_CONCEPT_DISPOSITION, "cd1", "采购进度状态是取值", "是状态。",
				interfaces.DiagnosisConceptDisposition{ConceptID: "c2", Disposition: interfaces.DIAGNOSIS_DISPOSITION_PROPERTY})
			dga.rows["p1"] = interfaces.Diagnosis{ProjectID: "p1", State: interfaces.DIAGNOSIS_STATE_READY, RunID: "r1",
				DraftUpdateTime: 100, Components: sampleFacts(d).Components,
				Suggestions: []*interfaces.DiagnosisSuggestion{wb, disposition}}

			restore := projecthooks.SetForTest(projecthooks.Hooks{})
			drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Return(sampleDraft(), nil)
			_, err := s.Apply(ctx, "t", "p1", "wb1", interfaces.DiagnosisApply{DraftUpdateTime: 100}, owner)
			code, name := httpCode(err)
			So(code, ShouldEqual, http.StatusBadRequest)
			So(name, ShouldEqual, perrors.BknProject_Diagnosis_NotApplicable)
			restore()

			hook := &writebackHook{}
			defer projecthooks.SetForTest(projecthooks.Hooks{WritebackObjects: hook})()
			after := sampleDraft()
			after.UpdateTime = 103
			gomock.InOrder(
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Times(2).Return(sampleDraft(), nil),
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Return(after, nil),
			)
			applied, err := s.Apply(ctx, "t", "p1", "wb1", interfaces.DiagnosisApply{DraftUpdateTime: 100}, owner)
			So(err, ShouldBeNil)
			So(applied.DraftUpdateTime, ShouldEqual, 103)
			So(hook.specs, ShouldHaveLength, 1)
			So(hook.specs[0].TrackedObjectTypeID, ShouldEqual, "forecast")

			Convey("A concept's disposition is recorded on the requirement specification\n", func() {
				drs.EXPECT().GetDraft(gomock.Any(), "t", "p1").Times(2).Return(after, nil)
				rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{UpdateTime: 55,
					Spec: interfaces.RequirementSpec{Concepts: []*interfaces.RequirementConcept{{ID: "c2", Category: "object"}}}}, true, nil)
				rqs.EXPECT().EditItem(gomock.Any(), "t", "p1", interfaces.REQUIREMENT_KIND_CONCEPTS, "c2", gomock.Any(), owner).
					DoAndReturn(func(_ context.Context, _, _, _, _ string, edit interfaces.RequirementItemEdit,
						_ interfaces.AccountInfo) (*interfaces.Requirement, error) {
						So(edit.UpdateTime, ShouldEqual, 55)
						So(*edit.Category, ShouldEqual, "property")
						return &interfaces.Requirement{}, nil
					})
				applied, err := s.Apply(ctx, "t", "p1", "cd1", interfaces.DiagnosisApply{}, owner)
				So(err, ShouldBeNil)
				So(applied.DraftUpdateTime, ShouldEqual, 103)
			})
		})
	})
}
