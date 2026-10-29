// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
		// A test's text names its values, given or still slots to fill.
		test := func(expect string, slots ...interfaces.QuestionTestSlot) *interfaces.QuestionTest {
			names := []string{}
			for i, s := range slots {
				if s.Value == "" {
					names = append(names, slotPlaceholder(i))
				} else {
					names = append(names, s.Value)
				}
			}
			return &interfaces.QuestionTest{ID: "t1", Text: "About " + strings.Join(names, " and ") + "?", Expect: expect, Slots: slots}
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

		sample := func(sql string, row map[string]any) {
			va.EXPECT().Query(gomock.Any(), caller, sql, checkQueryTimeout).Return([]map[string]any{row}, nil)
		}

		Convey("An empty value of a record expected to exist is the first value of its field, by identity\n", func() {
			sample("SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v0": float64(10001)})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))

			So(svc.checkTest(ctx, caller, &spec, draft, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(tt.Slots[0].Value, ShouldEqual, "10001")
			// The question asks about the value checked.
			So(tt.Text, ShouldEqual, "About 10001?")
		})

		Convey("A large identity is taken and counted with every digit\n", func() {
			sample("SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1",
				map[string]any{"v0": json.Number("1234567890123456789")})
			va.EXPECT().Query(gomock.Any(), caller, "SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '1234567890123456789'",
				checkQueryTimeout).Return([]map[string]any{{"n": json.Number("1")}}, nil)
			tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))

			So(svc.checkTest(ctx, caller, &spec, draft, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(tt.Text, ShouldEqual, "About 1234567890123456789?")
		})

		Convey("A value the text does not name is no question about it\n", func() {
			tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "10001"))
			tt.Text = "Which orders does this customer have?"

			So(svc.checkTest(ctx, caller, &spec, draft, tt), ShouldResemble,
				interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkValueNotInText})
		})

		Convey("A value to fill needs its slot in the text, whatever the text happens to contain\n", func() {
			// The value the data gives may be 1, which "Top 1 customer" contains by chance.
			tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))
			tt.Text = "Orders of the top 1 customer?"

			So(svc.checkTest(ctx, caller, &spec, draft, tt), ShouldResemble,
				interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkValueNotInText})
			So(tt.Text, ShouldEqual, "Orders of the top 1 customer?")
		})

		Convey("An empty value of a record expected not to exist is a real one with its last digits changed\n", func() {
			sample("SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v0": "C-0042"})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'C-9999'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'C-0000'", float64(0))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'C-0000'", float64(0))
			tt := test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("customer_id", ""))

			So(svc.checkTest(ctx, caller, &spec, draft, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(tt.Slots[0].Value, ShouldEqual, "C-0000")
			So(tt.Text, ShouldEqual, "About C-0000?")
		})

		Convey("A value without digits, or whose changed forms both exist, gives no absent value\n", func() {
			sample("SELECT `name` AS v0 FROM {{r-cust}} WHERE `name` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v0": "Acme"})
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("full_name", ""))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkNoAbsentValue})

			sample("SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v0": "7"})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '9'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '0'", float64(1))
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("customer_id", ""))).Reason,
				ShouldEqual, checkNoAbsentValue)
		})

		Convey("Empty values naming one record come from one row, beside the values given\n", func() {
			sample("SELECT `name` AS v1 FROM {{r-cust}} WHERE `cust_id` = '10001' AND `name` IS NOT NULL ORDER BY `cust_id` LIMIT 1",
				map[string]any{"v1": "Acme Ltd"})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = 'Acme Ltd'", float64(1))
			count("SELECT COUNT(DISTINCT `cust_id`) AS n FROM {{r-cust}} WHERE `cust_id` = '10001' OR `name` = 'Acme Ltd'", float64(1))
			tt := test(interfaces.TEST_EXPECT_SAME_RECORD, slot("customer_id", "10001"), slot("full_name", ""))

			So(svc.checkTest(ctx, caller, &spec, draft, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(tt.Slots[1].Value, ShouldEqual, "Acme Ltd")
		})

		Convey("A table with no value to take, or a read that fails, leaves the test not checked\n", func() {
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return([]map[string]any{}, nil)
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))).Reason,
				ShouldEqual, checkNoValueInData)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return(nil, errors.New("forbidden"))
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))).Reason,
				ShouldEqual, checkQueryFailed)
		})

		Convey("A field the privacy rules excluded is never read or counted\n", func() {
			draft.Items.Excluded = []*interfaces.DraftExcludedField{{ObjectType: "customer", Field: "name"}}
			for _, value := range []string{"", "Acme Ltd"} {
				So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("full_name", value))),
					ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkExcludedField})
			}
		})

		Convey("Tests left when checking runs out of time are not checked\n", func() {
			clock := int64(100)
			svc.now = func() int64 { return clock }
			first := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "10001"))
			second := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "10002"))
			// Checking the first takes past the deadline: the second is not started.
			va.EXPECT().Query(gomock.Any(), caller, "SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", checkQueryTimeout).
				DoAndReturn(func(context.Context, interfaces.VegaAccount, string, int) ([]map[string]any, error) {
					clock = 200
					return []map[string]any{{"n": float64(1)}}, nil
				})

			svc.checkTests(ctx, caller, &spec, draft, []*interfaces.QuestionTest{first, second}, 150)

			So(first.Check.Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(second.Check, ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkTimeout})
		})

		Convey("A count that fails is not checked\n", func() {
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return(nil, errors.New("forbidden"))
			So(svc.checkTest(ctx, caller, &spec, draft, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkQueryFailed})
		})
	})
}
