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
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// conversationStore keeps conversations as the database does, a copy each read and write.
type conversationStore struct {
	list []*interfaces.RequirementConversation
}

func copyConversation(c *interfaces.RequirementConversation) *interfaces.RequirementConversation {
	raw, _ := json.Marshal(c)
	out := &interfaces.RequirementConversation{}
	_ = json.Unmarshal(raw, out)
	return out
}

func (cs *conversationStore) expect(ra *pmock.MockRequirementAccess) {
	ra.EXPECT().GetConversation(gomock.Any(), "p1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, id string) (*interfaces.RequirementConversation, bool, error) {
			for _, c := range cs.list {
				if c.ID == id {
					return copyConversation(c), true, nil
				}
			}
			return nil, false, nil
		}).AnyTimes()
	ra.EXPECT().CreateConversation(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, c *interfaces.RequirementConversation) error {
			cs.list = append(cs.list, copyConversation(c))
			return nil
		}).AnyTimes()
	ra.EXPECT().UpdateConversation(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, c *interfaces.RequirementConversation, read int64) (bool, error) {
			for i, stored := range cs.list {
				if stored.ID == c.ID && stored.UpdateTime == read {
					cs.list[i] = copyConversation(c)
					return true, nil
				}
			}
			return false, nil
		}).AnyTimes()
}

// askSpec is a specification with two metrics of one meaning, a function, and a question on one.
func askSpec() *interfaces.Requirement {
	need := func(id, kind, name string) *interfaces.RequirementLogicNeed {
		return &interfaces.RequirementLogicNeed{ID: id, Kind: kind, Name: name, Source: interfaces.LOGIC_NEED_FROM_ANALYSIS,
			ObjectTypeIDs: []string{}, Properties: []interfaces.RequirementPropertyRef{}, ConceptIDs: []string{},
			ScenarioIDs: []string{"s1"}, Citations: []interfaces.RequirementCitation{}}
	}
	spec := emptySpec()
	spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
		need("n1", interfaces.LOGIC_NEED_METRIC, "Late rate"),
		need("n2", interfaces.LOGIC_NEED_METRIC, "On-time rate"),
		need("n3", interfaces.LOGIC_NEED_FUNCTION, "Kit check"),
	}
	spec.Scenarios = []*interfaces.RequirementScenario{{ID: "s1", Name: "Chase suppliers", Knowledge: []string{},
		Citations: []interfaces.RequirementCitation{}}}
	spec.Questions = []*interfaces.RequirementQuestion{{ID: "q1", Text: "Which suppliers are late?", ScenarioIDs: []string{"s1"},
		LogicNeedIDs: []string{"n1"}, Basis: interfaces.RequirementBasis{ConceptIDs: []string{}},
		Needs: []interfaces.RequirementItemRef{}, Citations: []interfaces.RequirementCitation{}}}
	spec.LastIDs = map[string]int{"n": 3, "s": 1, "q": 1}
	return &interfaces.Requirement{ProjectID: "p1", Version: 1, Status: interfaces.REQUIREMENT_STATUS_READY, Spec: *spec,
		UpdateTime: 7}
}

func Test_CheckProposals(t *testing.T) {
	Convey("Proposals are checked against the specification as asked\n", t, func() {
		spec := &askSpec().Spec
		draft := kineticDraft()
		items := []*interfaces.LogicNetworkItem{
			{Type: interfaces.LOGIC_LINK_FUNCTION, ID: "f1", BoxID: "b1", Name: "Kit check"},
			{Type: interfaces.LOGIC_LINK_SKILL, ID: "k1", Name: "Chase"},
		}
		proposed := []agentProposal{
			{Op: "add", Kind: "logic_needs", Item: &interfaces.RequirementProposalItem{LogicKind: "metric", Name: "Line load",
				ObjectTypeIDs: []string{"customer", "carrier"}}, Reason: " Asked for. "},
			// Repeats a need the specification has.
			{Op: "add", Kind: "logic_needs", Item: &interfaces.RequirementProposalItem{LogicKind: "metric", Name: "late rate"}},
			{Op: "merge", Kind: "logic_needs", TargetID: "n1", IntoID: "n2",
				Item: &interfaces.RequirementProposalItem{Definition: "Share on time"}},
			// n2 was merged into already.
			{Op: "delete", Kind: "logic_needs", TargetID: "n2"},
			{Op: "link", Kind: "logic_needs", TargetID: "n3", NetworkItemID: "kept-network-1"},
			// A function does not link to a Skill, nor is a scenario of this page.
			{Op: "modify", Kind: "scenarios", TargetID: "s1", Item: &interfaces.RequirementProposalItem{Name: "x"}},
			{Op: "modify", Kind: "logic_needs", TargetID: "n9", Item: &interfaces.RequirementProposalItem{Name: "x"}},
		}

		kept, dropped := checkProposals(interfaces.REQUIREMENT_PAGE_KINETIC, proposed, spec, draft, items)

		So(dropped, ShouldEqual, 4)
		So(len(kept), ShouldEqual, 3)
		So(kept[0].Item.ObjectTypeIDs, ShouldResemble, []string{"customer"})
		So(kept[0].Reason, ShouldEqual, "Asked for.")
		So(kept[1].IntoID, ShouldEqual, "n2")
		So(kept[1].Before, ShouldEqual, spec.LogicNeeds[0])
		So(kept[2].NetworkItem, ShouldResemble, &interfaces.RequirementProposalNetwork{Type: "function", ID: "f1",
			BoxID: "b1", Name: "Kit check"})

		Convey("A split keeps two parts and moves only the scenario's questions\n", func() {
			kept, dropped := checkProposals(interfaces.REQUIREMENT_PAGE_SCENARIOS, []agentProposal{{Op: "split",
				Kind: "scenarios", TargetID: "s1", Parts: []interfaces.RequirementProposalItem{{Name: "Chase"}, {Name: "Review"}},
				Moves: []interfaces.RequirementProposalMove{{QuestionID: "q1", Part: 1}, {QuestionID: "q9", Part: 0}}}},
				spec, draft, nil)
			So(dropped, ShouldEqual, 0)
			So(kept[0].Moves, ShouldResemble, []interfaces.RequirementProposalMove{{QuestionID: "q1", Part: 1}})
		})
	})
}

func Test_AskAndAdopt(t *testing.T) {
	Convey("A question is answered in the background, and its proposals adopted into the list\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		nextID := 0
		svc := &requirementService{ps: ps, ra: ra, da: da, ba: ba, rla: rla,
			now:   func() int64 { return time.Now().UnixMilli() },
			newID: func() string { nextID++; return "id" + string(rune('0'+nextID)) },
			run:   func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller,
			NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).AnyTimes()
		ra.EXPECT().GetSupplement(gomock.Any(), "p1").Return(&interfaces.RequirementSupplement{Text: "Planning only."}, nil).AnyTimes()
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(nil, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(kineticDraft(), true, nil).AnyTimes()
		rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		st := &runStore{req: askSpec()}
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").DoAndReturn(
			func(context.Context, string) (*interfaces.Requirement, bool, error) { return st.read() }).AnyTimes()
		ra.EXPECT().UpdateRequirement(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *interfaces.Requirement, read int64) (bool, error) {
				if st.req.UpdateTime != read {
					return false, nil
				}
				st.req = req
				return true, nil
			}).AnyTimes()
		cs := &conversationStore{}
		cs.expect(ra)
		output := `{"answer":"Keep the on-time rate; a late rate is its complement.","proposals":[
			{"op":"merge","kind":"logic_needs","target_id":"n1","into_id":"n2","item":{"definition":"Share of lines on time"},"reason":"Same measure."},
			{"op":"add","kind":"logic_needs","item":{"logic_kind":"metric","name":"Line load"},"reason":"Asked for."},
			{"op":"add","kind":"scenarios","item":{"name":"Nope"},"reason":"Not this page."}]}`
		ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
				So(req.Message, ShouldStartWith, "pass ask\nlanguage: en-US\n")
				var msg askMessage
				So(json.Unmarshal([]byte(req.Message[strings.Index(req.Message, "{"):]), &msg), ShouldBeNil)
				So(msg.Supplement, ShouldEqual, "Planning only.")
				So(msg.Refs, ShouldResemble, []string{"n1", "n2"})
				So(msg.Editable, ShouldResemble, map[string][]string{"logic_needs": {"add", "modify", "merge", "delete", "link"}})
				So(len(msg.LogicNeeds), ShouldEqual, 3)
				return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil
			})

		c, err := svc.Ask(ctx, token, "p1", "", interfaces.RequirementAskRequest{Page: "kinetic", Question: "Merge these two",
			Refs: []interfaces.RequirementAskRef{{Kind: "logic_needs", ID: "n1"}, {Kind: "logic_needs", ID: "n2"}}}, caller)

		So(err, ShouldBeNil)
		So(c.Title, ShouldEqual, "Merge these two")
		So(c.Turns[0].Refs[0].Name, ShouldEqual, "Late rate")
		read, err := svc.GetConversation(ctx, token, "p1", c.ID)
		So(err, ShouldBeNil)
		turn := read.Turns[0]
		So(turn.Status, ShouldEqual, interfaces.REQUIREMENT_TURN_READY)
		So(turn.Answer, ShouldStartWith, "Keep the on-time rate")
		So(len(turn.Proposals), ShouldEqual, 2)
		So(turn.Dropped, ShouldEqual, 1)

		Convey("A conversation answers one question at a time\n", func() {
			cs.list[0].Turns[0].Status = interfaces.REQUIREMENT_TURN_PENDING
			_, err := svc.Ask(ctx, token, "p1", c.ID, interfaces.RequirementAskRequest{Question: "And?"}, caller)
			So(err.(*httperrors.HTTPError).BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Requirement_AskPending)
		})

		Convey("Adopting all writes them as accepted, the merged need's questions follow\n", func() {
			result, err := svc.DecideProposals(ctx, token, "p1", c.ID, turn.ID,
				interfaces.RequirementProposalsDecision{UpdateTime: 7, Adopt: []int{0, 1}}, caller)

			So(err, ShouldBeNil)
			spec := st.req.Spec
			So(ids(spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n2", "n3", "n4"})
			So(spec.LogicNeeds[0].Definition, ShouldEqual, "Share of lines on time")
			So(spec.LogicNeeds[0].Accepted, ShouldBeTrue)
			So(spec.Questions[0].LogicNeedIDs, ShouldResemble, []string{"n2"})
			So(spec.LogicNeeds[2].Source, ShouldEqual, interfaces.REQUIREMENT_SOURCE_ASK)
			So(spec.LogicNeeds[2].Accepted, ShouldBeTrue)
			So(result.Conversation.Turns[0].Proposals[0].Status, ShouldEqual, interfaces.REQUIREMENT_PROPOSAL_ADOPTED)
			So(result.Conversation.Turns[0].Proposals[1].ResultID, ShouldEqual, "n4")

			// Analysing again keeps what was adopted, as what a person accepted; starting over keeps only
			// what a question rests on.
			again := cloneSpec(&spec)
			keepForKinetic(again, false)
			So(ids(again.LogicNeeds, logicNeedID), ShouldResemble, []string{"n2", "n4"})
			keepForKinetic(again, true)
			So(ids(again.LogicNeeds, logicNeedID), ShouldResemble, []string{"n2"})

			Convey("An add whose item is in the list already is not added twice, as when its decision was not recorded\n", func() {
				cs.list[0].Turns[0].Proposals[1].Status = ""
				_, err := svc.DecideProposals(ctx, token, "p1", c.ID, turn.ID,
					interfaces.RequirementProposalsDecision{UpdateTime: st.req.UpdateTime, Adopt: []int{1}}, caller)
				So(err.(*httperrors.HTTPError).BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Requirement_ProposalChanged)
				So(len(st.req.Spec.LogicNeeds), ShouldEqual, 3)
			})

			Convey("A proposal decided once is not decided again\n", func() {
				_, err := svc.DecideProposals(ctx, token, "p1", c.ID, turn.ID,
					interfaces.RequirementProposalsDecision{UpdateTime: st.req.UpdateTime, Decline: []int{0}}, caller)
				So(err.(*httperrors.HTTPError).BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Requirement_ProposalDecided)
			})
		})

		Convey("A proposal whose item changed since it was asked is refused\n", func() {
			st.req.Spec.LogicNeeds[0].Name = "Delay rate"
			_, err := svc.DecideProposals(ctx, token, "p1", c.ID, turn.ID,
				interfaces.RequirementProposalsDecision{UpdateTime: 7, Adopt: []int{0, 1}}, caller)

			So(err.(*httperrors.HTTPError).BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Requirement_ProposalChanged)
			So(len(st.req.Spec.LogicNeeds), ShouldEqual, 3)
		})

		Convey("Declining changes nothing in the list\n", func() {
			result, err := svc.DecideProposals(ctx, token, "p1", c.ID, turn.ID,
				interfaces.RequirementProposalsDecision{Decline: []int{1}}, caller)

			So(err, ShouldBeNil)
			So(result.Requirement, ShouldBeNil)
			So(result.Conversation.Turns[0].Proposals[1].Status, ShouldEqual, interfaces.REQUIREMENT_PROPOSAL_DECLINED)
			So(st.req.UpdateTime, ShouldEqual, 7)
		})
	})

	Convey("A turn the Agent fails reads as failed, and a ref the list does not have is refused\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, ba: ba, rla: rla,
			now: func() int64 { return time.Now().UnixMilli() }, newID: func() string { return "x" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).AnyTimes()
		ra.EXPECT().GetSupplement(gomock.Any(), "p1").Return(nil, errors.New("down")).AnyTimes()
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(nil, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		cs := &conversationStore{}
		cs.expect(ra)
		ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(nil, errors.New("down"))

		_, err := svc.Ask(ctx, token, "p1", "", interfaces.RequirementAskRequest{Page: "scenarios", Question: "Why?",
			Refs: []interfaces.RequirementAskRef{{Kind: "scenarios", ID: "s9"}}}, caller)
		So(err.(*httperrors.HTTPError).BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Requirement_InvalidItem)

		c, err := svc.Ask(ctx, token, "p1", "", interfaces.RequirementAskRequest{Page: "scenarios", Question: "Why?"}, caller)
		So(err, ShouldBeNil)
		So(cs.list[0].Turns[0].Status, ShouldEqual, interfaces.REQUIREMENT_TURN_FAILED)
		So(cs.list[0].Turns[0].Error, ShouldEqual, failureAgentUnavailable)
		So(c.Page, ShouldEqual, interfaces.REQUIREMENT_PAGE_SCENARIOS)
	})
}
