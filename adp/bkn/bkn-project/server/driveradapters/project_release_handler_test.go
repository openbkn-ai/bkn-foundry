// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_ProjectReleaseRestHandler(t *testing.T) {
	Convey("Test project release handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		prs := pmock.NewMockProjectReleaseService(mockCtrl)
		drs := pmock.NewMockDraftService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, prs: prs, drs: drs}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
			Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User, TokenID: "Bearer t"}, nil)
		caller := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
		author := interfaces.GitAuthor{Name: "u1"}
		const base = "/api/bkn-project/v1/projects/p1"

		Convey("release-review reviews the version the query names, as the caller\n", func() {
			prs.EXPECT().ReviewRelease(gomock.Any(), "Bearer t", "p1", "1.1.0", caller).
				Return(&interfaces.ReleaseReview{ProjectID: "p1", Version: "1.1.0", Signature: "sig"}, nil)

			w := serve(handler, http.MethodGet, base+"/release-review?version=1.1.0")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"signature":"sig"`)
		})

		Convey("prepare prepares the version the path names with the review the body names\n", func() {
			prs.EXPECT().PrepareRelease(gomock.Any(), "Bearer t", "p1", interfaces.ProjectPrepare{
				Version: "1.1.0", Signature: "sig", Accepts: []string{"index_not_ready"}, OverwriteDraftChanges: true,
			}, caller).Return(&interfaces.Release{ID: "r1", Version: "1.1.0", State: interfaces.RELEASE_STATE_PREPARED}, nil)

			w := serveBody(handler, http.MethodPost, base+"/releases/1.1.0/prepare",
				[]byte(`{"version":"9.9.9","signature":"sig","accepts":["index_not_ready"],"overwrite_draft_changes":true}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"state":"prepared"`)
		})

		Convey("prepare refuses a body that is not JSON, and replies a refusal of the service\n", func() {
			w := serveBody(handler, http.MethodPost, base+"/releases/1.1.0/prepare", []byte(`{"signature":`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_InvalidRequestBody)

			prs.EXPECT().PrepareRelease(gomock.Any(), "Bearer t", "p1", gomock.Any(), caller).Return(nil,
				httperrors.NewHTTPError(t.Context(), http.StatusConflict, perrors.BknProject_Release_ChecksFailed))
			w = serveBody(handler, http.MethodPost, base+"/releases/1.1.0/prepare", []byte(`{"signature":"sig"}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_ChecksFailed)
		})

		Convey("releases publishes the prepared version the body names and replies 201\n", func() {
			prs.EXPECT().PublishRelease(gomock.Any(), "Bearer t", "p1", interfaces.ProjectPublish{
				Version: "1.1.0", Notes: "n", AllowOutdatedBase: true,
			}, caller, author).Return(&interfaces.PublishReleaseResult{NetworkID: "supply", Tag: "v1.1.0", KNVersion: 4}, nil)

			w := serveBody(handler, http.MethodPost, base+"/releases",
				[]byte(`{"version":"1.1.0","notes":"n","allow_outdated_base":true}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
			So(w.Body.String(), ShouldContainSubstring, `"network_id":"supply"`)
			So(w.Body.String(), ShouldContainSubstring, `"kn_version":4`)
		})

		Convey("A release body that is not JSON is refused\n", func() {
			w := serveBody(handler, http.MethodPost, base+"/releases", []byte(`{"version":`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_InvalidRequestBody)
		})

		Convey("A refusal of the service is the reply\n", func() {
			prs.EXPECT().PublishRelease(gomock.Any(), "Bearer t", "p1", gomock.Any(), caller, author).Return(nil,
				httperrors.NewHTTPError(t.Context(), http.StatusConflict, perrors.BknProject_Release_ChecksFailed))

			w := serveBody(handler, http.MethodPost, base+"/releases", []byte(`{"version":"1.1.0"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_ChecksFailed)
		})

		Convey("branch:sync syncs the project's branch as the caller\n", func() {
			prs.EXPECT().SyncBranch(gomock.Any(), "Bearer t", "p1", caller, author).
				Return(&interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_MERGED, Commit: "b2"}, nil)

			w := serve(handler, http.MethodPost, base+"/branch:sync")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"result":"merged"`)
		})

		Convey("drift:commit commits the live network's drift and syncs the branch\n", func() {
			prs.EXPECT().CommitDrift(gomock.Any(), "Bearer t", "p1", caller, author).Return(&interfaces.ProjectDriftCommit{
				Sync: &interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_FAST_FORWARD},
			}, nil)

			w := serve(handler, http.MethodPost, base+"/drift:commit")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"result":"fast_forward"`)
		})

		Convey("catalogs:understand still reaches its handler beside the release actions\n", func() {
			drs.EXPECT().Understand(gomock.Any(), "Bearer t", "p1", gomock.Any(), caller).
				Return(&interfaces.ProjectUnderstandSubmitted{}, nil)

			So(serve(handler, http.MethodPost, base+"/catalogs:understand").Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(serve(handler, http.MethodPost, base+"/branch:merge").Result().StatusCode, ShouldEqual, http.StatusNotFound)
		})

		Convey("Switch routes are gone\n", func() {
			So(serve(handler, http.MethodGet, base+"/switch-plan?to_network_id=supply").Result().StatusCode,
				ShouldEqual, http.StatusNotFound)
			So(serveBody(handler, http.MethodPost, base+"/switches", []byte(`{}`)).Result().StatusCode,
				ShouldEqual, http.StatusNotFound)
		})

		Convey("The rollback plan is read for the version the query names, the previous one by default\n", func() {
			prs.EXPECT().PlanRollback(gomock.Any(), "Bearer t", "p1", 0).
				Return(&interfaces.RollbackPlan{ProjectID: "p1", Signature: "sig"}, nil)
			w := serve(handler, http.MethodGet, base+"/rollback-plan")
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"signature":"sig"`)

			prs.EXPECT().PlanRollback(gomock.Any(), "Bearer t", "p1", 2).
				Return(&interfaces.RollbackPlan{ProjectID: "p1", Signature: "sig2"}, nil)
			w = serve(handler, http.MethodGet, base+"/rollback-plan?to_version=2")
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"signature":"sig2"`)

			for _, bad := range []string{"0", "-1", "two"} {
				w = serve(handler, http.MethodGet, base+"/rollback-plan?to_version="+bad)
				So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
				So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_InvalidRequestBody)
			}
		})

		Convey("rollbacks rolls back with the plan's signature and version and replies 200\n", func() {
			prs.EXPECT().Rollback(gomock.Any(), "Bearer t", "p1", interfaces.ProjectRollback{Signature: "sig", ToVersion: 2}, caller).
				Return(&interfaces.RollbackResult{RunningVersion: 2, Warnings: []interfaces.KNVersionWarning{}}, nil)
			w := serveBody(handler, http.MethodPost, base+"/rollbacks", []byte(`{"signature":"sig","to_version":2}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldEqual, `{"running_version":2,"warnings":[]}`)
			So(serveBody(handler, http.MethodPost, base+"/rollbacks", []byte(`[`)).Result().StatusCode,
				ShouldEqual, http.StatusBadRequest)
		})

		Convey("versions lists the versions of the project's network\n", func() {
			prs.EXPECT().ListVersions(gomock.Any(), "Bearer t", "p1").Return(&interfaces.ProjectVersions{
				Versioning: interfaces.KNVersioning{Enabled: true, RunningVersion: 2},
				Entries: []interfaces.ProjectVersion{{KNVersion: interfaces.KNVersion{Version: 2, State: "running"},
					Origin: interfaces.VERSION_ORIGIN_PROJECT, Release: "1.1.0"}},
			}, nil)

			w := serve(handler, http.MethodGet, base+"/versions")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			var body map[string]any
			So(json.Unmarshal(w.Body.Bytes(), &body), ShouldBeNil)
			So(body["versioning"].(map[string]any)["running_version"], ShouldEqual, 2)
			entry := body["entries"].([]any)[0].(map[string]any)
			So(entry["version"], ShouldEqual, 2)
			So(entry["origin"], ShouldEqual, "project")
			So(entry["release"], ShouldEqual, "1.1.0")
			So(body, ShouldNotContainKey, "prepared")
		})

		Convey("versions replies a refusal of the service\n", func() {
			prs.EXPECT().ListVersions(gomock.Any(), "Bearer t", "p1").Return(nil,
				httperrors.NewHTTPError(t.Context(), http.StatusNotFound, perrors.BknProject_Project_NotFound))

			w := serve(handler, http.MethodGet, base+"/versions")

			So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Project_NotFound)
		})

		Convey("copy copies the version the path names into the project as the caller\n", func() {
			prs.EXPECT().CopyVersion(gomock.Any(), "Bearer t", "p1", 3, interfaces.ProjectCopyVersion{OverwriteDraftChanges: true},
				caller, author).Return(&interfaces.CommitDefinitionsResult{Commit: "c9"}, nil)

			w := serveBody(handler, http.MethodPost, base+"/versions/3/copy", []byte(`{"overwrite_draft_changes":true}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"commit":"c9"`)
		})

		Convey("copy refuses a version that is not a positive number and a body that is not JSON\n", func() {
			for _, c := range []struct{ version, body string }{
				{"0", `{}`}, {"-2", `{}`}, {"v3", `{}`}, {"3", `{"overwrite_draft_changes":`},
			} {
				w := serveBody(handler, http.MethodPost, base+"/versions/"+c.version+"/copy", []byte(c.body))
				So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
				So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_InvalidRequestBody)
			}
		})

		Convey("copy replies a refusal of the service\n", func() {
			prs.EXPECT().CopyVersion(gomock.Any(), "Bearer t", "p1", 1, gomock.Any(), caller, author).Return(nil,
				httperrors.NewHTTPError(t.Context(), http.StatusForbidden, perrors.BknProject_Project_NotOwner))

			w := serveBody(handler, http.MethodPost, base+"/versions/1/copy", []byte(`{}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusForbidden)
		})

		Convey("A release's regression is read for the version the path names\n", func() {
			prs.EXPECT().GetRegression(gomock.Any(), "Bearer t", "p1", "1.1.0").Return(&interfaces.Regression{
				ReleaseID: "r1", Version: "1.1.0", Status: interfaces.REGRESSION_STATUS_NOT_PASSED,
			}, nil)

			w := serve(handler, http.MethodGet, base+"/releases/1.1.0/regression")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"status":"not_passed"`)
		})

		Convey("regression:run runs it again as the caller and replies 202\n", func() {
			prs.EXPECT().RunRegression(gomock.Any(), "Bearer t", "p1", "1.1.0", caller).Return(&interfaces.Regression{
				ReleaseID: "r1", Version: "1.1.0", Status: interfaces.REGRESSION_STATUS_RUNNING,
			}, nil)

			w := serve(handler, http.MethodPost, base+"/releases/1.1.0/regression:run")

			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(w.Body.String(), ShouldContainSubstring, `"status":"running"`)
		})

		Convey("Another action on a release is not found, and a refusal is the reply\n", func() {
			So(serve(handler, http.MethodPost, base+"/releases/1.1.0/regression:stop").Result().StatusCode,
				ShouldEqual, http.StatusNotFound)

			prs.EXPECT().RunRegression(gomock.Any(), "Bearer t", "p1", "1.1.0", caller).Return(nil,
				httperrors.NewHTTPError(t.Context(), http.StatusConflict, perrors.BknProject_Release_RegressionRunning))

			w := serve(handler, http.MethodPost, base+"/releases/1.1.0/regression:run")

			So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_RegressionRunning)
		})

		Convey("Publishing still reaches its handler beside the release routes\n", func() {
			prs.EXPECT().PublishRelease(gomock.Any(), "Bearer t", "p1", gomock.Any(), caller, author).
				Return(&interfaces.PublishReleaseResult{NetworkID: "supply"}, nil)

			w := serveBody(handler, http.MethodPost, base+"/releases", []byte(`{"version":"1.1.0"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
		})
	})
}
