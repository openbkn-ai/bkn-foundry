// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_UndecidedItems(t *testing.T) {
	Convey("Test undecidedItems\n", t, func() {
		draft := func() *interfaces.DraftItems {
			items := confirmableDraft().Items
			items.Decisions = nil
			// audit_log waits for its identity; customers_to_products was only proposed.
			items.ObjectTypes[3].NeedsIdentity = true
			items.RelationTypes = append(items.RelationTypes, compileFixture().RelationTypes[3])
			items.RelationTypes[2].ProposedBecause = "below_adopt_hit_rate"
			return &items
		}

		Convey("What drafting left out and no one decided is undecided, with why, by kind and ID\n", func() {
			So(undecidedItems(draft()), ShouldResemble, []undecidedItem{
				{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "audit_log", Because: interfaces.BLOCKER_NEEDS_IDENTITY},
				{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: "customers_to_products", Because: "below_adopt_hit_rate"},
			})
		})

		Convey("A decision on an item decides it, whichever it was\n", func() {
			items := draft()
			items.Decisions = []*interfaces.DraftDecision{
				{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: "customers_to_products",
					Action: interfaces.DRAFT_DECISION_UNADOPT},
			}

			So(len(undecidedItems(items)), ShouldEqual, 1)
		})

		Convey("Choosing an identity alone does not decide whether the object type is in\n", func() {
			items := draft()
			items.Decisions = []*interfaces.DraftDecision{
				{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "audit_log", Action: interfaces.DRAFT_DECISION_IDENTITY},
			}

			So(undecidedItems(items)[0].ItemID, ShouldEqual, "audit_log")
		})

		Convey("Items an earlier confirmation accepted as undecided are decided\n", func() {
			items := draft()
			items.Decisions = []*interfaces.DraftDecision{{
				Kind: interfaces.DRAFT_KIND_DRAFT, Action: interfaces.DRAFT_DECISION_ACCEPT_UNDECIDED,
				After: []string{"object_types/audit_log", "relation_types/customers_to_products"},
			}}

			So(undecidedItems(items), ShouldBeEmpty)
		})

		Convey("A relation not adopted because of its end is undecided too, for that reason\n", func() {
			items := draft()
			items.RelationTypes[2].ProposedBecause = "end_not_adopted"

			So(len(undecidedItems(items)), ShouldEqual, 2)
		})
	})
}
