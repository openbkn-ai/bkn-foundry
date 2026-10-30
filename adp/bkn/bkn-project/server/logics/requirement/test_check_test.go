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
		// The scenario question the tests ask in other ways.
		asked := spec.Questions[0]
		count := func(sql string, n any) {
			va.EXPECT().Query(gomock.Any(), caller, sql, checkQueryTimeout).Return([]map[string]any{{"n": n}}, nil)
		}

		Convey("A record expected to exist passes when every value is in its field, counted as the caller\n", func() {
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "10001"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED})

			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '99999'", "0")
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "99999"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkRecordNotFound})
		})

		Convey("A record expected not to exist passes only when no value is found\n", func() {
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '99999'", float64(0))
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("customer_id", "99999"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED})

			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(2))
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("customer_id", "10001"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkRecordExists})
		})

		Convey("Two ways of naming one record must find that one record\n", func() {
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = 'Acme Ltd'", float64(1))
			count("SELECT COUNT(DISTINCT `cust_id`) AS n FROM {{r-cust}} WHERE `cust_id` = '10001' OR `name` = 'Acme Ltd'", float64(1))
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_SAME_RECORD,
				slot("customer_id", "10001"), slot("full_name", "Acme Ltd"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED})

			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = 'Other Co'", float64(1))
			count("SELECT COUNT(DISTINCT `cust_id`) AS n FROM {{r-cust}} WHERE `cust_id` = '10001' OR `name` = 'Other Co'", float64(2))
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_SAME_RECORD,
				slot("customer_id", "10001"), slot("full_name", "Other Co"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkDifferentRecords})
		})

		Convey("A value is written as a literal, quotes and backslashes escaped\n", func() {
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = 'O''Brien \\\\ Sons'", float64(1))
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("full_name", `O'Brien \ Sons`))).Outcome,
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
				So(svc.checkTest(ctx, caller, &spec, draft, asked, c.t), ShouldResemble,
					interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: c.reason})
			}

			// A concept that maps to nothing, or to a relation type, names no record.
			spec.Concepts[1].MapsTo = nil
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))).Reason,
				ShouldEqual, checkConceptNotMapped)
			spec.Concepts[1].MapsTo = &relationRef
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))).Reason,
				ShouldEqual, checkConceptNotMapped)

			// The draft no longer has the item, or the property has no field.
			spec.Concepts[1].MapsTo = &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "carrier"}
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))).Reason,
				ShouldEqual, checkItemNotDrafted)
			spec.Concepts[1].MapsTo = &customerRef
			draft.Items.ObjectTypes[1].Properties[0].MappedField = ""
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))).Reason,
				ShouldEqual, checkPropertyNotMapped)
			So(svc.checkTest(ctx, caller, &spec, nil, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))).Reason,
				ShouldEqual, checkItemNotDrafted)
		})

		Convey("Two ways of naming a record need one object type with a single-field identity\n", func() {
			draft.Items.ObjectTypes[0].Properties = []*interfaces.DraftProperty{{Name: "order_no", MappedField: "order_no"}}
			ordersSlot := interfaces.QuestionTestSlot{ConceptID: "c1", Property: "order_no", Value: "SO1"}
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_SAME_RECORD, slot("customer_id", "1"), ordersSlot)).Reason,
				ShouldEqual, checkSameRecordNeedsOneItem)
			draft.Items.ObjectTypes[1].PrimaryKeys = []string{"customer_id", "full_name"}
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_SAME_RECORD, slot("customer_id", "1"), slot("full_name", "A"))).Reason,
				ShouldEqual, checkCompositeIdentity)
		})

		sample := func(sql string, row map[string]any) {
			va.EXPECT().Query(gomock.Any(), caller, sql, checkQueryTimeout).Return([]map[string]any{row}, nil)
		}

		Convey("An empty value of a record expected to exist is the first value of its field, by identity\n", func() {
			sample("SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v0": float64(10001)})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))

			So(svc.checkTest(ctx, caller, &spec, draft, asked, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(tt.Slots[0].Value, ShouldEqual, "10001")
			// The question asks about the value checked.
			So(tt.Text, ShouldEqual, "About 10001?")
		})

		Convey("Empty values of one object type are taken from one row, so they name one record\n", func() {
			sample("SELECT `cust_id` AS v0, `name` AS v1 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL AND `name` IS NOT NULL ORDER BY `cust_id` LIMIT 1",
				map[string]any{"v0": "10001", "v1": "Acme Ltd"})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = 'Acme Ltd'", float64(1))
			tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""), slot("full_name", ""))

			So(svc.checkTest(ctx, caller, &spec, draft, asked, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(tt.Text, ShouldEqual, "About 10001 and Acme Ltd?")
		})

		Convey("A same_record test names the record of the server's template test\n", func() {
			anchor := &interfaces.QuestionTest{ID: "t1", Kind: interfaces.TEST_KIND_TEMPLATE, ByServer: true,
				Slots: []interfaces.QuestionTestSlot{slot("customer_id", "10001")},
				Check: interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED}}
			named := func(n float64, same float64, name string) {
				count("SELECT COUNT(DISTINCT `cust_id`) AS n FROM {{r-cust}} WHERE `name` = '"+name+"'", n)
				if n == 1 {
					count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = '"+name+"' AND `cust_id` = '10001'", same)
				}
			}

			// One other way to name it, taken from that record's row.
			sample("SELECT `name` AS v0 FROM {{r-cust}} WHERE `cust_id` = '10001' AND `name` IS NOT NULL ORDER BY `cust_id` LIMIT 1",
				map[string]any{"v0": "Acme Ltd"})
			named(1, 1, "Acme Ltd")
			tt := test(interfaces.TEST_EXPECT_SAME_RECORD, slot("full_name", ""))
			So(svc.checkAnchored(ctx, caller, &spec, draft, asked, tt, anchor), ShouldResemble,
				interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED})
			So(tt.Text, ShouldEqual, "About Acme Ltd?")

			for _, c := range []struct {
				records, same float64
				reason        string
			}{
				{1, 0, checkDifferentRecords},
				{2, 0, checkDifferentRecords},
				{0, 0, checkRecordNotFound},
			} {
				named(c.records, c.same, "Other Co")
				So(svc.checkAnchored(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_SAME_RECORD, slot("full_name", "Other Co")), anchor),
					ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: c.reason})
			}

			// Several fields that name it together are one way of naming it.
			count("SELECT COUNT(DISTINCT `cust_id`) AS n FROM {{r-cust}} WHERE `cust_id` = '10001' AND `name` = 'Acme Ltd'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001' AND `name` = 'Acme Ltd' AND `cust_id` = '10001'", float64(1))
			So(svc.checkAnchored(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_SAME_RECORD,
				slot("customer_id", "10001"), slot("full_name", "Acme Ltd")), anchor).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
		})

		Convey("Only a template test the data bore out anchors, and checking the tests passes it on\n", func() {
			template := &interfaces.QuestionTest{ID: "t1", Kind: interfaces.TEST_KIND_TEMPLATE, ByServer: true, Text: "About 10001?",
				Expect: interfaces.TEST_EXPECT_EXISTS, Slots: []interfaces.QuestionTestSlot{slot("customer_id", "10001")}}
			So(anchorOf([]*interfaces.QuestionTest{template}), ShouldBeNil)
			agentWritten := *template
			agentWritten.ByServer, agentWritten.Check.Outcome = false, interfaces.TEST_CHECK_PASSED
			So(anchorOf([]*interfaces.QuestionTest{&agentWritten}), ShouldBeNil)

			variant := test(interfaces.TEST_EXPECT_SAME_RECORD, slot("full_name", "Acme Ltd"))
			variant.ID = "t2"
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			count("SELECT COUNT(DISTINCT `cust_id`) AS n FROM {{r-cust}} WHERE `name` = 'Acme Ltd'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = 'Acme Ltd' AND `cust_id` = '10001'", float64(1))
			svc.now = func() int64 { return 0 }
			svc.checkTests(ctx, caller, &spec, draft, asked, []*interfaces.QuestionTest{template, variant}, 1)

			So(template.Check.Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(anchorOf([]*interfaces.QuestionTest{template}), ShouldEqual, template)
			So(variant.Check.Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
		})

		Convey("A large identity is taken and counted with every digit\n", func() {
			sample("SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1",
				map[string]any{"v0": json.Number("1234567890123456789")})
			va.EXPECT().Query(gomock.Any(), caller, "SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '1234567890123456789'",
				checkQueryTimeout).Return([]map[string]any{{"n": json.Number("1")}}, nil)
			tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))

			So(svc.checkTest(ctx, caller, &spec, draft, asked, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(tt.Text, ShouldEqual, "About 1234567890123456789?")
		})

		Convey("A value the text says outside every slot is checked by no one\n", func() {
			spec := spec
			spec.Rules = []*interfaces.RequirementRule{{ID: "r1", Statement: "Orders over 50000 need approval."}}
			for _, c := range []struct {
				asked, text string
				outside     bool
			}{
				{"Which orders?", "About {{slot 1}} on 2025-06-30?", true},
				{"Which orders?", "About {{slot 1}} at 10:30?", true},
				{"Which orders?", "About {{slot 1}} or CUST-10001?", true},
				{"Which orders?", "About {{slot 1}} above 3.5?", true},
				{"Which orders?", "客户{{slot 1}}前 10 名的订单？", true},
				{"Which orders of the last 30 days?", "About {{slot 1}} in the last 30 days?", false},
				{"Which orders?", "About {{slot 1}} over 50000?", false},
				// A number within a longer one the question or a rule says is not stated.
				{"Which orders of the last 30 days?", "About {{slot 1}} in the last 3 days?", true},
				{"Which orders?", "About {{slot 1}} over 5000?", true},
				{"Which orders?", "About {{slot 1}}?", false},
				// Its number with a unit, joined or not, in the question or a rule.
				{"Which orders of the next 14 days?", "About {{slot 1}} in a 14-day window?", false},
				{"Which orders in a 14-day window?", "About {{slot 1}} in the next 14 days?", false},
				{"Which orders of the next 14 days?", "About {{slot 1}} in a 15-day window?", true},
				{"Which orders of A14?", "About {{slot 1}} at A14?", false},
				{"Which orders of the next 14 days?", "About {{slot 1}} at A14?", true},
			} {
				tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))
				tt.Text = c.text
				if !c.outside {
					sample("SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v0": "10001"})
					count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
				}

				check := svc.checkTest(ctx, caller, &spec, draft, &interfaces.RequirementQuestion{Text: c.asked}, tt)

				So(check.Reason == checkValueOutsideSlot, ShouldEqual, c.outside)
			}
		})

		Convey("A value the documents gave is no value outside its slot\n", func() {
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'CUST-10001'", float64(1))
			tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "CUST-10001"))

			So(svc.checkTest(ctx, caller, &spec, draft, asked, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
		})

		Convey("Of a record expected not to exist, only the first slot is made absent; the others keep real values\n", func() {
			sample("SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v0": "C-0042"})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'C-9999'", float64(0))
			sample("SELECT `name` AS v1 FROM {{r-cust}} WHERE `name` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v1": "Acme"})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'C-9999'", float64(0))
			tt := test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("customer_id", ""), slot("full_name", ""))

			So(svc.checkTest(ctx, caller, &spec, draft, asked, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(tt.Text, ShouldEqual, "About C-9999 and Acme?")
		})

		Convey("A value for a record that exists is taken from the records the question's relation reaches\n", func() {
			pathDraft := guidanceDraft()
			pathDraft.Items.ObjectTypes[1].Table = interfaces.DraftTable{ResourceID: "r-cust"}
			pathDraft.Items.ObjectTypes[1].PrimaryKeys = []string{"customer_id"}
			pathDraft.Items.ObjectTypes[0].Table = interfaces.DraftTable{ResourceID: "r-so"}
			pathDraft.Items.ObjectTypes[0].Properties = []*interfaces.DraftProperty{{Name: "customer_ref", Type: "string", MappedField: "cust_ref"}}
			pathDraft.Items.RelationTypes[0].Mapping = []interfaces.DraftFieldMapping{{SourceProperty: "customer_ref", TargetProperty: "customer_id"}}
			rel := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "sales_order_customer"}
			withPath := &interfaces.RequirementQuestion{Text: asked.Text, Needs: []interfaces.RequirementItemRef{customerRef, rel}}
			// The shape vega-backend accepts: a join on the other end's distinct values, never a
			// subquery in WHERE, which it rejects as invalid syntax.
			reached := "SELECT a.`cust_id` AS v FROM {{r-cust}} a JOIN (SELECT DISTINCT `cust_ref` FROM {{r-so}}) b ON a.`cust_id` = b.`cust_ref` WHERE a.`cust_id` IS NOT NULL ORDER BY a.`cust_id` LIMIT 1"
			anyRecord := "SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1"

			Convey("and says where it came from\n", func() {
				sample(reached, map[string]any{"v": "10007"})
				count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10007'", float64(1))
				tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))
				tt.Slots[0].ValueFrom = "stale"

				So(svc.checkTest(ctx, caller, &spec, pathDraft, withPath, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
				So(tt.Slots[0].Value, ShouldEqual, "10007")
				So(tt.Slots[0].ValueFrom, ShouldEqual, "sales_order")
			})

			Convey("or, when it reaches none, from any record, saying nothing of a path\n", func() {
				va.EXPECT().Query(gomock.Any(), caller, reached, checkQueryTimeout).Return([]map[string]any{}, nil)
				sample(anyRecord, map[string]any{"v0": "10001"})
				count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
				tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))
				tt.Slots[0].ValueFrom = "stale"

				So(svc.checkTest(ctx, caller, &spec, pathDraft, withPath, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
				So(tt.Slots[0].ValueFrom, ShouldEqual, "")
			})

			Convey("a failed read is no check\n", func() {
				va.EXPECT().Query(gomock.Any(), caller, reached, checkQueryTimeout).Return(nil, errors.New("boom"))
				tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))

				So(svc.checkTest(ctx, caller, &spec, pathDraft, withPath, tt).Reason, ShouldEqual, checkQueryFailed)
			})

			Convey("but the record that should not exist is not looked for there\n", func() {
				sample(anyRecord, map[string]any{"v0": "C-0042"})
				count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'C-9999'", float64(0))
				count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'C-9999'", float64(0))
				tt := test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("customer_id", ""))

				So(svc.checkTest(ctx, caller, &spec, pathDraft, withPath, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
				So(tt.Slots[0].ValueFrom, ShouldEqual, "")
			})

			Convey("nor by a relation the question does not need, through a link table, or on several fields\n", func() {
				for _, change := range []func(q *interfaces.RequirementQuestion, rt *interfaces.DraftRelationType){
					func(q *interfaces.RequirementQuestion, _ *interfaces.DraftRelationType) { q.Needs = q.Needs[:1] },
					// An object type of the same id is not the relation.
					func(q *interfaces.RequirementQuestion, _ *interfaces.DraftRelationType) {
						q.Needs = []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order_customer"}}
					},
					func(_ *interfaces.RequirementQuestion, rt *interfaces.DraftRelationType) {
						rt.LinkTable = &interfaces.DraftTable{}
					},
					func(_ *interfaces.RequirementQuestion, rt *interfaces.DraftRelationType) {
						rt.Mapping = append(rt.Mapping, interfaces.DraftFieldMapping{SourceProperty: "a", TargetProperty: "b"})
					},
				} {
					q := *withPath
					rt := *pathDraft.Items.RelationTypes[0]
					change(&q, &rt)
					d := *pathDraft
					d.Items.RelationTypes = []*interfaces.DraftRelationType{&rt}
					So(pathOf(&d, &q, &slotField{objectType: d.Items.ObjectTypes[1], field: "cust_id"}), ShouldBeNil)
				}
				So(pathOf(pathDraft, withPath, &slotField{objectType: pathDraft.Items.ObjectTypes[1], field: "cust_id"}), ShouldNotBeNil)

				// Nor across catalogs: one query cannot read both tables.
				d := *pathDraft
				other := *d.Items.ObjectTypes[0]
				other.Table.CatalogID = "c2"
				d.Items.ObjectTypes = []*interfaces.DraftObjectType{&other, d.Items.ObjectTypes[1]}
				So(pathOf(&d, withPath, &slotField{objectType: d.Items.ObjectTypes[1], field: "cust_id"}), ShouldBeNil)
			})

			Convey("through one object type between, when no relation the question needs reaches it\n", func() {
				// material <- purchase order line -> supplier: the question needs material and supplier only.
				hop := &interfaces.Draft{Items: interfaces.DraftItems{
					ObjectTypes: []*interfaces.DraftObjectType{
						{ID: "material", Table: interfaces.DraftTable{ResourceID: "r-mat", CatalogID: "c1"}, Adopted: true,
							Properties: []*interfaces.DraftProperty{{Name: "material_code", MappedField: "code"}}},
						{ID: "po_line", Table: interfaces.DraftTable{ResourceID: "r-po", CatalogID: "c1"}, Adopted: true,
							Properties: []*interfaces.DraftProperty{{Name: "material_number", MappedField: "mat_no"}, {Name: "supplier_number", MappedField: "sup_no"}}},
						{ID: "supplier", Table: interfaces.DraftTable{ResourceID: "r-sup", CatalogID: "c1"}, Adopted: true,
							Properties: []*interfaces.DraftProperty{{Name: "supplier_code", MappedField: "sup_code"}}},
						{ID: "inventory", Table: interfaces.DraftTable{ResourceID: "r-inv", CatalogID: "c1"}, Adopted: true,
							Properties: []*interfaces.DraftProperty{{Name: "material_code", MappedField: "mat"}}},
					},
					RelationTypes: []*interfaces.DraftRelationType{
						// Inventory reaches materials but no other needed object type: passed over.
						{ID: "inv_mat", Source: "inventory", Target: "material", Adopted: true,
							Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "material_code", TargetProperty: "material_code"}}},
						{ID: "po_sup", Source: "po_line", Target: "supplier", Adopted: true,
							Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "supplier_number", TargetProperty: "supplier_code"}}},
						{ID: "po_mat", Source: "po_line", Target: "material", Adopted: true,
							Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "material_number", TargetProperty: "material_code"}}},
					},
				}}
				needs := &interfaces.RequirementQuestion{Needs: []interfaces.RequirementItemRef{
					{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "material"}, {Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "supplier"}}}
				slotOn := &slotField{objectType: hop.Items.ObjectTypes[0], field: "code"}

				p := pathOf(hop, needs, slotOn)

				So(p.objectType, ShouldEqual, "po_line")
				// The shape vega-backend accepts: joins on derived tables, no subquery in WHERE.
				So(p.sample("r-mat", "code", "code"), ShouldEqual,
					"SELECT a.`code` AS v FROM {{r-mat}} a JOIN (SELECT DISTINCT i.`mat_no` FROM {{r-po}} i JOIN (SELECT DISTINCT `sup_code` FROM {{r-sup}}) c ON i.`sup_no` = c.`sup_code`) b ON a.`code` = b.`mat_no` WHERE a.`code` IS NOT NULL ORDER BY a.`code` LIMIT 1")

				// Not when the question names no other object type, nor through relations not adopted.
				alone := &interfaces.RequirementQuestion{Needs: needs.Needs[:1]}
				So(pathOf(hop, alone, slotOn), ShouldBeNil)
				hop.Items.RelationTypes[2].Adopted = false
				So(pathOf(hop, needs, slotOn), ShouldBeNil)
				hop.Items.RelationTypes[2].Adopted = true
				hop.Items.RelationTypes[1].Adopted = false
				So(pathOf(hop, needs, slotOn), ShouldBeNil)
				hop.Items.RelationTypes[1].Adopted = true
				// Nor across catalogs.
				hop.Items.ObjectTypes[2].Table.CatalogID = "c2"
				So(pathOf(hop, needs, slotOn), ShouldBeNil)
				hop.Items.ObjectTypes[2].Table.CatalogID = "c1"

				// Filled through the line, saying so; from any material when no line reaches a supplier.
				hopSQL := p.sample("r-mat", "code", "code")
				for _, c := range []struct {
					rows  []map[string]any
					value string
					from  string
				}{
					{[]map[string]any{{"v": "101-000005"}}, "101-000005", "po_line"},
					{[]map[string]any{}, "101-000002", ""},
					// A read through the line that fails (too slow, or the caller may not read lines)
					// is passed over too.
					{nil, "101-000002", ""},
				} {
					var err error
					if c.rows == nil {
						err = errors.New("timeout")
					}
					va.EXPECT().Query(gomock.Any(), caller, hopSQL, checkQueryTimeout).Return(c.rows, err)
					if c.from == "" {
						sample("SELECT `code` AS v0 FROM {{r-mat}} WHERE `code` IS NOT NULL ORDER BY `code` LIMIT 1",
							map[string]any{"v0": "101-000002"})
					}
					tt := &interfaces.QuestionTest{Text: "Suppliers of {{slot 1}}?", Expect: interfaces.TEST_EXPECT_EXISTS,
						Slots: []interfaces.QuestionTestSlot{{ConceptID: "c1", Property: "material_code"}}}

					reason := svc.fill(ctx, caller, tt, []*slotField{{objectType: hop.Items.ObjectTypes[0], field: "code"}},
						func(f *slotField) *relationPath { return pathOf(hop, needs, f) }, nil)

					So(reason, ShouldEqual, "")
					So(tt.Slots[0].Value, ShouldEqual, c.value)
					So(tt.Slots[0].ValueFrom, ShouldEqual, c.from)
				}

				// A relation between the two, though the question does not name it, is the shorter way.
				between := *hop
				between.Items.RelationTypes = append([]*interfaces.DraftRelationType{{ID: "mat_sup", Source: "material", Target: "supplier",
					Adopted: true, Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "material_code", TargetProperty: "supplier_code"}}}},
					hop.Items.RelationTypes...)
				short := pathOf(&between, needs, slotOn)
				So(short.objectType, ShouldEqual, "supplier")
				So(short.reached, ShouldEqual, "SELECT DISTINCT `sup_code` FROM {{r-sup}}")
				So(short.through, ShouldBeTrue)

				// A relation back to the slot's own object type is no way to the other one.
				back := *hop
				back.Items.RelationTypes = []*interfaces.DraftRelationType{hop.Items.RelationTypes[2],
					{ID: "po_alt", Source: "po_line", Target: "material", Adopted: true,
						Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "supplier_number", TargetProperty: "material_code"}}},
					hop.Items.RelationTypes[1]}
				So(pathOf(&back, needs, slotOn).reached, ShouldContainSubstring, "{{r-sup}}")

				// A relation the question needs is followed directly, without a hop.
				direct := &interfaces.RequirementQuestion{Needs: append(append([]interfaces.RequirementItemRef{}, needs.Needs...),
					interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "inv_mat"})}
				So(pathOf(hop, direct, slotOn).objectType, ShouldEqual, "inventory")
			})

			Convey("from either end of the relation\n", func() {
				order := pathDraft.Items.ObjectTypes[0]
				p := pathOf(pathDraft, withPath, &slotField{objectType: order, field: "cust_ref"})

				So(p.objectType, ShouldEqual, "customer")
				So(p.sample("r-so", "cust_ref", "so_id"), ShouldEqual,
					"SELECT a.`cust_ref` AS v FROM {{r-so}} a JOIN (SELECT DISTINCT `cust_id` FROM {{r-cust}}) b ON a.`cust_ref` = b.`cust_id` WHERE a.`cust_ref` IS NOT NULL ORDER BY a.`so_id` LIMIT 1")
			})
		})

		Convey("A value the text does not name is no question about it\n", func() {
			tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "10001"))
			tt.Text = "Which orders does this customer have?"

			So(svc.checkTest(ctx, caller, &spec, draft, asked, tt), ShouldResemble,
				interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkValueNotInText})
		})

		Convey("A value to fill needs its slot in the text, whatever the text happens to contain\n", func() {
			// The value the data gives may be 1, which "Top 1 customer" contains by chance.
			tt := test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))
			tt.Text = "Orders of the top 1 customer?"

			So(svc.checkTest(ctx, caller, &spec, draft, asked, tt), ShouldResemble,
				interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkValueNotInText})
			So(tt.Text, ShouldEqual, "Orders of the top 1 customer?")
		})

		Convey("An empty value of a record expected not to exist is a real one with its last digits changed\n", func() {
			sample("SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v0": "C-0042"})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'C-9999'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'C-0000'", float64(0))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = 'C-0000'", float64(0))
			tt := test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("customer_id", ""))

			So(svc.checkTest(ctx, caller, &spec, draft, asked, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(tt.Slots[0].Value, ShouldEqual, "C-0000")
			So(tt.Text, ShouldEqual, "About C-0000?")
		})

		Convey("A value without digits, or whose changed forms both exist, gives no absent value\n", func() {
			sample("SELECT `name` AS v0 FROM {{r-cust}} WHERE `name` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v0": "Acme"})
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("full_name", ""))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkNoAbsentValue})

			sample("SELECT `cust_id` AS v0 FROM {{r-cust}} WHERE `cust_id` IS NOT NULL ORDER BY `cust_id` LIMIT 1", map[string]any{"v0": "7"})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '9'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '0'", float64(1))
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_NOT_EXISTS, slot("customer_id", ""))).Reason,
				ShouldEqual, checkNoAbsentValue)
		})

		Convey("Empty values naming one record come from one row, beside the values given\n", func() {
			sample("SELECT `name` AS v1 FROM {{r-cust}} WHERE `cust_id` = '10001' AND `name` IS NOT NULL ORDER BY `cust_id` LIMIT 1",
				map[string]any{"v1": "Acme Ltd"})
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", float64(1))
			count("SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `name` = 'Acme Ltd'", float64(1))
			count("SELECT COUNT(DISTINCT `cust_id`) AS n FROM {{r-cust}} WHERE `cust_id` = '10001' OR `name` = 'Acme Ltd'", float64(1))
			tt := test(interfaces.TEST_EXPECT_SAME_RECORD, slot("customer_id", "10001"), slot("full_name", ""))

			So(svc.checkTest(ctx, caller, &spec, draft, asked, tt).Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(tt.Slots[1].Value, ShouldEqual, "Acme Ltd")
		})

		Convey("A table with no value to take, or a read that fails, leaves the test not checked\n", func() {
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return([]map[string]any{}, nil)
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))).Reason,
				ShouldEqual, checkNoValueInData)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return(nil, errors.New("forbidden"))
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", ""))).Reason,
				ShouldEqual, checkQueryFailed)
		})

		Convey("A field the privacy rules excluded is never read or counted\n", func() {
			draft.Items.Excluded = []*interfaces.DraftExcludedField{{ObjectType: "customer", Field: "name"}}
			for _, value := range []string{"", "Acme Ltd"} {
				So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("full_name", value))),
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

			svc.checkTests(ctx, caller, &spec, draft, asked, []*interfaces.QuestionTest{first, second}, 150)

			So(first.Check.Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(second.Check, ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkTimeout})
		})

		Convey("A count that fails is not checked\n", func() {
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return(nil, errors.New("forbidden"))
			So(svc.checkTest(ctx, caller, &spec, draft, asked, test(interfaces.TEST_EXPECT_EXISTS, slot("customer_id", "1"))),
				ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkQueryFailed})
		})
	})
}

func Test_valueTokens(t *testing.T) {
	Convey("Numbers, dates, times and codes are each one token, with the marks joined to them\n", t, func() {
		So(valueTokens("on 2025-06-30 at 10:30, CUST-10001 above 3.5."), ShouldResemble,
			[]string{"2025-06-30", "10:30", "CUST-10001", "3.5"})
		So(valueTokens("前 10 名，第3季度"), ShouldResemble, []string{"10", "3"})
		So(valueTokens("Which orders, and why?"), ShouldResemble, []string{})
	})
}

func Test_numberOf(t *testing.T) {
	Convey("A number with a unit joined to it is its number; a code keeps its letters\n", t, func() {
		for token, want := range map[string]string{
			"14-day": "14", "14-Days": "14", "30d": "30", "3.5kg": "3.5", "2h": "2", "6-months": "6", "10pcs": "10",
			// Not a unit: a code, or letters before the number.
			"999-A": "999-A", "A14": "A14", "14-ABC": "14-ABC", "LOT-999-A": "LOT-999-A", "2025-06-30": "2025-06-30",
			"10:30": "10:30", "14": "14", "3.5": "3.5", "12th": "12th",
		} {
			So(numberOf(token), ShouldEqual, want)
		}
	})
}
