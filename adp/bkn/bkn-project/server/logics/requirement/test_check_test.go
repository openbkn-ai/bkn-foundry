// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_CheckTests(t *testing.T) {
	Convey("Test checking test questions against the data\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &requirementService{va: va}
		ctx := context.Background()
		spec := readySpec().Spec
		spec.Concepts[1].MapsTo = &customerRef
		draft := guidanceDraft()
		draft.Items.ObjectTypes[1].Table = interfaces.DraftTable{ResourceID: "r-cust"}
		draft.Items.ObjectTypes[1].PrimaryKeys = []string{"customer_id"}
		slot := func(property, value string) interfaces.QuestionTestSlot {
			return interfaces.QuestionTestSlot{ConceptID: "c2", Property: property, Value: value}
		}
		test := func(expect string, slots ...interfaces.QuestionTestSlot) *interfaces.QuestionTest {
			return &interfaces.QuestionTest{ID: "t1", Text: "x", Expect: expect, Slots: slots}
		}
		count := func(sql string, n any) {
			va.EXPECT().Query(gomock.Any(), caller, sql, checkQueryTimeout).Return([]map[string]any{{"n": n}}, nil)
		}

		Convey("A record expected to exist passes when every value is in its field, counted as the caller\n", func() {
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "10001"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED})

			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '99999'", "0")
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "99999"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkRecordNotFound})
		})

		Convey("A record expected not to exist passes only when no value is found\n", func() {
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '99999'", float64(0))
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("customer_id", "99999"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED})

			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(2))
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("customer_id", "10001"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkRecordExists})
		})

		Convey("Two ways of naming one record must find that one record\n", func() {
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = 'Acme Ltd'", float64(1))
			count("SELECT COUNT(DISTINCT `cust_id`) AS n FROM {{r-cust}} WHERE `cust_id` = '10001' OR `name` = 'Acme Ltd'", float64(1))
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_SAME_RECORD,
				slot("customer_id", "10001"), slot("full_name", "Acme Ltd"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED})

			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = 'Other Co'", float64(1))
			count("SELECT COUNT(DISTINCT `cust_id`) AS n FROM {{r-cust}} WHERE `cust_id` = '10001' OR `name` = 'Other Co'", float64(2))
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_SAME_RECORD,
				slot("customer_id", "10001"), slot("full_name", "Other Co"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkDifferentRecords})
		})

		Convey("A value is written as a literal, quotes and backslashes escaped\n", func() {
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = 'O''Brien \\\\ Sons'", float64(1))
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("full_name", `O'Brien \ Sons`))).Outcome,
				ShouldEqual, interfaces.TEST_CHECK_PASSED)
		})

		Convey("What cannot be checked is not passed, and says why\n", func() {
			unmapped := interfaces.QuestionTestSlot{ConceptID: "c1x", Property: "customer_id", Value: "1"}
			for _, c := range []struct {
				t      *interfaces.QuestionTest
				reason string
			}{
				{test(interfaces.TEST_EXPECT_EXISTS), checkNoValues},
				{test(interfaces.TEST_EXPECT_EXISTS, unmapped), checkUnknownConcept},
				{test(interfaces.TEST_EXPECT_EXISTS, interfaces.QuestionTestSlot{ConceptID: "c2", Property: "phone", Value: "1"}), checkUnknownProperty},
				{test(interfaces.TEST_EXPECT_SAME_RECORD, slot("customer_id", "1")), checkSameRecordNeedsTwo},
			} {
				So(svc.checkTest(ctx, caller, &spec, draft, c.t), ShouldResemble,
					interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: c.reason})
			}

			// A concept that maps to nothing, or to a relation type, names no record.
			spec.Concepts[1].MapsTo = nil
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))).Reason,
				ShouldEqual, checkConceptNotMapped)
			spec.Concepts[1].MapsTo = &relationRef
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))).Reason,
				ShouldEqual, checkConceptNotMapped)

			// The draft no longer has the item, or the property has no field.
			spec.Concepts[1].MapsTo = &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "carrier"}
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))).Reason,
				ShouldEqual, checkItemNotDrafted)
			spec.Concepts[1].MapsTo = &customerRef
			draft.Items.ObjectTypes[1].Properties[0].MappedField = ""
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))).Reason,
				ShouldEqual, checkPropertyNotMapped)
			So(svc.checkTest(ctx, caller, &spec, nil, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))).Reason,
				ShouldEqual, checkItemNotDrafted)
		})

		Convey("Two ways of naming a record need one object type with a single-field identity\n", func() {
			draft.Items.ObjectTypes[0].Properties = []*interfaces.DraftProperty{{Name: "order_no", MappedField: "order_no"}}
			ordersSlot := interfaces.QuestionTestSlot{ConceptID: "c1", Property: "order_no", Value: "SO1"}
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_SAME_RECORD, slot("customer_id", "1"), ordersSlot)).Reason,
				ShouldEqual, checkSameRecordNeedsOneItem)
			draft.Items.ObjectTypes[1].PrimaryKeys = []string{"customer_id", "full_name"}
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_SAME_RECORD, slot("customer_id", "1"), slot("full_name", "A"))).Reason,
				ShouldEqual, checkCompositeIdentity)
		})

		Convey("A count that fails is not checked\n", func() {
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return(nil, errors.New("forbidden"))
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkQueryFailed})
		})
	})
}
