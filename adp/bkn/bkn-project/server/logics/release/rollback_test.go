// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_Rollback(t *testing.T) {
	Convey("Test rolling a project's network back to an earlier version\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockReleaseAccess(ctrl)
		kva := pmock.NewMockKNVersionAccess(ctrl)
		rs := pmock.NewMockRequirementService(ctrl)
		// Only what a rollback reads and the activation: no lineage, git, draft or regression access is
		// wired, so importing or writing anything else fails the test.
		svc := &projectReleaseService{ps: ps, ra: ra, kva: kva, rs: rs}
		ctx := context.Background()
		other := interfaces.AccountInfo{ID: "u2", Type: "user"}
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "supply", Owner: reviewOwner}}
		ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil).AnyTimes()

		published := interfaces.RELEASE_STATE_PUBLISHED
		r3 := &interfaces.Release{ID: "r3", LineageID: "supply", Version: "1.2.0", ProjectID: "p1", State: published, KNVersion: 3, CreateTime: 3}
		r2 := &interfaces.Release{ID: "r2", LineageID: "supply", Version: "1.1.0", ProjectID: "p1", State: published, KNVersion: 2, CreateTime: 2}
		r1 := &interfaces.Release{ID: "r1", LineageID: "supply", Version: "1.0.0", ProjectID: "p2", State: published, KNVersion: 1, CreateTime: 1}
		// A prepared release has no network version yet and names none.
		r4 := &interfaces.Release{ID: "r4", LineageID: "supply", Version: "1.3.0", ProjectID: "p1",
			State: interfaces.RELEASE_STATE_PREPARED, CreateTime: 4}
		releases := []*interfaces.Release{r4, r3, r2, r1}
		ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "supply", Limit: interfaces.MAX_RELEASE_LIMIT}).
			DoAndReturn(func(context.Context, interfaces.ReleasesQuery) ([]*interfaces.Release, error) {
				return releases, nil
			}).AnyTimes()

		versioning := &interfaces.KNVersioning{Enabled: true, RunningVersion: 3, PreviousVersion: 2}
		kva.EXPECT().GetVersioning(gomock.Any(), reviewToken, "supply").
			DoAndReturn(func(context.Context, string, string) (*interfaces.KNVersioning, error) {
				return versioning, nil
			}).AnyTimes()
		source := interfaces.KNVersionSource{Type: interfaces.KN_VERSION_SOURCE_TYPE, ProjectID: "p1"}
		v3 := &interfaces.KNVersion{Version: 3, Label: "v1.2.0", State: interfaces.KN_VERSION_STATE_RUNNING, Source: source}
		v2 := &interfaces.KNVersion{Version: 2, Label: "v1.1.0", State: interfaces.KN_VERSION_STATE_HISTORY, Source: source}
		v1 := &interfaces.KNVersion{Version: 1, Label: "v1.0.0", State: interfaces.KN_VERSION_STATE_HISTORY,
			Source: interfaces.KNVersionSource{Type: interfaces.KN_VERSION_SOURCE_TYPE, ProjectID: "p2"}}
		versions := []*interfaces.KNVersion{v3, v2, v1}
		kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").
			DoAndReturn(func(context.Context, string, string) ([]*interfaces.KNVersion, error) {
				return versions, nil
			}).AnyTimes()

		questions := &interfaces.ScenarioQuestions{SpecVersion: 2, Questions: []interfaces.ScenarioQuestion{
			{ID: "q1", Text: "Which lots expire this week?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
				Needs: []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "lot"}}},
			{ID: "q2", Text: "Who supplies a part?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
				Needs: []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "supplier"}}},
		}}
		with := map[string][]byte{"object_types/lot.bkn": objectTypeFile("lot", "Lot"), "object_types/supplier.bkn": objectTypeFile("supplier", "Supplier")}
		without := map[string][]byte{"object_types/supplier.bkn": objectTypeFile("supplier", "Supplier")}
		// expectQuestions reads the definitions of the running version and the target.
		expectQuestions := func(from string, to string) {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions, nil)
			kva.EXPECT().ExportVersion(gomock.Any(), reviewToken, "supply", from).Return(with, nil)
			kva.EXPECT().ExportVersion(gomock.Any(), reviewToken, "supply", to).Return(without, nil)
		}
		refusedWith := func(plan *interfaces.RollbackPlan, code string) {
			So(plan.Refusals, ShouldHaveLength, 1)
			So(plan.Refusals[0].Code, ShouldEqual, code)
		}

		Convey("The plan goes back to the previous version, naming both releases and placing the questions\n", func() {
			expectQuestions("3", "2")

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 0)

			So(err, ShouldBeNil)
			So(plan.ProjectID, ShouldEqual, "p1")
			So(plan.FullHistory, ShouldBeFalse)
			So(plan.From, ShouldResemble, interfaces.RollbackVersion{KNVersion: 3, Label: "v1.2.0", Version: "1.2.0"})
			So(*plan.To, ShouldResemble, interfaces.RollbackVersion{KNVersion: 2, Label: "v1.1.0", Version: "1.1.0"})
			So(plan.Refusals, ShouldBeEmpty)
			So(plan.Signature, ShouldEqual, rollbackSignature(3, 2))
			So(plan.Questions.Unanswerable, ShouldHaveLength, 1)
			So(plan.Questions.Unanswerable[0].ID, ShouldEqual, "q1")
			So(plan.Questions.Unanswerable[0].Missing, ShouldResemble,
				[]interfaces.ReleaseItem{{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "lot", Name: "Lot"}})
		})

		Convey("Naming the previous version is the same as naming none\n", func() {
			expectQuestions("3", "2")

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 2)

			So(err, ShouldBeNil)
			So(plan.Refusals, ShouldBeEmpty)
			So(plan.Signature, ShouldEqual, rollbackSignature(3, 2))
		})

		Convey("A network without a running version has nothing to go back to\n", func() {
			versioning.RunningVersion, versioning.PreviousVersion = 0, 0
			versions = []*interfaces.KNVersion{}

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 0)

			So(err, ShouldBeNil)
			So(plan.To, ShouldBeNil)
			refusedWith(plan, perrors.BknProject_Rollback_NoPrevious)

			_, err = svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: plan.Signature}, reviewOwner)
			status, code, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Rollback_NoPrevious)
		})

		Convey("The first version has no previous one\n", func() {
			versioning.PreviousVersion = 0

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 0)

			So(err, ShouldBeNil)
			So(plan.From.KNVersion, ShouldEqual, 3)
			So(plan.To, ShouldBeNil)
			refusedWith(plan, perrors.BknProject_Rollback_NoPrevious)
		})

		Convey("A running version another project published is not this project's to roll back\n", func() {
			r3.ProjectID = "p2"
			v3.Source.ProjectID = "p2"

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 0)

			So(err, ShouldBeNil)
			refusedWith(plan, perrors.BknProject_Rollback_NotProjectRelease)
			So(plan.Refusals[0].Details, ShouldResemble, map[string]any{"project_id": "p2"})

			_, err = svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: plan.Signature}, reviewOwner)
			status, code, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Rollback_NotProjectRelease)
		})

		Convey("A running version no release of the lineage published is not this project's either\n", func() {
			releases = []*interfaces.Release{r2, r1}

			plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 0)

			So(err, ShouldBeNil)
			So(plan.From, ShouldResemble, interfaces.RollbackVersion{KNVersion: 3, Label: "v1.2.0"})
			refusedWith(plan, perrors.BknProject_Rollback_NotProjectRelease)
		})

		Convey("The community edition goes back to the previous version only\n", func() {
			plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 1)

			So(err, ShouldBeNil)
			So(plan.To, ShouldBeNil)
			refusedWith(plan, perrors.BknProject_Rollback_EditionRestricted)
			So(plan.Refusals[0].Details, ShouldResemble, map[string]any{"previous_version": 2})

			_, err = svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: plan.Signature, ToVersion: 1}, reviewOwner)
			status, code, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusForbidden)
			So(code, ShouldEqual, perrors.BknProject_Rollback_EditionRestricted)
		})

		Convey("With the full history", func() {
			versioning.FullHistory = true

			Convey("any earlier version kept can be gone back to, named by the release that published it\n", func() {
				expectQuestions("3", "1")

				plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 1)

				So(err, ShouldBeNil)
				So(plan.FullHistory, ShouldBeTrue)
				So(plan.Refusals, ShouldBeEmpty)
				So(*plan.To, ShouldResemble, interfaces.RollbackVersion{KNVersion: 1, Label: "v1.0.0", Version: "1.0.0"})
				So(plan.Signature, ShouldEqual, rollbackSignature(3, 1))
			})

			Convey("a version the network does not have is not found\n", func() {
				plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 9)

				So(err, ShouldBeNil)
				refusedWith(plan, perrors.BknProject_Release_NotFound)
				So(plan.Refusals[0].Details, ShouldResemble, map[string]any{"kn_version": 9})
			})

			Convey("a purged version is refused\n", func() {
				v1.State = interfaces.KN_VERSION_STATE_PURGED

				plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 1)

				So(err, ShouldBeNil)
				So(plan.To, ShouldBeNil)
				refusedWith(plan, perrors.BknProject_Release_VersionPurged)

				_, err = svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: plan.Signature, ToVersion: 1}, reviewOwner)
				_, code, _ := errorOf(err)
				So(code, ShouldEqual, perrors.BknProject_Release_VersionPurged)
			})

			Convey("a deprecated version cannot be activated\n", func() {
				v1.State = interfaces.KN_VERSION_STATE_DEPRECATED

				plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 1)

				So(err, ShouldBeNil)
				refusedWith(plan, perrors.BknProject_Rollback_NotActivatable)
				So(plan.Refusals[0].Details, ShouldResemble,
					map[string]any{"kn_version": 1, "status": interfaces.KN_VERSION_STATE_DEPRECATED})
			})

			Convey("the running version itself cannot be gone back to\n", func() {
				plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 3)

				So(err, ShouldBeNil)
				refusedWith(plan, perrors.BknProject_Rollback_NotActivatable)
			})
		})

		Convey("The owner rolls back with the plan's signature: only the activation is made\n", func() {
			expectQuestions("3", "2")
			plan, err := svc.PlanRollback(ctx, reviewToken, "p1", 0)
			So(err, ShouldBeNil)
			expectQuestions("3", "2")
			kept := interfaces.KNVersionWarning{Category: "metric", ID: "m1", Description: "stale"}
			// Each release copies its functions and Skills, so their code not being restored says nothing.
			notPinned := interfaces.KNVersionWarning{Category: interfaces.KN_VERSION_WARNING_CAPABILITY_NOT_PINNED, ID: "f1"}
			kva.EXPECT().Activate(gomock.Any(), reviewToken, "supply", 2, 3, rollbackNotes).
				Return(&interfaces.KNVersionActivated{RunningVersion: 2, Warnings: []interfaces.KNVersionWarning{notPinned, kept}}, nil)

			got, err := svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: plan.Signature}, reviewOwner)

			So(err, ShouldBeNil)
			So(got, ShouldResemble, &interfaces.RollbackResult{RunningVersion: 2, Warnings: []interfaces.KNVersionWarning{kept}})
		})

		Convey("An activation with only warnings dropped has an empty list of them\n", func() {
			expectQuestions("3", "2")
			kva.EXPECT().Activate(gomock.Any(), reviewToken, "supply", 2, 3, rollbackNotes).
				Return(&interfaces.KNVersionActivated{RunningVersion: 2, Warnings: []interfaces.KNVersionWarning{
					{Category: interfaces.KN_VERSION_WARNING_CAPABILITY_NOT_PINNED, ID: "s1"}}}, nil)

			got, err := svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: rollbackSignature(3, 2)}, reviewOwner)

			So(err, ShouldBeNil)
			So(got.Warnings, ShouldNotBeNil)
			So(got.Warnings, ShouldBeEmpty)
		})

		Convey("A version published or activated between the plan and the rollback is caught by bkn-backend\n", func() {
			expectQuestions("3", "2")
			kva.EXPECT().Activate(gomock.Any(), reviewToken, "supply", 2, 3, rollbackNotes).
				Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusConflict,
					BaseError: httperrors.BaseError{ErrorCode: interfaces.KN_VERSION_ERROR_RUNNING_MISMATCH}})

			_, err := svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: rollbackSignature(3, 2)}, reviewOwner)

			status, code, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Rollback_PlanOutdated)
		})

		Convey("bkn-backend's other refusals of the activation are worded as bkn-project's\n", func() {
			expectQuestions("3", "2")
			kva.EXPECT().Activate(gomock.Any(), reviewToken, "supply", 2, 3, rollbackNotes).
				Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusConflict,
					BaseError: httperrors.BaseError{ErrorCode: interfaces.KN_VERSION_ERROR_BROKEN_REFERENCES}})

			_, err := svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: rollbackSignature(3, 2)}, reviewOwner)

			status, code, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Rollback_BrokenReferences)
		})

		Convey("Any other failure to activate is an internal error\n", func() {
			expectQuestions("3", "2")
			kva.EXPECT().Activate(gomock.Any(), reviewToken, "supply", 2, 3, rollbackNotes).Return(nil, errors.New("timeout"))

			_, err := svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: rollbackSignature(3, 2)}, reviewOwner)

			status, _, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusInternalServerError)
		})

		Convey("A plan that no longer stands is refused without activating, and so is no plan at all\n", func() {
			expectQuestions("3", "2")

			_, err := svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{Signature: rollbackSignature(4, 3)}, reviewOwner)

			status, code, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Rollback_PlanOutdated)

			_, err = svc.Rollback(ctx, reviewToken, "p1", interfaces.ProjectRollback{}, reviewOwner)
			status, code, _ = errorOf(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Rollback_PrecheckRequired)
		})

		Convey("The signature changes with the running version and with the target\n", func() {
			base := rollbackSignature(3, 2)
			So(rollbackSignature(3, 2), ShouldEqual, base)
			So(rollbackSignature(4, 2), ShouldNotEqual, base)
			So(rollbackSignature(3, 1), ShouldNotEqual, base)
			So(rollbackSignature(2, 3), ShouldNotEqual, base)
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
			ra2 := pmock.NewMockReleaseAccess(ctrl)
			paging := &projectReleaseService{ra: ra2}
			full := make([]*interfaces.Release, interfaces.MAX_RELEASE_LIMIT)
			for i := range full {
				full[i] = &interfaces.Release{ID: "r", CreateTime: int64(1000 - i)}
			}
			ra2.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "supply", Limit: interfaces.MAX_RELEASE_LIMIT}).Return(full, nil)
			ra2.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "supply", Limit: interfaces.MAX_RELEASE_LIMIT,
				Cursor: &interfaces.ReleaseCursor{CreateTime: full[len(full)-1].CreateTime, ID: "r"}}).Return([]*interfaces.Release{r1}, nil)

			got, err := paging.lineageReleases(ctx, "supply")

			So(err, ShouldBeNil)
			So(got, ShouldHaveLength, interfaces.MAX_RELEASE_LIMIT+1)
		})

		Convey("Failures to read the network's versions\n", func() {
			kva2 := pmock.NewMockKNVersionAccess(ctrl)
			reading := &projectReleaseService{ps: ps, ra: ra, kva: kva2}

			Convey("a network that is not versioned is bkn-project's refusal\n", func() {
				kva2.EXPECT().GetVersioning(gomock.Any(), reviewToken, "supply").Return(nil, &interfaces.BknBackendError{
					HTTPCode: http.StatusConflict, BaseError: httperrors.BaseError{ErrorCode: interfaces.KN_VERSION_ERROR_NOT_VERSIONED}})

				_, err := reading.PlanRollback(ctx, reviewToken, "p1", 0)

				status, code, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusConflict)
				So(code, ShouldEqual, perrors.BknProject_Release_NotVersioned)
			})

			Convey("anything else is an internal error\n", func() {
				kva2.EXPECT().GetVersioning(gomock.Any(), reviewToken, "supply").Return(versioning, nil)
				kva2.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return(nil, errors.New("timeout"))

				_, err := reading.PlanRollback(ctx, reviewToken, "p1", 0)

				status, code, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusInternalServerError)
				So(code, ShouldEqual, perrors.BknProject_Release_InternalError)
			})
		})
	})
}
