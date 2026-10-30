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

func errorOf(err error) (int, string, any) {
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		return 0, "", nil
	}
	return httpErr.HTTPCode, httpErr.BaseError.ErrorCode, httpErr.ErrorDetails
}

func Test_ProjectReleaseService_Publish(t *testing.T) {
	Convey("Test projectReleaseService publishing, syncing and switching\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ps := pmock.NewMockProjectService(mockCtrl)
		ls := pmock.NewMockLineageService(mockCtrl)
		ra := pmock.NewMockReleaseAccess(mockCtrl)
		reviewer := pmock.NewMockProjectReleaseService(mockCtrl)
		rgs := pmock.NewMockRegressionService(mockCtrl)
		rga := pmock.NewMockRegressionAccess(mockCtrl)
		svc := &projectReleaseService{ps: ps, ls: ls, ra: ra, reviewer: reviewer, rgs: rgs, rga: rga}
		// A release's regression starts after it; tests of publishing that do not look at it allow it.
		allowRegression := func() {
			rgs.EXPECT().StartRegression(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&interfaces.Regression{}, true, nil).AnyTimes()
		}
		ctx := context.Background()
		author := interfaces.GitAuthor{Name: reviewOwner.ID}
		other := interfaces.AccountInfo{ID: "u2", Type: "user"}

		project := &interfaces.ProjectEntry{Project: &interfaces.Project{
			ID: "p1", KNID: "supply", Branch: "project/p1", Owner: reviewOwner,
		}}
		review := &interfaces.ReleaseReview{
			ProjectID: "p1", Version: "1.1.0", Signature: "sig",
			Checks: []interfaces.ReleaseCheck{
				{Code: interfaces.RELEASE_CHECK_CONFIRMED, Passed: true},
				{Code: interfaces.RELEASE_CHECK_INDEX, Reason: interfaces.RELEASE_REASON_INDEX_NOT_READY,
					Accept: interfaces.RELEASE_ACCEPT_INDEX_NOT_READY},
			},
			Refusals: []interfaces.ReleaseRefusal{},
			Accepts:  []string{interfaces.RELEASE_ACCEPT_INDEX_NOT_READY},
		}
		request := interfaces.ProjectPublish{
			Version: "1.1.0", Notes: "Purchase orders.", Signature: "sig",
			Accepts: []string{interfaces.RELEASE_ACCEPT_INDEX_NOT_READY},
		}
		released := &interfaces.Release{LineageID: "supply", NetworkID: "supply_v1_1_0", Version: "1.1.0", ProjectID: "p1"}
		published := &interfaces.PublishReleaseResult{Release: released, NetworkID: "supply_v1_1_0", Tag: "v1.1.0"}

		Convey("Publishing\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil)

			Convey("A published release starts its regression as the publisher\n", func() {
				reviewer.EXPECT().ReviewRelease(gomock.Any(), reviewToken, "p1", "1.1.0", reviewOwner).Return(review, nil)
				ls.EXPECT().PublishRelease(gomock.Any(), reviewToken, "supply", gomock.Any(), reviewOwner, author).
					Return(published, nil)
				rgs.EXPECT().StartRegression(gomock.Any(), reviewToken, released, reviewOwner).
					Return(&interfaces.Regression{}, true, nil)

				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				So(err, ShouldBeNil)
			})

			Convey("A regression that cannot start leaves the release published\n", func() {
				reviewer.EXPECT().ReviewRelease(gomock.Any(), reviewToken, "p1", "1.1.0", reviewOwner).Return(review, nil)
				ls.EXPECT().PublishRelease(gomock.Any(), reviewToken, "supply", gomock.Any(), reviewOwner, author).
					Return(published, nil)
				rgs.EXPECT().StartRegression(gomock.Any(), reviewToken, released, reviewOwner).
					Return(nil, false, errors.New("db down"))

				result, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				So(err, ShouldBeNil)
				So(result, ShouldEqual, published)
			})

			allowRegression()

			Convey("A release whose review stands and whose failures are accepted is published, recording them\n", func() {
				reviewer.EXPECT().ReviewRelease(gomock.Any(), reviewToken, "p1", "1.1.0", reviewOwner).Return(review, nil)
				ls.EXPECT().PublishRelease(gomock.Any(), reviewToken, "supply", interfaces.PublishReleaseRequest{
					Branch: "project/p1", Version: "1.1.0", ProjectID: "p1",
					Notes: "Purchase orders.\n\nAccepted: index_not_ready",
				}, reviewOwner, author).Return(published, nil)

				result, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				So(err, ShouldBeNil)
				So(result, ShouldEqual, published)
			})

			Convey("Only what the review needed accepting is recorded, and notes without text get just that\n", func() {
				request.Notes = ""
				request.Accepts = []string{interfaces.RELEASE_ACCEPT_SIMILAR_NAMES, interfaces.RELEASE_ACCEPT_INDEX_NOT_READY}
				reviewer.EXPECT().ReviewRelease(gomock.Any(), reviewToken, "p1", "1.1.0", reviewOwner).Return(review, nil)
				ls.EXPECT().PublishRelease(gomock.Any(), reviewToken, "supply", interfaces.PublishReleaseRequest{
					Branch: "project/p1", Version: "1.1.0", ProjectID: "p1", Notes: "Accepted: index_not_ready",
				}, reviewOwner, author).Return(published, nil)

				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				So(err, ShouldBeNil)
			})

			Convey("A release that needs nothing accepted keeps its notes as written\n", func() {
				review.Checks, review.Accepts = review.Checks[:1], []string{}
				reviewer.EXPECT().ReviewRelease(gomock.Any(), reviewToken, "p1", "1.1.0", reviewOwner).Return(review, nil)
				ls.EXPECT().PublishRelease(gomock.Any(), reviewToken, "supply", interfaces.PublishReleaseRequest{
					Branch: "project/p1", Version: "1.1.0", ProjectID: "p1", Notes: "Purchase orders.",
				}, reviewOwner, author).Return(published, nil)

				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				So(err, ShouldBeNil)
			})

			Convey("Someone other than the owner is refused before the review is read\n", func() {
				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, other, author)

				status, code, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusForbidden)
				So(code, ShouldEqual, perrors.BknProject_Release_NotOwner)
			})

			Convey("An archived project is refused\n", func() {
				project.Archived = true

				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				status, code, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusConflict)
				So(code, ShouldEqual, perrors.BknProject_Release_ProjectArchived)
			})

			Convey("Accepting anything but the three acceptable failures is refused\n", func() {
				request.Accepts = []string{interfaces.RELEASE_ACCEPT_INDEX_NOT_READY, interfaces.RELEASE_REASON_CHECK_ISSUES}

				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				status, code, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusBadRequest)
				So(code, ShouldEqual, perrors.BknProject_Release_InvalidAccepts)
			})

			Convey("A review the project moved past is refused, with the signature it has now\n", func() {
				review.Signature = "moved"
				reviewer.EXPECT().ReviewRelease(gomock.Any(), reviewToken, "p1", "1.1.0", reviewOwner).Return(review, nil)

				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				status, code, details := errorOf(err)
				So(status, ShouldEqual, http.StatusConflict)
				So(code, ShouldEqual, perrors.BknProject_Release_ReviewOutdated)
				So(details, ShouldResemble, map[string]any{"signature": "moved"})
			})

			Convey("A failure not accepted is refused, listing the checks that did not pass\n", func() {
				request.Accepts = nil
				reviewer.EXPECT().ReviewRelease(gomock.Any(), reviewToken, "p1", "1.1.0", reviewOwner).Return(review, nil)

				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				status, code, details := errorOf(err)
				So(status, ShouldEqual, http.StatusConflict)
				So(code, ShouldEqual, perrors.BknProject_Release_ChecksFailed)
				So(details, ShouldResemble, map[string]any{
					"checks":   []interfaces.ReleaseCheck{review.Checks[1]},
					"refusals": []interfaces.ReleaseRefusal{},
					"accepts":  []string{interfaces.RELEASE_ACCEPT_INDEX_NOT_READY},
				})
			})

			Convey("Tables gone cannot be accepted: the release is refused with that check\n", func() {
				gone := interfaces.ReleaseCheck{Code: interfaces.RELEASE_CHECK_DATA_SOURCE,
					Reason:  interfaces.RELEASE_REASON_TABLE_MISSING,
					Details: []interfaces.ReleaseItem{{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "part", Name: "Part"}}}
				review.Checks = append(review.Checks, gone)
				review.Blocked = true
				request.Accepts = acceptable
				reviewer.EXPECT().ReviewRelease(gomock.Any(), reviewToken, "p1", "1.1.0", reviewOwner).Return(review, nil)

				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				_, code, details := errorOf(err)
				So(code, ShouldEqual, perrors.BknProject_Release_ChecksFailed)
				So(details.(map[string]any)["checks"], ShouldContain, gone)

				request.Accepts = []string{interfaces.RELEASE_REASON_TABLE_MISSING}
				ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil)
				_, err = svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)
				status, code, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusBadRequest)
				So(code, ShouldEqual, perrors.BknProject_Release_InvalidAccepts)
			})

			Convey("A blocked release is refused whatever is accepted\n", func() {
				review.Blocked = true
				review.Refusals = []interfaces.ReleaseRefusal{{Code: perrors.BknProject_Release_VersionExists}}
				reviewer.EXPECT().ReviewRelease(gomock.Any(), reviewToken, "p1", "1.1.0", reviewOwner).Return(review, nil)

				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				_, code, _ := errorOf(err)
				So(code, ShouldEqual, perrors.BknProject_Release_ChecksFailed)
			})

			Convey("The lineage's refusal of the release itself is passed on\n", func() {
				behind := httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_BranchBehind)
				reviewer.EXPECT().ReviewRelease(gomock.Any(), reviewToken, "p1", "1.1.0", reviewOwner).Return(review, nil)
				ls.EXPECT().PublishRelease(gomock.Any(), reviewToken, "supply", gomock.Any(), reviewOwner, author).Return(nil, behind)

				_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)

				So(err, ShouldEqual, behind)
			})
		})

		Convey("A project the caller cannot read is the error it is\n", func() {
			notFound := httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Project_NotFound)
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(nil, notFound).Times(5)

			_, err := svc.PublishRelease(ctx, reviewToken, "p1", request, reviewOwner, author)
			So(err, ShouldEqual, notFound)
			_, err = svc.SyncBranch(ctx, reviewToken, "p1", reviewOwner, author)
			So(err, ShouldEqual, notFound)
			_, err = svc.CommitDrift(ctx, reviewToken, "p1", reviewOwner, author)
			So(err, ShouldEqual, notFound)
			_, err = svc.PlanSwitch(ctx, reviewToken, "p1", "supply_v1_1_0")
			So(err, ShouldEqual, notFound)
			_, err = svc.SwitchConsumers(ctx, reviewToken, "p1", "supply_v1_1_0", reviewOwner)
			So(err, ShouldEqual, notFound)
		})

		Convey("Syncing brings the live version into the project's branch, for the owner\n", func() {
			synced := &interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_MERGED, Commit: "b2"}
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil).Times(3)
			ls.EXPECT().SyncBranch(gomock.Any(), "supply", "project/p1", author).Return(synced, nil)

			result, err := svc.SyncBranch(ctx, reviewToken, "p1", reviewOwner, author)
			So(err, ShouldBeNil)
			So(result, ShouldEqual, synced)

			_, err = svc.SyncBranch(ctx, reviewToken, "p1", other, author)
			_, code, _ := errorOf(err)
			So(code, ShouldEqual, perrors.BknProject_Release_NotOwner)

			project.Archived = true
			_, err = svc.SyncBranch(ctx, reviewToken, "p1", reviewOwner, author)
			_, code, _ = errorOf(err)
			So(code, ShouldEqual, perrors.BknProject_Release_ProjectArchived)
		})

		Convey("Committing drift commits the live network's to main, then syncs the branch\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil)
			drift := &interfaces.DriftResult{DriftReport: &interfaces.DriftReport{KNID: "supply_v1_0_0"}, Commit: "m2"}
			synced := &interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_MERGED, Commit: "b2"}

			Convey("The live network is the newest release\n", func() {
				ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "supply", Limit: 1}).Return(
					[]*interfaces.Release{{NetworkID: "supply_v1_0_0"}}, nil)
				gomock.InOrder(
					ls.EXPECT().CheckDrift(gomock.Any(), reviewToken, "supply", "supply_v1_0_0", true, author).Return(drift, nil),
					ls.EXPECT().SyncBranch(gomock.Any(), "supply", "project/p1", author).Return(synced, nil),
				)

				result, err := svc.CommitDrift(ctx, reviewToken, "p1", reviewOwner, author)

				So(err, ShouldBeNil)
				So(result, ShouldResemble, &interfaces.ProjectDriftCommit{Drift: drift, Sync: synced})
			})

			Convey("Before any release it is the network the lineage started from\n", func() {
				ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
				ls.EXPECT().CheckDrift(gomock.Any(), reviewToken, "supply", "supply", true, author).Return(drift, nil)
				ls.EXPECT().SyncBranch(gomock.Any(), "supply", "project/p1", author).Return(synced, nil)

				_, err := svc.CommitDrift(ctx, reviewToken, "p1", reviewOwner, author)

				So(err, ShouldBeNil)
			})

			Convey("A refused commit is passed on and the branch is not synced\n", func() {
				refused := httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_NetworkNeedsCheck)
				ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
				ls.EXPECT().CheckDrift(gomock.Any(), reviewToken, "supply", "supply", true, author).Return(nil, refused)

				_, err := svc.CommitDrift(ctx, reviewToken, "p1", reviewOwner, author)

				So(err, ShouldEqual, refused)
			})

			Convey("A conflict syncing after the commit is passed on\n", func() {
				conflict := httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_MergeConflict)
				ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
				ls.EXPECT().CheckDrift(gomock.Any(), reviewToken, "supply", "supply", true, author).Return(drift, nil)
				ls.EXPECT().SyncBranch(gomock.Any(), "supply", "project/p1", author).Return(nil, conflict)

				_, err := svc.CommitDrift(ctx, reviewToken, "p1", reviewOwner, author)

				So(err, ShouldEqual, conflict)
			})

			Convey("Someone other than the owner is refused\n", func() {
				_, err := svc.CommitDrift(ctx, reviewToken, "p1", other, author)

				_, code, _ := errorOf(err)
				So(code, ShouldEqual, perrors.BknProject_Release_NotOwner)
			})
		})

		Convey("A release's regression is read by who can read the project, and run again by its owner\n", func() {
			release := &interfaces.Release{LineageID: "supply", NetworkID: "supply_v1_1_0", Version: "1.1.0", ProjectID: "p1"}
			regression := &interfaces.Regression{NetworkID: "supply_v1_1_0", Status: interfaces.REGRESSION_STATUS_PASSED}
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil).AnyTimes()

			Convey("Reading\n", func() {
				ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "supply_v1_1_0").Return(release, true, nil)

				Convey("A regression is returned as stored\n", func() {
					rga.EXPECT().GetRegression(gomock.Any(), "supply_v1_1_0").Return(regression, true, nil)

					got, err := svc.GetRegression(ctx, reviewToken, "p1", "supply_v1_1_0")

					So(err, ShouldBeNil)
					So(got, ShouldEqual, regression)
				})

				Convey("A release without one is 404\n", func() {
					rga.EXPECT().GetRegression(gomock.Any(), "supply_v1_1_0").Return(nil, false, nil)

					_, err := svc.GetRegression(ctx, reviewToken, "p1", "supply_v1_1_0")

					status, code, _ := errorOf(err)
					So(status, ShouldEqual, http.StatusNotFound)
					So(code, ShouldEqual, perrors.BknProject_Release_RegressionNotFound)
				})

				Convey("A failure to read it is an internal error\n", func() {
					rga.EXPECT().GetRegression(gomock.Any(), "supply_v1_1_0").Return(nil, false, errors.New("db down"))

					_, err := svc.GetRegression(ctx, reviewToken, "p1", "supply_v1_1_0")

					status, _, _ := errorOf(err)
					So(status, ShouldEqual, http.StatusInternalServerError)
				})
			})

			Convey("A network no release of the project created is refused, read or run\n", func() {
				ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "other_v1_0_0").Return(nil, false, nil).Times(2)

				_, err := svc.GetRegression(ctx, reviewToken, "p1", "other_v1_0_0")
				_, code, _ := errorOf(err)
				So(code, ShouldEqual, perrors.BknProject_Release_NotProjectRelease)

				_, err = svc.RunRegression(ctx, reviewToken, "p1", "other_v1_0_0", reviewOwner)
				_, code, _ = errorOf(err)
				So(code, ShouldEqual, perrors.BknProject_Release_NotProjectRelease)
			})

			Convey("Running again starts it as the owner, with the release\n", func() {
				ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "supply_v1_1_0").Return(release, true, nil)
				running := &interfaces.Regression{NetworkID: "supply_v1_1_0", Status: interfaces.REGRESSION_STATUS_RUNNING}
				rgs.EXPECT().StartRegression(gomock.Any(), reviewToken, release, reviewOwner).Return(running, true, nil)

				got, err := svc.RunRegression(ctx, reviewToken, "p1", "supply_v1_1_0", reviewOwner)

				So(err, ShouldBeNil)
				So(got, ShouldEqual, running)
			})

			Convey("Running again while it runs is refused\n", func() {
				ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "supply_v1_1_0").Return(release, true, nil)
				rgs.EXPECT().StartRegression(gomock.Any(), reviewToken, release, reviewOwner).
					Return(&interfaces.Regression{}, false, nil)

				_, err := svc.RunRegression(ctx, reviewToken, "p1", "supply_v1_1_0", reviewOwner)

				status, code, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusConflict)
				So(code, ShouldEqual, perrors.BknProject_Release_RegressionRunning)
			})

			Convey("A failure to start is an internal error\n", func() {
				ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "supply_v1_1_0").Return(release, true, nil)
				rgs.EXPECT().StartRegression(gomock.Any(), reviewToken, release, reviewOwner).
					Return(nil, false, errors.New("db down"))

				_, err := svc.RunRegression(ctx, reviewToken, "p1", "supply_v1_1_0", reviewOwner)

				status, _, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusInternalServerError)
			})

			Convey("Only the owner of an unarchived project runs it again\n", func() {
				_, err := svc.RunRegression(ctx, reviewToken, "p1", "supply_v1_1_0", other)
				_, code, _ := errorOf(err)
				So(code, ShouldEqual, perrors.BknProject_Release_NotOwner)

				project.Archived = true
				_, err = svc.RunRegression(ctx, reviewToken, "p1", "supply_v1_1_0", reviewOwner)
				_, code, _ = errorOf(err)
				So(code, ShouldEqual, perrors.BknProject_Release_ProjectArchived)
				project.Archived = false
			})
		})

		Convey("Switching consumers is only to a release of the project\n", func() {
			release := &interfaces.Release{LineageID: "supply", NetworkID: "supply_v1_1_0", ProjectID: "p1"}
			plan := &interfaces.SwitchPlan{LineageID: "supply", ToNetworkID: "supply_v1_1_0"}
			switched := &interfaces.Switch{ToNetworkID: "supply_v1_1_0"}
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil).AnyTimes()

			Convey("Its plan can be read by anyone who can read the project; the owner switches\n", func() {
				ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "supply_v1_1_0").Return(release, true, nil).Times(2)
				ls.EXPECT().PlanSwitch(gomock.Any(), reviewToken, "supply", "supply_v1_1_0").Return(plan, nil)
				ls.EXPECT().SwitchConsumers(gomock.Any(), reviewToken, "supply",
					interfaces.SwitchRequest{ToNetworkID: "supply_v1_1_0"}, reviewOwner).Return(switched, nil)

				gotPlan, err := svc.PlanSwitch(ctx, reviewToken, "p1", "supply_v1_1_0")
				So(err, ShouldBeNil)
				So(gotPlan, ShouldEqual, plan)
				gotSwitch, err := svc.SwitchConsumers(ctx, reviewToken, "p1", "supply_v1_1_0", reviewOwner)
				So(err, ShouldBeNil)
				So(gotSwitch, ShouldEqual, switched)
			})

			Convey("A network no release of this project created is refused\n", func() {
				for _, found := range []*interfaces.Release{
					nil,
					{LineageID: "supply", NetworkID: "supply_v1_1_0", ProjectID: "p2"},
					{LineageID: "other", NetworkID: "supply_v1_1_0", ProjectID: "p1"},
				} {
					ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "supply_v1_1_0").Return(found, found != nil, nil).Times(2)

					_, err := svc.PlanSwitch(ctx, reviewToken, "p1", "supply_v1_1_0")
					status, code, _ := errorOf(err)
					So(status, ShouldEqual, http.StatusBadRequest)
					So(code, ShouldEqual, perrors.BknProject_Release_NotProjectRelease)
					_, err = svc.SwitchConsumers(ctx, reviewToken, "p1", "supply_v1_1_0", reviewOwner)
					_, code, _ = errorOf(err)
					So(code, ShouldEqual, perrors.BknProject_Release_NotProjectRelease)
				}
			})

			Convey("Only the owner of an unarchived project switches\n", func() {
				_, err := svc.SwitchConsumers(ctx, reviewToken, "p1", "supply_v1_1_0", other)
				_, code, _ := errorOf(err)
				So(code, ShouldEqual, perrors.BknProject_Release_NotOwner)

				project.Archived = true
				_, err = svc.SwitchConsumers(ctx, reviewToken, "p1", "supply_v1_1_0", reviewOwner)
				_, code, _ = errorOf(err)
				So(code, ShouldEqual, perrors.BknProject_Release_ProjectArchived)
			})

			Convey("A failure to read the release is an internal error\n", func() {
				ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "supply_v1_1_0").Return(nil, false, errors.New("db down"))

				_, err := svc.PlanSwitch(ctx, reviewToken, "p1", "supply_v1_1_0")

				status, code, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusInternalServerError)
				So(code, ShouldEqual, perrors.BknProject_Release_InternalError)
			})
		})
	})
}
