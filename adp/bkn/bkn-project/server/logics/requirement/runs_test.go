// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// runStore keeps a project's specification as the store does for the concepts and kinetic runs:
// read as a copy, a run started and finished in place.
type runStore struct {
	req *interfaces.Requirement
	// started is the run StartRun recorded, and readUpdateTime the update time it was given.
	started        *interfaces.RequirementRun
	readUpdateTime int64
}

func (st *runStore) read() (*interfaces.Requirement, bool, error) {
	if st.req == nil {
		return nil, false, nil
	}
	body, err := json.Marshal(st.req)
	if err != nil {
		return nil, false, err
	}
	out := &interfaces.Requirement{}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// expect answers the reads and the start and end of runs from the store.
func (st *runStore) expect(ra *pmock.MockRequirementAccess) {
	ra.EXPECT().GetRequirement(gomock.Any(), "p1").DoAndReturn(
		func(context.Context, string) (*interfaces.Requirement, bool, error) { return st.read() }).AnyTimes()
	ra.EXPECT().StartRun(gomock.Any(), "p1", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, kind string, run *interfaces.RequirementRun, _ int64, readUpdateTime int64) (bool, error) {
			st.started, st.readUpdateTime = run, readUpdateTime
			if st.req == nil {
				st.req = &interfaces.Requirement{ProjectID: "p1", Version: 1, Status: interfaces.REQUIREMENT_STATUS_READY,
					Spec: *emptySpec()}
			}
			if st.req.Spec.Runs == nil {
				st.req.Spec.Runs = map[string]*interfaces.RequirementRun{}
			}
			copied := *run
			st.req.Spec.Runs[kind] = &copied
			st.req.UpdateTime = run.StartTime
			return true, nil
		}).AnyTimes()
	ra.EXPECT().FinishRun(gomock.Any(), "p1", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, kind string, runID string, finish func(*interfaces.Requirement)) (bool, error) {
			run := st.req.Spec.Runs[kind]
			if run == nil || run.RunID != runID || run.Status != interfaces.REQUIREMENT_RUN_RUNNING {
				return false, nil
			}
			finish(st.req)
			return true, nil
		}).AnyTimes()
}

func Test_FailInterruptedRuns(t *testing.T) {
	Convey("Test failing the runs a previous run of the service left going\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ra := pmock.NewMockRequirementAccess(ctrl)
		svc := &requirementService{ra: ra, now: func() int64 { return 1000 }}
		ctx := context.Background()

		Convey("Every run still running is failed as interrupted; ready ones and other projects' stay\n", func() {
			st := &runStore{req: &interfaces.Requirement{ProjectID: "p1", Status: interfaces.REQUIREMENT_STATUS_READY, UpdateTime: 5,
				Spec: interfaces.RequirementSpec{Rules: []*interfaces.RequirementRule{{ID: "r1"}},
					Runs: map[string]*interfaces.RequirementRun{
						interfaces.REQUIREMENT_RUN_CONCEPTS: {Status: interfaces.REQUIREMENT_RUN_RUNNING, RunID: "a", StartTime: 5},
						interfaces.REQUIREMENT_RUN_KINETIC:  {Status: interfaces.REQUIREMENT_RUN_READY, RunID: "b"},
					}}}}
			st.expect(ra)
			ra.EXPECT().ProjectsWithRunning(gomock.Any()).Return([]string{"p1"}, nil)

			n, err := svc.failInterruptedRuns(ctx)

			So(err, ShouldBeNil)
			So(n, ShouldEqual, 1)
			concepts := st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS]
			So(concepts.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_FAILED)
			So(concepts.Error, ShouldEqual, failureInterrupted)
			So(concepts.UpdateTime, ShouldEqual, 1000)
			So(st.req.UpdateTime, ShouldEqual, 1000)
			So(st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_KINETIC].Status, ShouldEqual, interfaces.REQUIREMENT_RUN_READY)
			So(st.req.Spec.Rules, ShouldHaveLength, 1)
		})

		Convey("A project gone meanwhile is passed over, and a failing store says so\n", func() {
			ra.EXPECT().ProjectsWithRunning(gomock.Any()).Return([]string{"gone"}, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "gone").Return(nil, false, nil)
			n, err := svc.failInterruptedRuns(ctx)
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 0)

			ra.EXPECT().ProjectsWithRunning(gomock.Any()).Return(nil, errors.New("db down"))
			_, err = svc.failInterruptedRuns(ctx)
			So(err, ShouldNotBeNil)
		})

		Convey("FailInterrupted fails them after draftings, before guidance\n", func() {
			ra.EXPECT().FailDrafting(gomock.Any(), failureInterrupted, int64(1000)).Return(int64(0), nil)
			ra.EXPECT().ProjectsWithRunning(gomock.Any()).Return(nil, errors.New("db down"))

			So(svc.FailInterrupted(ctx), ShouldNotBeNil)
		})
	})
}

func Test_RunGates(t *testing.T) {
	Convey("Test what the runs rest on\n", t, func() {
		Convey("A specification an ontology confirmation was made against is written at the next version\n", func() {
			confirmedAt := func(v int) *interfaces.Draft {
				return &interfaces.Draft{Items: interfaces.DraftItems{Confirmation: &interfaces.DraftConfirmation{RequirementVersion: v}}}
			}
			So(nextVersion(0, nil), ShouldEqual, 1)
			So(nextVersion(0, confirmedAt(0)), ShouldEqual, 1)
			So(nextVersion(3, nil), ShouldEqual, 3)
			So(nextVersion(3, testDraft()), ShouldEqual, 3)
			So(nextVersion(3, confirmedAt(3)), ShouldEqual, 4)
			So(nextVersion(3, confirmedAt(2)), ShouldEqual, 3)
		})

		Convey("The model is confirmed when its draft is ready, confirmed and adopts an object type\n", func() {
			notReady := confirmedDraft()
			notReady.Status = interfaces.DRAFT_STATUS_DRAFTING
			noneAdopted := confirmedDraft()
			for _, ot := range noneAdopted.Items.ObjectTypes {
				ot.Adopted = false
			}
			So(modelConfirmed(confirmedDraft()), ShouldBeTrue)
			So(modelConfirmed(nil), ShouldBeFalse)
			So(modelConfirmed(testDraft()), ShouldBeFalse)
			So(modelConfirmed(notReady), ShouldBeFalse)
			So(modelConfirmed(noneAdopted), ShouldBeFalse)
			// A confirmation under way or failed has committed nothing.
			uncommitted := confirmedDraft()
			uncommitted.Items.Confirmation.Commit = ""
			So(modelConfirmed(uncommitted), ShouldBeFalse)
		})

		Convey("Rejected items of other runs are kept for a run to list its own after\n", func() {
			rejected := []*interfaces.RequirementRejected{{Text: "a"}, {Text: "b", Run: interfaces.REQUIREMENT_RUN_CONCEPTS},
				{Text: "c", Run: interfaces.REQUIREMENT_RUN_KINETIC}}

			So(rejectedOf(rejected, interfaces.REQUIREMENT_RUN_CONCEPTS), ShouldResemble,
				[]*interfaces.RequirementRejected{rejected[0], rejected[2]})
			So(rejectedOf(rejected, ""), ShouldResemble, []*interfaces.RequirementRejected{rejected[1], rejected[2]})
		})
	})
}

func Test_MapConcepts(t *testing.T) {
	Convey("Test mapping concepts to the adopted items by name\n", t, func() {
		order := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}
		customer := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"}
		relation := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "sales_order_customer"}

		Convey("A concept maps to the one adopted item of its name or alias, by its category\n", func() {
			draft := testDraft()
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes,
				// Two items called Account: the name says neither.
				&interfaces.DraftObjectType{ID: "acc1", Name: "Account", Adopted: true},
				&interfaces.DraftObjectType{ID: "acc2", Name: "Ledger", Aliases: []string{"account"}, Adopted: true},
				// A candidate no one adopted is not the model.
				&interfaces.DraftObjectType{ID: "carrier", Name: "Carrier"})
			spec := emptySpec()
			spec.Concepts = []*interfaces.RequirementConcept{
				{ID: "c1", Term: " order ", Category: "object"},
				{ID: "c2", Term: "Customer", Category: "event"},
				{ID: "c3", Term: "Order of customer", Category: "relation"},
				{ID: "c4", Term: "Account", Category: "object"},
				{ID: "c5", Term: "Carrier", Category: "object"},
				// A property, a kinetic need or an evidence is no item, whatever its name.
				{ID: "c6", Term: "Customer", Category: "property"},
				{ID: "c7", Term: "Customer", Category: interfaces.REQUIREMENT_CONCEPT_KINETIC},
				// What a person changed or someone mapped stays.
				{ID: "c8", Term: "Customer", Category: "object", Edited: true},
				{ID: "c9", Term: "Customer", Category: "object", MapsTo: &order},
				// A relation is matched by its name only, against relation types.
				{ID: "c10", Term: "Customer", Category: "relation"},
			}
			spec.Questions = []*interfaces.RequirementQuestion{{ID: "q1", Basis: interfaces.RequirementBasis{ConceptIDs: []string{"c1", "c3"}},
				Needs: []interfaces.RequirementItemRef{}, NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS}}

			mapConcepts(spec, draft)

			maps := []*interfaces.RequirementItemRef{}
			for _, c := range spec.Concepts {
				maps = append(maps, c.MapsTo)
			}
			So(maps, ShouldResemble, []*interfaces.RequirementItemRef{&order, &customer, &relation, nil, nil, nil, nil, nil, &order, nil})
			// The questions' needs follow.
			So(spec.Questions[0].Needs, ShouldResemble, []interfaces.RequirementItemRef{order, relation})
		})

		Convey("Without a model nothing is mapped\n", func() {
			spec := emptySpec()
			spec.Concepts = []*interfaces.RequirementConcept{{ID: "c1", Term: "Order", Category: "object"}}

			mapConcepts(spec, nil)

			So(spec.Concepts[0].MapsTo, ShouldBeNil)
		})
	})
}

func Test_KeepRecordsDroppedIDs(t *testing.T) {
	Convey("Runs never give again the id of an item they drop, on a specification without last ids\n", t, func() {
		spec := &interfaces.RequirementSpec{
			Concepts:   []*interfaces.RequirementConcept{{ID: "c1", Term: "old", Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS}},
			LogicNeeds: []*interfaces.RequirementLogicNeed{{ID: "n1", Kind: interfaces.LOGIC_NEED_METRIC, Name: "old", Source: interfaces.LOGIC_NEED_FROM_ANALYSIS}},
		}
		keepForConcepts(spec, false)
		So(spec.Concepts, ShouldBeEmpty)
		So(nextID(spec, "c", spec.Concepts, conceptID), ShouldEqual, "c2")
		keepForKinetic(spec, false)
		So(spec.LogicNeeds, ShouldBeEmpty)
		So(nextID(spec, "n", spec.LogicNeeds, logicNeedID), ShouldEqual, "n2")
	})
}

func Test_RunOutlivedByEdits(t *testing.T) {
	Convey("A run that ends after people edited the specification writes nothing over their edits\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ra := pmock.NewMockRequirementAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &requirementService{ra: ra, ba: ba, now: func() int64 { return 1000 }}
		ctx := context.Background()
		inputs := &interfaces.RequirementInputs{Documents: []interfaces.RequirementInputDocument{{ID: "d1", SHA256: "a"}}}
		run := &interfaces.RequirementRun{Status: interfaces.REQUIREMENT_RUN_RUNNING, RunID: "r", StartTime: 5, Inputs: inputs}
		st := &runStore{req: &interfaces.Requirement{ProjectID: "p1", Status: interfaces.REQUIREMENT_STATUS_READY,
			Spec: interfaces.RequirementSpec{Rules: []*interfaces.RequirementRule{{ID: "r1", Statement: "edited", Edited: true}},
				Runs: map[string]*interfaces.RequirementRun{interfaces.REQUIREMENT_RUN_CONCEPTS: run}}}}
		st.expect(ra)
		ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
			Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: "{}"}, nil)
		wrote := false
		p := &partRun{kind: interfaces.REQUIREMENT_RUN_CONCEPTS, projectID: "p1", run: run, caller: caller,
			newOut:         func() any { return &conceptsOutput{} },
			prepareMessage: func(context.Context) (string, error) { return "m", nil },
			check:          func(context.Context, any) {},
			write:          func(*interfaces.Requirement, any) { wrote = true },
			inputs:         &interfaces.RequirementInputs{Documents: []interfaces.RequirementInputDocument{{ID: "d1", SHA256: "b"}}}}

		// An edit after the run outlived its time moved the update time on.
		st.req.UpdateTime = 9
		svc.execute(ctx, p)

		So(wrote, ShouldBeFalse)
		ended := st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS]
		So(ended.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_FAILED)
		So(ended.Error, ShouldEqual, failureInterrupted)
		// The inputs stay the last ready run's.
		So(ended.Inputs, ShouldResemble, inputs)
		So(st.req.Spec.Rules[0].Statement, ShouldEqual, "edited")
	})

	Convey("A run started over a specification keeps the inputs of the last run that ended ready\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ra := pmock.NewMockRequirementAccess(ctrl)
		svc := &requirementService{ra: ra, now: func() int64 { return 1000 }, newID: func() string { return "new" }}
		inputs := &interfaces.RequirementInputs{Documents: []interfaces.RequirementInputDocument{{ID: "d1"}}}
		previous := &interfaces.Requirement{UpdateTime: 7, Spec: interfaces.RequirementSpec{Runs: map[string]*interfaces.RequirementRun{
			interfaces.REQUIREMENT_RUN_CONCEPTS: {Status: interfaces.REQUIREMENT_RUN_FAILED, Inputs: inputs}}}}
		ra.EXPECT().StartRun(gomock.Any(), "p1", interfaces.REQUIREMENT_RUN_CONCEPTS, gomock.Any(), gomock.Any(), int64(7)).Return(true, nil)

		run, err := svc.startRun(context.Background(), "p1", interfaces.REQUIREMENT_RUN_CONCEPTS, caller, previous)

		So(err, ShouldBeNil)
		So(run.Inputs, ShouldEqual, inputs)
		So(run.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_RUNNING)
	})
}
