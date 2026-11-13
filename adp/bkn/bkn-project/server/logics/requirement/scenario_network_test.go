// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// networkDraft is confirmedDraft on a network that already has customers and the relation of an
// order to its customer: the project added sales orders.
func networkDraft() *interfaces.Draft {
	draft := confirmedDraft()
	draft.Items.ObjectTypes[1].Existing = true
	draft.Items.RelationTypes[0].Existing = true
	return draft
}

func Test_ScenariosCoverTheWholeModel(t *testing.T) {
	Convey("Test that scenarios cover what the network already has with what is new\n", t, func() {
		Convey("An object type the network has that no question rests on is uncovered too\n", func() {
			draft := networkDraft()
			spec := &interfaces.RequirementSpec{Questions: []*interfaces.RequirementQuestion{{ID: "q1",
				Needs: []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}}}}}

			So(markOntologyChanges(spec, draft), ShouldResemble, []interfaces.ReleaseItem{
				{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "customer", Name: "Customer"}})
		})

		Convey("The message holds the network's object and relation types and its kinetic items\n", func() {
			items := []*interfaces.LogicNetworkItem{
				{Type: interfaces.LOGIC_LINK_FUNCTION, ID: "f1", BoxID: "box1", Name: "Window check", Description: "d"},
				{Type: interfaces.LOGIC_LINK_ACTION_TYPE, ID: "a1", Name: "Hold order", OwnerObjectTypeID: "sales_order"},
			}
			message, _, _, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, networkDraft(),
				emptySpec(), emptySpec(), items, "")
			So(err, ShouldBeNil)
			var msg draftMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &msg), ShouldBeNil)
			So(msg.ObjectTypes, ShouldHaveLength, 2)
			So(msg.ObjectTypes[1].ID, ShouldEqual, "customer")
			So(msg.RelationTypes, ShouldHaveLength, 1)
			// Those on the model's object types first, each by its place among the items.
			So(msg.LogicNeeds, ShouldResemble, []messageLogicNeed{
				{ID: "kept-network-2", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Hold order", ObjectTypeIDs: []string{"sales_order"},
					OwnerObjectType: "Sales order"},
				{ID: "kept-network-1", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Window check", Definition: "d"},
			})
		})

		Convey("A question on a network item rests on a need linked to it; one no question asks adds none\n", func() {
			items := []*interfaces.LogicNetworkItem{
				{Type: interfaces.LOGIC_LINK_ACTION_TYPE, ID: "a1", Name: "Hold order", OwnerObjectTypeID: "sales_order"},
				{Type: interfaces.LOGIC_LINK_FUNCTION, ID: "f1", BoxID: "box1", Name: "Window check"},
				{Type: interfaces.LOGIC_LINK_SKILL, ID: "s1", Name: "Triage"},
			}
			spec := emptySpec()
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{{ID: "n1", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Window check",
				Link:       &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_FUNCTION, ID: "f1", BoxID: "box1"},
				ConceptIDs: []string{}, ScenarioIDs: []string{}}}
			spec.LastIDs = map[string]int{"n": 1}
			out := &draftOutput{Questions: []agentQuestion{
				{Text: "Which orders can be put on hold?", Type: "filter", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY,
					LogicNeedIDs: []string{"kept-network-1"}, SupplementReason: "r"},
				// The network's function the specification already links to is that need.
				{Text: "Is the window open?", Type: "judge", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY,
					LogicNeedIDs: []string{"kept-network-2", "kept-network-9"}, SupplementReason: "r"},
			}}
			caller := interfaces.AccountInfo{ID: "u1", Type: "user"}

			added := needsOfNetworkItems(out, spec, items, networkDraft(), caller, 1000)
			buildSpec(out, nil, networkDraft(), spec)
			leaveUnasked(spec, added)

			So(ids(spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n1", "n2"})
			hold := spec.LogicNeeds[1]
			So(hold.Kind, ShouldEqual, interfaces.LOGIC_NEED_ACTION_TYPE)
			So(hold.Name, ShouldEqual, "Hold order")
			So(hold.ObjectTypeIDs, ShouldResemble, []string{"sales_order"})
			So(hold.Source, ShouldEqual, interfaces.LOGIC_NEED_FROM_SCENARIOS)
			So(hold.Link, ShouldResemble, &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_ACTION_TYPE, ID: "a1",
				By: caller, At: 1000})
			So(spec.Questions[0].LogicNeedIDs, ShouldResemble, []string{"n2"})
			So(spec.Questions[1].LogicNeedIDs, ShouldResemble, []string{"n1"})
			So(hold.ScenarioIDs, ShouldResemble, []string{})
		})

		Convey("A need added for a question drafting dropped is taken back\n", func() {
			items := []*interfaces.LogicNetworkItem{{Type: interfaces.LOGIC_LINK_SKILL, ID: "s1", Name: "Triage"}}
			spec := emptySpec()
			// No citation and no reason: the question is dropped.
			out := &draftOutput{Questions: []agentQuestion{{Text: "Triage this?", Type: "judge",
				Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY, LogicNeedIDs: []string{"kept-network-1"}}}}

			added := needsOfNetworkItems(out, spec, items, networkDraft(), interfaces.AccountInfo{}, 1000)
			buildSpec(out, nil, networkDraft(), spec)
			leaveUnasked(spec, added)

			So(spec.Questions, ShouldBeEmpty)
			So(spec.LogicNeeds, ShouldBeEmpty)
		})
	})
}

func Test_ScenarioDraftingOnNetworks(t *testing.T) {
	Convey("Test drafting scenarios on a new network and on one that already runs\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		kva := pmock.NewMockKNVersionAccess(ctrl)
		bba := pmock.NewMockBknBackendAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba, rla: rla, kva: kva, bba: bba,
			now: func() int64 { return 1000 }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller,
			CatalogIDs: []string{"c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(nil, nil)
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)
		ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), int64(0)).Return(true, nil)
		bba.EXPECT().GetKN(gomock.Any(), token, "kn1").Return(&interfaces.KnowledgeNetwork{}, nil)
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return(nil, nil)
		rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
		var finished *interfaces.Requirement
		ra.EXPECT().FinishDrafting(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *interfaces.Requirement) (bool, error) {
				finished = req
				return true, nil
			})
		answer := func(output string, check func(msg draftMessage)) {
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
					var msg draftMessage
					So(json.Unmarshal([]byte(req.Message[strings.Index(req.Message, "{"):]), &msg), ShouldBeNil)
					check(msg)
					return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil
				})
		}

		Convey("On a new network scenarios rest on the new model alone, as before\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmedDraft(), true, nil).Times(2)
			kva.EXPECT().GetVersioning(gomock.Any(), token, "kn1").Return(&interfaces.KNVersioning{Enabled: true}, nil)
			answer(`{"scenarios":[],"concepts":[],"concept_maps":[],"questions":[{"text":"Which orders does a customer have?",
				"type":"filter","layer":"knowledge","logic_need_ids":[],"supplement_reason":"r","citations":[],"inputs":[]}]}`,
				func(msg draftMessage) {
					So(msg.ObjectTypes, ShouldHaveLength, 2)
					So(msg.LogicNeeds, ShouldBeEmpty)
				})

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_READY)
			So(finished.Spec.Questions, ShouldHaveLength, 1)
			So(finished.Spec.LogicNeeds, ShouldBeEmpty)
		})

		Convey("On a running network scenarios rest on what it has as well\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(networkDraft(), true, nil).Times(2)
			kva.EXPECT().GetVersioning(gomock.Any(), token, "kn1").Return(&interfaces.KNVersioning{Enabled: true, RunningVersion: 3}, nil)
			bba.EXPECT().ListActionTypes(gomock.Any(), token, "kn1", "").Return(
				[]*interfaces.KNActionType{{ID: "a1", Name: "Hold order", ObjectTypeID: "sales_order"}}, nil)
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_FUNCTION, "").Return(nil, nil)
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_SKILL, "").Return(nil, nil)
			bba.EXPECT().ListObjectMetrics(gomock.Any(), token, "kn1", "").Return(nil, nil)
			answer(`{"scenarios":[],"concepts":[],"concept_maps":[],"questions":[{"text":"Which orders can be put on hold?",
				"type":"filter","layer":"capability","logic_need_ids":["kept-network-1"],"supplement_reason":"r","citations":[],"inputs":[]}]}`,
				func(msg draftMessage) {
					// The network's customers and their relation go with the new sales orders.
					So(msg.ObjectTypes, ShouldHaveLength, 2)
					So(msg.RelationTypes, ShouldHaveLength, 1)
					So(msg.LogicNeeds, ShouldResemble, []messageLogicNeed{{ID: "kept-network-1",
						Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Hold order", ObjectTypeIDs: []string{"sales_order"},
						OwnerObjectType: "Sales order"}})
				})

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_READY)
			So(finished.Spec.LogicNeeds, ShouldHaveLength, 1)
			So(finished.Spec.LogicNeeds[0].Link.ID, ShouldEqual, "a1")
			So(finished.Spec.Questions[0].LogicNeedIDs, ShouldResemble, []string{finished.Spec.LogicNeeds[0].ID})
			So(finished.Spec.Questions[0].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
		})
	})
}
