// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_MessageNetworkItemsOf(t *testing.T) {
	Convey("At most maxMessageNetworkItems go to the Agent, those on the model first, the rest counted\n", t, func() {
		items := []*interfaces.LogicNetworkItem{}
		for i := range maxMessageNetworkItems + 5 {
			items = append(items, &interfaces.LogicNetworkItem{Type: interfaces.LOGIC_LINK_FUNCTION, ID: fmt.Sprint(i), Name: fmt.Sprint(i)})
		}
		items[maxMessageNetworkItems+4].OwnerObjectTypeID = "sales_order"

		got, omitted := messageNetworkItemsOf(items, map[string]string{"sales_order": "Sales order"})

		So(got, ShouldHaveLength, maxMessageNetworkItems)
		So(omitted, ShouldEqual, 5)
		// It keeps the id of its place among all the items.
		So(got[0].ID, ShouldEqual, fmt.Sprintf("kept-network-%d", maxMessageNetworkItems+5))
		So(got[1].ID, ShouldEqual, "kept-network-1")
		So(got[0].OwnerObjectType, ShouldEqual, "Sales order")
	})
}

func Test_LinkCovered(t *testing.T) {
	Convey("A need is linked only to the one item that covers it\n", t, func() {
		items := []*interfaces.LogicNetworkItem{
			{Type: interfaces.LOGIC_LINK_FUNCTION, ID: "f1", BoxID: "box1", Name: "Window check"},
			{Type: interfaces.LOGIC_LINK_FUNCTION, ID: "f1", BoxID: "box2", Name: "Window check"},
			{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: "m1", Name: "Late rate", OwnerObjectTypeID: "sales_order"},
			{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: "m2", Name: "Late rate", OwnerObjectTypeID: "shipment"},
			{Type: interfaces.LOGIC_LINK_FUNCTION, ID: "f3", BoxID: "box1", Name: "Rates"},
		}
		spec := emptySpec()
		spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
			// Of the same name in two toolboxes: left to a person.
			{ID: "n1", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "window check"},
			// Of the same name on two object types: the one on the need's.
			{ID: "n2", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late rate", ObjectTypeIDs: []string{"shipment"}},
			{ID: "n3", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late rate"},
			// An API needs a function bound from OpenAPI: left to a person.
			{ID: "n4", Kind: interfaces.LOGIC_NEED_API, Name: "Rates"},
		}

		linkCovered(spec, items, nil, caller, 5)

		So(spec.LogicNeeds[0].Link, ShouldBeNil)
		So(spec.LogicNeeds[1].Link, ShouldResemble, &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_OBJECT_METRIC,
			ID: "m2", By: caller, At: 5})
		So(spec.LogicNeeds[2].Link, ShouldBeNil)
		So(spec.LogicNeeds[3].Link, ShouldBeNil)

		// A proposal of any of their names is covered, and not added as new.
		rest, covered := uncovered([]agentLogicNeed{{ID: "x", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Window check"},
			{ID: "y", Kind: interfaces.LOGIC_NEED_METRIC, Name: "On-time rate"}}, items)
		So(rest, ShouldResemble, []agentLogicNeed{{ID: "y", Kind: interfaces.LOGIC_NEED_METRIC, Name: "On-time rate"}})
		So(covered, ShouldBeEmpty)
	})

	Convey("A need the Agent says an item covers is linked to it, not added as new\n", t, func() {
		items := []*interfaces.LogicNetworkItem{
			{Type: interfaces.LOGIC_LINK_FUNCTION, ID: "f1", BoxID: "box1", Name: "Shortage", Description: "Gross requirement by BOM"},
			{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: "m1", Name: "Available to promise"},
		}
		rest, covered := uncovered([]agentLogicNeed{
			// New ones covered by an item, itself or as a step of it, are not added.
			{ID: "x", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "BOM gross requirement", CoveredBy: "kept-network-1"},
			{ID: "y", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Promisable quantity", CoveredBy: " kept-network-2 "},
			// An id that names no item leaves the need new.
			{ID: "z", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Lead time", CoveredBy: "kept-network-9"},
			// Kept needs stay, to be linked to the item that covers them.
			{ID: "kept-n1", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Substitute first", CoveredBy: "kept-network-1"},
			{ID: "kept-n2", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Promise", CoveredBy: "kept-network-2"},
		}, items)
		So(ids(rest, func(p agentLogicNeed) string { return p.ID }), ShouldResemble, []string{"z", "kept-n1", "kept-n2"})
		So(covered, ShouldResemble, map[string]*interfaces.LogicNetworkItem{"n1": items[0], "n2": items[1]})

		spec := emptySpec()
		spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
			{ID: "n1", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Substitute first"},
			// A function is not linked to a metric.
			{ID: "n2", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Promise"},
			{ID: "n3", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Edited", Edited: true},
		}
		covered["n3"] = items[0]
		linkCovered(spec, items, covered, caller, 5)
		So(spec.LogicNeeds[0].Link, ShouldResemble, &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_FUNCTION,
			ID: "f1", BoxID: "box1", By: caller, At: 5})
		So(spec.LogicNeeds[1].Link, ShouldBeNil)
		So(spec.LogicNeeds[2].Link, ShouldBeNil)
	})
}
