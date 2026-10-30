// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"errors"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_Rollback(t *testing.T) {
	Convey("Test rolling a project's lineage back\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ls := pmock.NewMockLineageService(ctrl)
		ra := pmock.NewMockReleaseAccess(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		rs := pmock.NewMockRequirementService(ctrl)
		// Only what a rollback reads and the switch: no bkn-backend, vega, draft or regression access is
		// wired, so importing or writing anything else fails the test.
		svc := &projectReleaseService{ps: ps, ls: ls, ra: ra, gs: gs, rs: rs}
		ctx := context.Background()
		other := interfaces.AccountInfo{ID: "u2", Type: "user"}
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "supply", Owner: reviewOwner}}
		ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil).AnyTimes()

		v120 := &interfaces.Release{ID: "r3", LineageID: "supply", NetworkID: "supply_v1_2_0", Version: "1.2.0", ProjectID: "p1", CreateTime: 3}
		v110 := &interfaces.Release{ID: "r2", LineageID: "supply", NetworkID: "supply_v1_1_0", Version: "1.1.0", ProjectID: "p1", CreateTime: 2}
		v100 := &interfaces.Release{ID: "r1", LineageID: "supply", NetworkID: "supply_v1_0_0", Version: "1.0.0", ProjectID: "p2", CreateTime: 1}
		releases := []*interfaces.Release{v120, v110, v100}
		ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "supply", Limit: interfaces.MAX_RELEASE_LIMIT}).
			DoAndReturn(func(context.Context, interfaces.ReleasesQuery) ([]*interfaces.Release, error) {
				return releases, nil
			}).AnyTimes()
		inUse := func(networkID string) {
			ra.EXPECT().GetLatestSwitch(gomock.Any(), "supply").
				Return(&interfaces.Switch{ID: "sw-" + networkID, ToNetworkID: networkID}, true, nil).AnyTimes()
		}
		schedules := []*interfaces.ActionSchedule{{ID: "s1", Name: "Nightly restock"}}
		questions := &interfaces.ScenarioQuestions{SpecVersion: 2, Questions: []interfaces.ScenarioQuestion{
			{ID: "q1", Text: "Which lots expire this week?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
				Needs: []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "lot"}}},
			{ID: "q2", Text: "Who supplies a part?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
				Needs: []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "supplier"}}},
		}}
		with := map[string][]byte{"object_types/lot.bkn": objectTypeFile("lot", "Lot"), "object_types/supplier.bkn": objectTypeFile("supplier", "Supplier")}
		without := map[string][]byte{"object_types/supplier.bkn": objectTypeFile("supplier", "Supplier")}
		expectPlan := func(fromRef string, toRef string, to string) {
			ls.EXPECT().PlanSwitch(gomock.Any(), reviewToken, "supply", to).
				Return(&interfaces.SwitchPlan{LineageID: "supply", ToNetworkID: to, Schedules: schedules}, nil)
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "supply", fromRef).Return(with, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "supply", toRef).Return(without, nil)
			gs.EXPECT().Diff(gomock.Any(), "supply", fromRef, toRef).
				Return(&interfaces.PathDiff{Added: []string{}, Changed: []string{}, Removed: []string{"object_types/lot.bkn"}}, nil)
		}

		Convey("The plan goes back to the release before the one in use, with its schedules, consumers and questions\n", func() {
			inUse("supply_v1_2_0")
			expectPlan("refs/tags/v1.2.0", "refs/tags/v1.1.0", "supply_v1_1_0")

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1")

			So(err, ShouldBeNil)
			So(plan.From, ShouldResemble, interfaces.RollbackNetwork{NetworkID: "supply_v1_2_0", Version: "1.2.0"})
			So(*plan.To, ShouldResemble, interfaces.RollbackNetwork{NetworkID: "supply_v1_1_0", Version: "1.1.0"})
			So(plan.Schedules, ShouldResemble, schedules)
			So(plan.Consumers, ShouldResemble, []string{"agents", "mcp", "workshop", "eval"})
			So(plan.Refusals, ShouldBeEmpty)
			So(plan.Signature, ShouldNotBeEmpty)
			So(plan.Questions.Unanswerable, ShouldHaveLength, 1)
			So(plan.Questions.Unanswerable[0].ID, ShouldEqual, "q1")
			So(plan.Questions.Unanswerable[0].Missing, ShouldResemble,
				[]interfaces.ReleaseItem{{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "lot", Name: "Lot"}})
		})

		Convey("Rolling back again goes further back, never to the network just left\n", func() {
			inUse("supply_v1_1_0")
			expectPlan("refs/tags/v1.1.0", "refs/tags/v1.0.0", "supply_v1_0_0")

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1")

			So(err, ShouldBeNil)
			So(plan.To.NetworkID, ShouldEqual, "supply_v1_0_0")
		})

		Convey("Before the first release is the base network, read from the lineage's first commit\n", func() {
			releases = []*interfaces.Release{v110}
			inUse("supply_v1_1_0")
			gs.EXPECT().RootCommit(gomock.Any(), "supply").Return("c0", nil)
			expectPlan("refs/tags/v1.1.0", "c0", "supply")

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1")

			So(err, ShouldBeNil)
			So(*plan.To, ShouldResemble, interfaces.RollbackNetwork{NetworkID: "supply", Base: true})
			So(plan.Refusals, ShouldBeEmpty)
		})

		Convey("The base network in use has nothing to go back to\n", func() {
			ra.EXPECT().GetLatestSwitch(gomock.Any(), "supply").Return(nil, false, nil).AnyTimes()

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1")

			So(err, ShouldBeNil)
			So(plan.From.Base, ShouldBeTrue)
			So(plan.To, ShouldBeNil)
			So(plan.Refusals[0].Code, ShouldEqual, perrors.BknProject_Rollback_NoPrevious)

			_, err = svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: plan.Signature}, reviewOwner)
			status, code, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Rollback_NoPrevious)
		})

		Convey("Another project's release in use is not this project's to roll back\n", func() {
			releases = []*interfaces.Release{v120, v110, v100}
			v120.ProjectID = "p2"
			inUse("supply_v1_2_0")

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1")

			So(err, ShouldBeNil)
			So(plan.Refusals[0].Code, ShouldEqual, perrors.BknProject_Rollback_NotProjectRelease)

			_, err = svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: plan.Signature}, reviewOwner)
			_, code, _ := errorOf(err)
			So(code, ShouldEqual, perrors.BknProject_Rollback_NotProjectRelease)
			v120.ProjectID = "p1"
		})

		Convey("The owner rolls back with the plan's signature: only the switch is made\n", func() {
			inUse("supply_v1_2_0")
			expectPlan("refs/tags/v1.2.0", "refs/tags/v1.1.0", "supply_v1_1_0")
			plan, err := svc.PlanRollback(ctx, reviewToken, "p1")
			So(err, ShouldBeNil)
			expectPlan("refs/tags/v1.2.0", "refs/tags/v1.1.0", "supply_v1_1_0")
			switched := &interfaces.Switch{ID: "sw2", FromNetworkID: "supply_v1_2_0", ToNetworkID: "supply_v1_1_0"}
			ls.EXPECT().SwitchConsumers(gomock.Any(), reviewToken, "supply",
				interfaces.SwitchRequest{ToNetworkID: "supply_v1_1_0"}, reviewOwner).Return(switched, nil)

			got, err := svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: plan.Signature}, reviewOwner)

			So(err, ShouldBeNil)
			So(got, ShouldEqual, switched)
		})

		Convey("A plan that no longer stands is refused, and so is no plan at all\n", func() {
			inUse("supply_v1_2_0")
			expectPlan("refs/tags/v1.2.0", "refs/tags/v1.1.0", "supply_v1_1_0")

			_, err := svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: "an older plan"}, reviewOwner)

			status, code, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Rollback_PlanOutdated)

			_, err = svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{}, reviewOwner)
			status, code, _ = errorOf(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Rollback_PrecheckRequired)
		})

		Convey("The signature changes with the switch in use and with the schedules\n", func() {
			latest := &interfaces.Switch{ID: "a", ToNetworkID: "x"}
			base := rollbackSignature("x", "y", latest, schedules)
			So(rollbackSignature("x", "y", &interfaces.Switch{ID: "b", ToNetworkID: "x"}, schedules), ShouldNotEqual, base)
			So(rollbackSignature("x", "y", latest, nil), ShouldNotEqual, base)
			So(rollbackSignature("x", "z", latest, schedules), ShouldNotEqual, base)
		})

		Convey("Only the owner of an unarchived project rolls back\n", func() {
			_, err := svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: "s"}, other)
			_, code, _ := errorOf(err)
			So(code, ShouldEqual, perrors.BknProject_Release_NotOwner)

			project.Archived = true
			_, err = svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: "s"}, reviewOwner)
			_, code, _ = errorOf(err)
			So(code, ShouldEqual, perrors.BknProject_Release_ProjectArchived)
			project.Archived = false
		})

		Convey("Releases are read page by page\n", func() {
			paging := &projectReleaseService{ra: ra}
			ra2 := pmock.NewMockReleaseAccess(ctrl)
			paging.ra = ra2
			full := make([]*interfaces.Release, interfaces.MAX_RELEASE_LIMIT)
			for i := range full {
				full[i] = &interfaces.Release{ID: "r", CreateTime: int64(1000 - i)}
			}
			ra2.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "supply", Limit: interfaces.MAX_RELEASE_LIMIT}).Return(full, nil)
			ra2.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "supply", Limit: interfaces.MAX_RELEASE_LIMIT,
				Cursor: &interfaces.ReleaseCursor{CreateTime: full[len(full)-1].CreateTime, ID: "r"}}).Return([]*interfaces.Release{v100}, nil)

			got, err := paging.lineageReleases(ctx, "supply")

			So(err, ShouldBeNil)
			So(got, ShouldHaveLength, interfaces.MAX_RELEASE_LIMIT+1)
		})

		Convey("A failure to read the switches is an internal error\n", func() {
			ra.EXPECT().GetLatestSwitch(gomock.Any(), "supply").Return(nil, false, errors.New("db down"))

			_, err := svc.PlanRollback(ctx, reviewToken, "p1")

			status, _, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusInternalServerError)
		})
	})
}
