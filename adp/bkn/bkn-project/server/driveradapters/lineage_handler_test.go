// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func serveBody(handler *restHandler, method, path string, body []byte) *httptest.ResponseRecorder {
	engine := gin.New()
	engine.Use(gin.Recovery())
	handler.RegisterPublic(engine)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(body)))
	return w
}

func bknTar(files map[string]string) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(content))})
		_, _ = tw.Write([]byte(content))
	}
	_ = tw.Close()
	return buf.Bytes()
}

func httpError(status int, code string) error {
	return httperrors.NewHTTPError(context.Background(), status, code)
}

func Test_LineageRestHandler(t *testing.T) {
	Convey("Test lineage handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		ls := pmock.NewMockLineageService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, ls: ls}
		visitor := hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User, TokenID: "Bearer t1"}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(visitor, nil)
		author := interfaces.GitAuthor{Name: "u1"}

		Convey("Create lineage exports with the caller's token and replies 201\n", func() {
			ls.EXPECT().CreateLineage(gomock.Any(), "Bearer t1", "orders", author).
				Return(&interfaces.CreateLineageResult{LineageID: "orders", Commit: "c1"}, nil)

			w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages", []byte(`{"kn_id":"orders"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
			So(w.Body.String(), ShouldEqual, `{"lineage_id":"orders","commit":"c1"}`)
		})

		Convey("Create lineage replies the service error and refuses a bad body\n", func() {
			ls.EXPECT().CreateLineage(gomock.Any(), "Bearer t1", "orders", author).
				Return(nil, httpError(http.StatusConflict, perrors.BknProject_Lineage_AlreadyExists))

			w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages", []byte(`{"kn_id":"orders"}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Lineage_AlreadyExists)

			w = serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages", []byte(`not json`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Lineage_InvalidRequestBody)
		})

		Convey("Create branch passes name and from and replies 201\n", func() {
			ls.EXPECT().CreateBranch(gomock.Any(), "orders", "project/p1", "").
				Return(&interfaces.CreateBranchResult{Name: "project/p1", Commit: "c1"}, nil)

			w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/branches", []byte(`{"name":"project/p1"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
			So(w.Body.String(), ShouldEqual, `{"name":"project/p1","commit":"c1"}`)
		})

		Convey("Create branch replies 409 for an existing branch\n", func() {
			ls.EXPECT().CreateBranch(gomock.Any(), "orders", "project/p1", "main").
				Return(nil, httpError(http.StatusConflict, perrors.BknProject_Lineage_BranchAlreadyExists))

			w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/branches", []byte(`{"name":"project/p1","from":"main"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Lineage_BranchAlreadyExists)
		})

		definitionsURL := "/api/bkn-project/v1/lineages/orders/branches/project/p1/definitions"

		Convey("Commit definitions reads the tar body and the message\n", func() {
			files := map[string][]byte{"network.bkn": []byte("network"), "object_types/order.bkn": []byte("order")}
			ls.EXPECT().CommitDefinitions(gomock.Any(), "orders", "project/p1", files, "edit orders", author).
				Return(&interfaces.CommitDefinitionsResult{Commit: "c2", Changed: interfaces.PathDiff{
					Added: []string{}, Changed: []string{"object_types/order.bkn"}, Removed: []string{}}}, nil)

			body := bknTar(map[string]string{"./network.bkn": "network", "object_types/order.bkn": "order", "CHECKSUM": "x"})
			w := serveBody(handler, http.MethodPut, definitionsURL+"?message=edit+orders", body)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldEqual, `{"commit":"c2","changed":{"added":[],"changed":["object_types/order.bkn"],"removed":[]}}`)
		})

		Convey("Commit definitions refuses a body that is not a BKN tar\n", func() {
			body := bknTar(map[string]string{"../escape.bkn": "x"})
			w := serveBody(handler, http.MethodPut, definitionsURL, body)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Lineage_InvalidDefinitions)
		})

		Convey("Commit definitions replies 403 for main\n", func() {
			ls.EXPECT().CommitDefinitions(gomock.Any(), "orders", "main", gomock.Any(), "", author).
				Return(nil, httpError(http.StatusForbidden, perrors.BknProject_Lineage_MainBranchReadOnly))

			w := serveBody(handler, http.MethodPut, "/api/bkn-project/v1/lineages/orders/branches/main/definitions",
				bknTar(map[string]string{"network.bkn": "network"}))

			So(w.Result().StatusCode, ShouldEqual, http.StatusForbidden)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Lineage_MainBranchReadOnly)
		})

		Convey("Sync replies the result\n", func() {
			ls.EXPECT().SyncBranch(gomock.Any(), "orders", "project/p1", author).
				Return(&interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_FAST_FORWARD, Commit: "c3"}, nil)

			w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/branches/project/p1/sync", nil)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldEqual, `{"result":"fast_forward","commit":"c3"}`)
		})

		Convey("Sync replies 409 with the conflicts\n", func() {
			conflict := httperrors.NewHTTPError(context.Background(), http.StatusConflict, perrors.BknProject_Lineage_MergeConflict).
				WithErrorDetails(map[string]any{"conflicts": []interfaces.MergeConflict{{Type: "object_type", ID: "part", Reason: "both_changed"}}})
			ls.EXPECT().SyncBranch(gomock.Any(), "orders", "project/p1", author).Return(nil, conflict)

			w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/branches/project/p1/sync", nil)

			So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Lineage_MergeConflict)
			So(w.Body.String(), ShouldContainSubstring, `"id":"part"`)
		})

		Convey("An unknown branch action is not found\n", func() {
			for _, method := range []string{http.MethodPost, http.MethodPut} {
				w := serveBody(handler, method, "/api/bkn-project/v1/lineages/orders/branches/project/p1/other", nil)
				So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
			}
		})

		Convey("Drift reports without committing by default\n", func() {
			report := &interfaces.DriftReport{LineageID: "orders", KNID: "orders",
				PathDiff: interfaces.PathDiff{Added: []string{}, Changed: []string{"network.bkn"}, Removed: []string{}}}
			ls.EXPECT().CheckDrift(gomock.Any(), "Bearer t1", "orders", "orders", false, author).
				Return(&interfaces.DriftResult{DriftReport: report}, nil)

			w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/drift", []byte(`{"kn_id":"orders"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldEqual, `{"lineage_id":"orders","kn_id":"orders","added":[],"changed":["network.bkn"],"removed":[]}`)
		})

		Convey("Drift commits with commit=true\n", func() {
			report := &interfaces.DriftReport{LineageID: "orders", KNID: "orders"}
			ls.EXPECT().CheckDrift(gomock.Any(), "Bearer t1", "orders", "orders", true, author).
				Return(&interfaces.DriftResult{DriftReport: report, Commit: "c4"}, nil)

			w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/drift?commit=true", []byte(`{"kn_id":"orders"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"commit":"c4"`)
		})

		Convey("Drift passes a bkn-backend refusal through\n", func() {
			ls.EXPECT().CheckDrift(gomock.Any(), "Bearer t1", "orders", "gone", false, author).
				Return(nil, httpError(http.StatusNotFound, perrors.BknProject_Lineage_BknBackendRequestFailed))

			w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/drift", []byte(`{"kn_id":"gone"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Lineage_BknBackendRequestFailed)
		})

		releasesURL := "/api/bkn-project/v1/lineages/orders/releases"

		Convey("Publish imports with the caller's token, records the caller and replies 201\n", func() {
			req := interfaces.PublishReleaseRequest{Branch: "project/p1", Version: "1.1.0", ProjectID: "p1", Notes: "n"}
			publisher := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
			ls.EXPECT().PublishRelease(gomock.Any(), "Bearer t1", "orders", req, publisher, author).
				Return(&interfaces.PublishReleaseResult{
					Release: &interfaces.Release{ID: "r1", NetworkID: "orders_v1_1_0"}, NetworkID: "orders_v1_1_0", Tag: "v1.1.0", Commit: "c5",
				}, nil)

			w := serveBody(handler, http.MethodPost, releasesURL,
				[]byte(`{"branch":"project/p1","version":"1.1.0","project_id":"p1","notes":"n"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
			var body map[string]any
			So(json.Unmarshal(w.Body.Bytes(), &body), ShouldBeNil)
			So(body["network_id"], ShouldEqual, "orders_v1_1_0")
			So(body["tag"], ShouldEqual, "v1.1.0")
			So(body["commit"], ShouldEqual, "c5")
			So(body["release"].(map[string]any)["id"], ShouldEqual, "r1")
		})

		Convey("Publish replies the service error and refuses a bad body\n", func() {
			ls.EXPECT().PublishRelease(gomock.Any(), "Bearer t1", "orders", gomock.Any(), gomock.Any(), author).
				Return(nil, httpError(http.StatusConflict, perrors.BknProject_Release_Drift))

			w := serveBody(handler, http.MethodPost, releasesURL, []byte(`{"branch":"project/p1","version":"1.1.0"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_Drift)

			w = serveBody(handler, http.MethodPost, releasesURL, []byte(`not json`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Lineage_InvalidRequestBody)
		})

		Convey("A switch plan reads the target from the query\n", func() {
			ls.EXPECT().PlanSwitch(gomock.Any(), "Bearer t1", "orders", "orders_v1_1_0").Return(&interfaces.SwitchPlan{
				LineageID: "orders", FromNetworkID: "orders_v1_0_0", ToNetworkID: "orders_v1_1_0",
				Schedules: []*interfaces.ActionSchedule{{ID: "s1", Name: "nightly", ActionTypeID: "restock", Status: "active"}},
			}, nil)

			w := serveBody(handler, http.MethodGet, "/api/bkn-project/v1/lineages/orders/switch-plan?to=orders_v1_1_0", nil)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			var body map[string]any
			So(json.Unmarshal(w.Body.Bytes(), &body), ShouldBeNil)
			So(body["from_network_id"], ShouldEqual, "orders_v1_0_0")
			So(body["schedules"].([]any)[0].(map[string]any)["name"], ShouldEqual, "nightly")
		})

		Convey("A switch records the caller as operator and replies 201\n", func() {
			operator := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
			ls.EXPECT().SwitchConsumers(gomock.Any(), "Bearer t1", "orders", interfaces.SwitchRequest{ToNetworkID: "orders"}, operator).
				Return(&interfaces.Switch{ID: "w1", FromNetworkID: "orders_v1_0_0", ToNetworkID: "orders",
					Schedules: []interfaces.SwitchedSchedule{}}, nil)

			w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/switches", []byte(`{"to_network_id":"orders"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
			So(w.Body.String(), ShouldContainSubstring, `"to_network_id":"orders"`)

			w = serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/switches", []byte(`not json`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})
	})

	Convey("Lineage routes need a valid token\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		ls := pmock.NewMockLineageService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, ls: ls}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{}, context.Canceled)

		w := serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/branches/project/p1/sync", nil)

		So(w.Result().StatusCode, ShouldEqual, http.StatusUnauthorized)

		w = serveBody(handler, http.MethodPost, "/api/bkn-project/v1/lineages/orders/releases", []byte(`{}`))

		So(w.Result().StatusCode, ShouldEqual, http.StatusUnauthorized)
	})
}
