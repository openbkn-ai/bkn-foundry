// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"errors"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_RecordTests(t *testing.T) {
	Convey("Test filling scenario questions with a record from the data\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &requirementService{ra: ra, da: da, va: va, now: func() int64 { return 0 }}
		ctx := context.Background()
		draft := guidanceDraft()
		draft.Items.ObjectTypes[1].Table = interfaces.DraftTable{ResourceID: "r-cust"}
		draft.Items.ObjectTypes[1].PrimaryKeys = []string{"customer_id"}
		// q2 asks about one customer; q1 has no input and is asked as it is.
		req := readySpec()
		req.Spec.Concepts[1].MapsTo = &customerRef
		req.Spec.Questions[1].Text, req.Spec.Questions[1].Template = "Orders of a customer?", "Orders of customer {customer}?"
		req.Spec.Questions[1].Inputs = &[]interfaces.RequirementInput{{Name: "customer", ConceptID: "c2", Property: "customer_id",
			Role: interfaces.REQUIREMENT_INPUT_RECORD}}
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(req, true, nil).AnyTimes()
		sample := func(rows ...map[string]any) {
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return(rows, nil)
		}

		Convey("A question about a record is asked of the first record the data has, checked as the caller\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			sample(map[string]any{"v0": "10001"})
			va.EXPECT().Query(gomock.Any(), caller, "SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'",
				checkQueryTimeout).Return([]map[string]any{{"n": float64(1)}}, nil)

			tests, err := svc.RecordTests(ctx, caller, "p1", []string{"q1", "q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldResemble, map[string]*interfaces.ScenarioTest{"q2": {ID: recordTestID,
				Text: "Orders of customer 10001?", Expect: interfaces.TEST_EXPECT_EXISTS}})
		})

		Convey("Without a record to fill it with, the question has none to be asked of\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			sample()
			tests, err := svc.RecordTests(ctx, caller, "p1", []string{"q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldResemble, map[string]*interfaces.ScenarioTest{"q2": nil})

		})

		Convey("Data that could not be read says nothing of its records: the question is left out\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return(nil,
				&interfaces.VegaError{HTTPCode: http.StatusInternalServerError})
			tests, err := svc.RecordTests(ctx, caller, "p1", []string{"q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldBeEmpty)

			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return(nil, errors.New("down"))
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			tests, err = svc.RecordTests(ctx, caller, "p1", []string{"q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldBeEmpty)

			// Nor without a draft, whose fields hold the values.
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			tests, err = svc.RecordTests(ctx, caller, "p1", []string{"q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldBeEmpty)
		})

		Convey("Only a record the data lacks is no record\n", func() {
			So(noRecord(interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkRecordNotFound}), ShouldBeTrue)
			So(noRecord(notChecked(checkNoValueInData)), ShouldBeTrue)
			for _, reason := range []string{"query_forbidden", "query_failed", checkTimeout, "table_missing", checkDataNotBuilt, checkValueOutsideSlot} {
				So(noRecord(notChecked(reason)), ShouldBeFalse)
			}
		})

		Convey("Only the questions named are filled, and none is read for no question\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			tests, err := svc.RecordTests(ctx, caller, "p1", []string{"q1"})
			So(err, ShouldBeNil)
			So(tests, ShouldBeEmpty)
			tests, err = svc.RecordTests(ctx, caller, "p1", nil)
			So(err, ShouldBeNil)
			So(tests, ShouldBeEmpty)
		})

		Convey("Out of the run's time, the questions left are asked as written: no record is read for them\n", func() {
			req.Spec.Questions[0].Needs = []interfaces.RequirementItemRef{customerRef}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			calls := 0
			late := &requirementService{ra: ra, da: da, va: va, now: func() int64 {
				calls++
				if calls == 1 {
					return 0
				}
				return recordTestsTimeout.Milliseconds()
			}}
			tests, err := late.RecordTests(ctx, caller, "p1", []string{"q1", "q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldBeEmpty)
		})

		Convey("A question with no template is asked of a record of the first object type it needs, said after it\n", func() {
			req.Spec.Questions[0].Needs = []interfaces.RequirementItemRef{customerRef}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			sample(map[string]any{"v0": "10001"})
			tests, err := svc.RecordTests(ctx, caller, "p1", []string{"q1"})
			So(err, ShouldBeNil)
			So(tests["q1"], ShouldNotBeNil)
			So(tests["q1"].Text, ShouldStartWith, "Which orders?")
			So(tests["q1"].Text, ShouldContainSubstring, "Customer ID")
			So(tests["q1"].Text, ShouldContainSubstring, "10001")
			So(tests["q1"].Expect, ShouldEqual, interfaces.TEST_EXPECT_EXISTS)

			// The data has no value of it: the question has no record to be asked of.
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			sample()
			tests, err = svc.RecordTests(ctx, caller, "p1", []string{"q1"})
			So(err, ShouldBeNil)
			So(tests, ShouldResemble, map[string]*interfaces.ScenarioTest{"q1": nil})

			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			ra.EXPECT().ListGuidance(gomock.Any(), "p1").Return(nil, nil)
			sq, err := svc.ScenarioQuestions(ctx, "p1")
			So(err, ShouldBeNil)
			So(sq.Questions[0].Fillable, ShouldBeTrue)
		})

		Convey("A document object's records are read by the edition, not by the data's SQL\n", func() {
			draft.Items.ObjectTypes[1].Document = &interfaces.DraftDocumentModel{Role: "entity"}
			sampler := &recordSampler{rows: []map[string]any{{"customer_id": ""}, {"customer_id": "C-7"}}}
			defer projecthooks.SetForTest(projecthooks.Hooks{Drafts: sampler})()
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			tests, err := svc.RecordTests(ctx, caller, "p1", []string{"q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldResemble, map[string]*interfaces.ScenarioTest{"q2": {ID: recordTestID,
				Text: "Orders of customer C-7?", Expect: interfaces.TEST_EXPECT_EXISTS}})
			So(sampler.objects, ShouldResemble, []string{"customer"})

			// Nothing built is no record; a read that failed leaves the question out.
			sampler.rows = nil
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			tests, err = svc.RecordTests(ctx, caller, "p1", []string{"q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldResemble, map[string]*interfaces.ScenarioTest{"q2": nil})
			sampler.err = errors.New("down")
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			tests, err = svc.RecordTests(ctx, caller, "p1", []string{"q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldBeEmpty)
		})

		Convey("Values of several object types are filled from the one record that names them all\n", func() {
			// q2 names a line by its order and its customer: the line holds both, by the relations
			// it is matched to them by; neither the order nor the customer holds the other.
			draft.Items.ObjectTypes[0].Document = &interfaces.DraftDocumentModel{Role: "entity"}
			draft.Items.ObjectTypes[0].Properties = []*interfaces.DraftProperty{{Name: "order_no", Type: "string", MappedField: "order_no"}}
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, &interfaces.DraftObjectType{ID: "order_line", Adopted: true,
				Document: &interfaces.DraftDocumentModel{Role: "in_document"}, Properties: []*interfaces.DraftProperty{
					{Name: "order_ref", Type: "string"}, {Name: "cust_ref", Type: "string"}}})
			linking := func(id, target, mention, identity string) *interfaces.DraftRelationType {
				return &interfaces.DraftRelationType{ID: id, Source: "order_line", Target: target, Adopted: true,
					DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: mention, IdentityProperty: identity}}
			}
			draft.Items.RelationTypes = append(draft.Items.RelationTypes, linking("line_order", "sales_order", "order_ref", "order_no"),
				linking("line_customer", "customer", "cust_ref", "customer_id"))
			req.Spec.Concepts[0].MapsTo = &orderRef
			req.Spec.Questions[1].Template = "Line of order {order} for customer {customer}?"
			req.Spec.Questions[1].Inputs = &[]interfaces.RequirementInput{
				{Name: "order", ConceptID: "c1", Property: "order_no", Role: interfaces.REQUIREMENT_INPUT_RECORD},
				{Name: "customer", ConceptID: "c2", Property: "customer_id", Role: interfaces.REQUIREMENT_INPUT_RECORD}}
			sampler := &recordSampler{rows: []map[string]any{{"order_ref": "SO-1"}, {"order_ref": "SO-2", "cust_ref": "C-7"}}}
			defer projecthooks.SetForTest(projecthooks.Hooks{Drafts: sampler})()
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)

			tests, err := svc.RecordTests(ctx, caller, "p1", []string{"q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldResemble, map[string]*interfaces.ScenarioTest{"q2": {ID: recordTestID,
				Text: "Line of order SO-2 for customer C-7?", Expect: interfaces.TEST_EXPECT_EXISTS}})
			So(sampler.objects, ShouldResemble, []string{"order_line"})
		})

		Convey("Scenario questions say which the server can fill\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			ra.EXPECT().ListGuidance(gomock.Any(), "p1").Return(nil, nil)
			sq, err := svc.ScenarioQuestions(ctx, "p1")
			So(err, ShouldBeNil)
			So(sq.Questions[0].Fillable, ShouldBeFalse)
			So(sq.Questions[1].Fillable, ShouldBeTrue)
		})
	})
}

// recordSampler reads records of document object types, as an edition does.
type recordSampler struct {
	projecthooks.Drafts
	rows    []map[string]any
	err     error
	objects []string
}

func (r *recordSampler) SampleRecords(_ context.Context, _ interfaces.AccountInfo, _ *interfaces.Draft, objectTypeID string, _ int) ([]map[string]any, error) {
	r.objects = append(r.objects, objectTypeID)
	return r.rows, r.err
}

func Test_RecordCarrier(t *testing.T) {
	Convey("Test which record holds the values of several object types\n", t, func() {
		order := &interfaces.DraftObjectType{ID: "order", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "entity"},
			Properties: []*interfaces.DraftProperty{{Name: "no"}, {Name: "customer"}}}
		customer := &interfaces.DraftObjectType{ID: "customer", Adopted: true, Properties: []*interfaces.DraftProperty{{Name: "id"}}}
		draft := &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{order, customer},
			RelationTypes: []*interfaces.DraftRelationType{{ID: "order_customer", Source: "customer", Target: "order", Adopted: true,
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "id", TargetProperty: "customer"}}}}}}
		slots := []*slotField{{objectType: order, field: "no"}, {objectType: customer, field: "id"}}

		Convey("A record holds its own value and, by a join either way, the value it refers to\n", func() {
			carrier, held := recordCarrier(draft, slots)
			So(carrier, ShouldEqual, order)
			So(held, ShouldResemble, []string{"no", "customer"})
		})
		Convey("A relation not adopted names nothing\n", func() {
			draft.Items.RelationTypes[0].Adopted = false
			carrier, _ := recordCarrier(draft, slots)
			So(carrier, ShouldBeNil)
		})
		Convey("Two object types that could hold them both: none is chosen\n", func() {
			other := &interfaces.DraftObjectType{ID: "return", Adopted: true, Properties: []*interfaces.DraftProperty{{Name: "order"}, {Name: "cust"}}}
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, other)
			draft.Items.RelationTypes = append(draft.Items.RelationTypes,
				&interfaces.DraftRelationType{ID: "r1", Source: "return", Target: "order", Adopted: true,
					DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "order", IdentityProperty: "no"}},
				&interfaces.DraftRelationType{ID: "r2", Source: "return", Target: "customer", Adopted: true,
					Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "cust", TargetProperty: "id"}}})
			carrier, _ := recordCarrier(draft, slots)
			So(carrier, ShouldBeNil)
		})
		Convey("Two relations to one object type that match it from different fields: which is meant is not said\n", func() {
			order.Properties = append(order.Properties, &interfaces.DraftProperty{Name: "payer"})
			draft.Items.RelationTypes = append(draft.Items.RelationTypes, &interfaces.DraftRelationType{ID: "order_payer",
				Source: "order", Target: "customer", Adopted: true, Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "payer", TargetProperty: "id"}}})
			carrier, _ := recordCarrier(draft, slots)
			So(carrier, ShouldBeNil)
		})
		Convey("Slots only of tables are left to the data's SQL\n", func() {
			order.Document = nil
			carrier, _ := recordCarrier(draft, slots)
			So(carrier, ShouldBeNil)
		})
		Convey("Slots of one object type are its own record\n", func() {
			carrier, held := recordCarrier(draft, []*slotField{{objectType: customer, field: "id"}})
			So(carrier, ShouldEqual, customer)
			So(held, ShouldResemble, []string{"id"})
		})
	})
}
