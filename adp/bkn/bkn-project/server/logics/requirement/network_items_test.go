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

		got, omitted := messageNetworkItemsOf(items, map[string]bool{"sales_order": true})

		So(got, ShouldHaveLength, maxMessageNetworkItems)
		So(omitted, ShouldEqual, 5)
		// It keeps the id of its place among all the items.
		So(got[0].ID, ShouldEqual, fmt.Sprintf("kept-network-%d", maxMessageNetworkItems+5))
		So(got[1].ID, ShouldEqual, "kept-network-1")
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

		linkCovered(spec, items, caller, 5)

		So(spec.LogicNeeds[0].Link, ShouldBeNil)
		So(spec.LogicNeeds[1].Link, ShouldResemble, &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_OBJECT_METRIC,
			ID: "m2", By: caller, At: 5})
		So(spec.LogicNeeds[2].Link, ShouldBeNil)
		So(spec.LogicNeeds[3].Link, ShouldBeNil)

		// A proposal of any of their names is covered, and not added as new.
		So(uncovered([]agentLogicNeed{{ID: "x", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Window check"},
			{ID: "y", Kind: interfaces.LOGIC_NEED_METRIC, Name: "On-time rate"}}, items), ShouldResemble,
			[]agentLogicNeed{{ID: "y", Kind: interfaces.LOGIC_NEED_METRIC, Name: "On-time rate"}})
	})
}
