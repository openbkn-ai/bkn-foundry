// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func need(kind string, id string) interfaces.RequirementItemRef {
	return interfaces.RequirementItemRef{Kind: kind, ID: id}
}

func question(id string, layer string, needs ...interfaces.RequirementItemRef) interfaces.ScenarioQuestion {
	return interfaces.ScenarioQuestion{ID: id, Text: "Text of " + id, Layer: layer, Needs: needs}
}

func Test_ReleaseQuestions(t *testing.T) {
	Convey("Test releaseQuestions\n", t, func() {
		const ot, rt, knowledge = interfaces.RELEASE_ITEM_OBJECT_TYPE, interfaces.RELEASE_ITEM_RELATION_TYPE,
			interfaces.REQUIREMENT_LAYER_KNOWLEDGE
		mainFiles := map[string][]byte{
			"network.bkn":               []byte(networkBKN),
			"object_types/part.bkn":     objectTypeBKN("part", "Part", true),
			"object_types/supplier.bkn": objectTypeBKN("supplier", "Supplier", true),
		}
		branchFiles := map[string][]byte{
			"network.bkn":                     []byte(networkBKN),
			"object_types/part.bkn":           objectTypeBKN("part", "Part", true),
			"object_types/purchase_order.bkn": objectTypeBKN("purchase_order", "Purchase Order", true),
			"relation_types/supplier_has_purchase_order.bkn": relationTypeBKN("supplier_has_purchase_order",
				"Supplier has purchase order"),
		}
		diff := interfaces.PathDiff{
			Added:   []string{"object_types/purchase_order.bkn", "relation_types/supplier_has_purchase_order.bkn"},
			Changed: []string{"object_types/part.bkn"},
			Removed: []string{"object_types/supplier.bkn"},
		}
		purchaseOrder := item(ot, "purchase_order", "Purchase Order")
		relation := item(rt, "supplier_has_purchase_order", "Supplier has purchase order")
		part, supplier := item(ot, "part", "Part"), item(ot, "supplier", "Supplier")

		Convey("Each knowledge question is placed by where its needs are\n", func() {
			sq := &interfaces.ScenarioQuestions{SpecVersion: 3, UpdateTime: 9, Questions: []interfaces.ScenarioQuestion{
				// Answerable only once released.
				question("q1", knowledge, need(ot, "purchase_order"), need(rt, "supplier_has_purchase_order")),
				// Answerable now, and the release changes what it needs.
				question("q2", knowledge, need(ot, "part")),
				// Answerable now, and the release removes what it needs.
				question("q3", knowledge, need(ot, "supplier")),
				// Needs something no definition has.
				question("q4", knowledge, need(ot, "customer"), need(ot, "purchase_order")),
				// Says nothing of what it needs.
				question("q5", knowledge),
				// Not answered from definitions.
				question("q6", interfaces.REQUIREMENT_LAYER_CAPABILITY, need(ot, "part")),
			}}

			q := releaseQuestions(sq, branchFiles, mainFiles, diff)

			So(q.SpecVersion, ShouldEqual, 3)
			So(q.Total, ShouldEqual, 5)
			So(q.Unjudged, ShouldEqual, 1)
			So(q.AnswerableNew, ShouldResemble, []interfaces.ReleaseQuestion{
				{ID: "q1", Text: "Text of q1", Needs: []interfaces.ReleaseItem{purchaseOrder, relation}},
			})
			So(q.Affected, ShouldResemble, []interfaces.ReleaseQuestion{
				{ID: "q2", Text: "Text of q2", Needs: []interfaces.ReleaseItem{part}},
				{ID: "q3", Text: "Text of q3", Needs: []interfaces.ReleaseItem{supplier}},
			})
			So(q.Unanswerable, ShouldResemble, []interfaces.ReleaseQuestion{
				{ID: "q3", Text: "Text of q3", Needs: []interfaces.ReleaseItem{supplier},
					Missing: []interfaces.ReleaseItem{supplier}},
				{ID: "q4", Text: "Text of q4", Needs: []interfaces.ReleaseItem{item(ot, "customer", "customer"), purchaseOrder},
					Missing: []interfaces.ReleaseItem{item(ot, "customer", "customer")}},
			})
		})

		Convey("A question answerable before and after that the release does not touch is only counted\n", func() {
			sq := &interfaces.ScenarioQuestions{Questions: []interfaces.ScenarioQuestion{question("q1", knowledge, need(ot, "part"))}}

			q := releaseQuestions(sq, branchFiles, mainFiles, interfaces.PathDiff{})

			So(q.Total, ShouldEqual, 1)
			So(q.AnswerableNew, ShouldBeEmpty)
			So(q.Affected, ShouldBeEmpty)
			So(q.Unanswerable, ShouldBeEmpty)
		})

		Convey("A question needing a changed item and a new one is answerable only once released\n", func() {
			sq := &interfaces.ScenarioQuestions{Questions: []interfaces.ScenarioQuestion{
				question("q1", knowledge, need(ot, "part"), need(ot, "purchase_order")),
			}}

			q := releaseQuestions(sq, branchFiles, mainFiles, diff)

			So(q.AnswerableNew, ShouldHaveLength, 1)
			So(q.Affected, ShouldBeEmpty)
		})

		Convey("A removed item is known by the id main's file gives it, whatever the file is called\n", func() {
			main := map[string][]byte{"object_types/vendor.bkn": objectTypeBKN("supplier", "Supplier", true)}
			sq := &interfaces.ScenarioQuestions{Questions: []interfaces.ScenarioQuestion{question("q1", knowledge, need(ot, "supplier"))}}

			q := releaseQuestions(sq, map[string][]byte{}, main, interfaces.PathDiff{Removed: []string{"object_types/vendor.bkn"}})

			So(q.Affected, ShouldResemble, []interfaces.ReleaseQuestion{{ID: "q1", Text: "Text of q1", Needs: []interfaces.ReleaseItem{supplier}}})
		})

		Convey("A relation type and an object type of the same id are told apart\n", func() {
			sq := &interfaces.ScenarioQuestions{Questions: []interfaces.ScenarioQuestion{
				question("q1", knowledge, need(rt, "purchase_order")),
			}}

			q := releaseQuestions(sq, branchFiles, mainFiles, diff)

			So(q.Unanswerable, ShouldHaveLength, 1)
			So(q.Unanswerable[0].Missing, ShouldResemble, []interfaces.ReleaseItem{item(rt, "purchase_order", "purchase_order")})
		})

		Convey("Without a specification there are no questions, and the lists are empty, not null\n", func() {
			for _, sq := range []*interfaces.ScenarioQuestions{nil, {Questions: []interfaces.ScenarioQuestion{}}} {
				q := releaseQuestions(sq, branchFiles, mainFiles, diff)

				So(q, ShouldResemble, interfaces.ReleaseQuestions{
					AnswerableNew: []interfaces.ReleaseQuestion{}, Affected: []interfaces.ReleaseQuestion{},
					Unanswerable: []interfaces.ReleaseQuestion{},
				})
			}
		})
	})
}
