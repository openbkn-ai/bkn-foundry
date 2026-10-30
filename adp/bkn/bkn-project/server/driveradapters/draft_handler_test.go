// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
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

func Test_DraftRestHandler(t *testing.T) {
	Convey("Test draft handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		drs := pmock.NewMockDraftService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, drs: drs}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
			Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User, TokenID: "Bearer t"}, nil)
		caller := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
		const base = "/api/bkn-project/v1/projects/p1/ontology"

		Convey("draft:generate starts drafting as the caller and replies 202\n", func() {
			drs.EXPECT().GenerateDraft(gomock.Any(), "Bearer t", "p1", caller, false).
				Return(&interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_DRAFTING}, nil)

			w := serve(handler, http.MethodPost, base+"/draft:generate")

			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(w.Body.String(), ShouldContainSubstring, `"status":"drafting"`)
		})

		Convey("draft:generate discards decisions only when the body says so\n", func() {
			drs.EXPECT().GenerateDraft(gomock.Any(), "Bearer t", "p1", caller, true).
				Return(&interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_DRAFTING}, nil)

			w := serveBody(handler, http.MethodPost, base+"/draft:generate", []byte(`{"discard_decisions":true}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)

			w = serveBody(handler, http.MethodPost, base+"/draft:generate", []byte(`{"discard_decisions":`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Draft_InvalidRequestBody)
		})

		Convey("An item edit is handed to the service with what the body sets\n", func() {
			drs.EXPECT().EditDraftItem(gomock.Any(), "Bearer t", "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders",
				gomock.Any(), caller).
				DoAndReturn(func(_ context.Context, _, _, _, _ string, edit interfaces.DraftItemEdit,
					_ interfaces.AccountInfo) (*interfaces.Draft, error) {
					So(edit.UpdateTime, ShouldEqual, 7)
					So(*edit.Adopted, ShouldBeFalse)
					So(*edit.Name, ShouldEqual, "Sales orders")
					So(edit.Description, ShouldBeNil)
					return &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: 8}, nil
				})

			w := serveBody(handler, http.MethodPatch, base+"/draft/items/object_types/orders",
				[]byte(`{"update_time":7,"adopted":false,"name":"Sales orders"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"update_time":8`)
		})

		Convey("An item edit with a malformed body is refused\n", func() {
			w := serveBody(handler, http.MethodPatch, base+"/draft/items/object_types/orders", []byte(`not json`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Draft_InvalidRequestBody)
		})

		Convey("An identity choice is handed to the service\n", func() {
			drs.EXPECT().ChooseIdentity(gomock.Any(), "Bearer t", "p1", "order_lines",
				interfaces.DraftIdentity{UpdateTime: 7, Properties: []string{"order_no", "line_no"}}, caller).
				Return(&interfaces.Draft{ProjectID: "p1", UpdateTime: 8}, nil)

			w := serveBody(handler, http.MethodPut, base+"/draft/object_types/order_lines/identity",
				[]byte(`{"update_time":7,"properties":["order_no","line_no"]}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(serveBody(handler, http.MethodPut, base+"/draft/object_types/order_lines/identity", []byte(`[`)).
				Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("A question about an object type is handed to the service, and its answer replied\n", func() {
			drs.EXPECT().AskObjectType(gomock.Any(), "Bearer t", "p1", "order_lines",
				interfaces.DraftAskRequest{Question: "用什么做唯一标识？"}, caller).
				Return(&interfaces.DraftAskAnswer{Answer: "用订单号和行号。", Suggestion: &interfaces.DraftAskSuggestion{
					Field: interfaces.DRAFT_ASK_FIELD_IDENTITY, Properties: []string{"order_no", "line_no"}}}, nil)

			w := serveBody(handler, http.MethodPost, base+"/draft/object_types/order_lines/ask",
				[]byte(`{"question":"用什么做唯一标识？"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"field":"identity"`)
			So(serveBody(handler, http.MethodPost, base+"/draft/object_types/order_lines/ask", []byte(`[`)).
				Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("draft:restore restores the excluded field the body names\n", func() {
			drs.EXPECT().RestoreExcluded(gomock.Any(), "Bearer t", "p1",
				interfaces.DraftRestore{UpdateTime: 7, ObjectType: "customers", Field: "Phone"}, caller).
				Return(&interfaces.Draft{ProjectID: "p1", UpdateTime: 8}, nil)

			w := serveBody(handler, http.MethodPost, base+"/draft:restore",
				[]byte(`{"update_time":7,"object_type":"customers","field":"Phone"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(serveBody(handler, http.MethodPost, base+"/draft:restore", nil).Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("draft:confirm confirms the draft made at the update time the body names\n", func() {
			drs.EXPECT().ConfirmDraft(gomock.Any(), "Bearer t", "p1", interfaces.DraftConfirm{UpdateTime: 7}, caller).
				Return(&interfaces.Draft{ProjectID: "p1", UpdateTime: 8}, nil)

			w := serveBody(handler, http.MethodPost, base+"/draft:confirm", []byte(`{"update_time":7}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(serveBody(handler, http.MethodPost, base+"/draft:confirm", nil).Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("check:run runs the check again as the caller and replies 202\n", func() {
			drs.EXPECT().RunCheck(gomock.Any(), "Bearer t", "p1", caller).
				Return(&interfaces.Check{ProjectID: "p1", Commit: "c1", Status: interfaces.CHECK_STATUS_RUNNING}, nil)

			w := serve(handler, http.MethodPost, base+"/check:run")

			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(w.Body.String(), ShouldContainSubstring, `"status":"running"`)
		})

		Convey("catalogs:understand submits as the caller and replies 202; a bad body is refused\n", func() {
			const project = "/api/bkn-project/v1/projects/p1"
			drs.EXPECT().Understand(gomock.Any(), "Bearer t", "p1",
				interfaces.ProjectUnderstand{ApplyMode: "force", All: true}, caller).
				Return(&interfaces.ProjectUnderstandSubmitted{Catalogs: []interfaces.UnderstandSubmittedCatalog{
					{CatalogID: "c1", Permitted: true, Tables: 3}}}, nil)

			w := serveBody(handler, http.MethodPost, project+"/catalogs:understand", []byte(`{"apply_mode":"force","all":true}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(w.Body.String(), ShouldContainSubstring, `"tables":3`)

			// An empty body takes the defaults.
			drs.EXPECT().Understand(gomock.Any(), "Bearer t", "p1", interfaces.ProjectUnderstand{}, caller).
				Return(&interfaces.ProjectUnderstandSubmitted{}, nil)
			So(serveBody(handler, http.MethodPost, project+"/catalogs:understand", nil).Result().StatusCode,
				ShouldEqual, http.StatusAccepted)
			So(serveBody(handler, http.MethodPost, project+"/catalogs:understand", []byte(`{`)).Result().StatusCode,
				ShouldEqual, http.StatusBadRequest)
			So(serve(handler, http.MethodPost, project+"/catalogs:unknown").Result().StatusCode, ShouldEqual, http.StatusNotFound)
		})

		Convey("The source changes are read as the caller, and source-changes:draft drafts them with the signature read\n", func() {
			const project = "/api/bkn-project/v1/projects/p1"
			drs.EXPECT().GetSourceChanges(gomock.Any(), "Bearer t", "p1", caller).
				Return(&interfaces.SourceChanges{ProjectID: "p1", State: interfaces.SOURCE_CHANGES_CHANGED, Signature: "s1"}, nil)

			w := serve(handler, http.MethodGet, project+"/source-changes")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"state":"changed"`)

			drs.EXPECT().DraftSourceChanges(gomock.Any(), "Bearer t", "p1", interfaces.SourceChangesDraft{Signature: "s1"}, caller).
				Return(&interfaces.Draft{ProjectID: "p1"}, nil)
			So(serveBody(handler, http.MethodPost, project+"/source-changes:draft", []byte(`{"signature":"s1"}`)).Result().StatusCode,
				ShouldEqual, http.StatusOK)
			So(serveBody(handler, http.MethodPost, project+"/source-changes:draft", []byte(`{`)).Result().StatusCode,
				ShouldEqual, http.StatusBadRequest)
		})

		Convey("The understanding is read as the caller\n", func() {
			drs.EXPECT().GetUnderstanding(gomock.Any(), "Bearer t", "p1", caller).
				Return(&interfaces.ProjectUnderstanding{Catalogs: []interfaces.UnderstandingCatalog{
					{CatalogID: "c1", Visible: true, Counts: map[string]int{"completed": 1}}}}, nil)

			w := serve(handler, http.MethodGet, "/api/bkn-project/v1/projects/p1/catalogs/understanding")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"visible":true`)
		})

		Convey("index:build builds the indexes again as the caller and replies the readiness\n", func() {
			drs.EXPECT().BuildIndex(gomock.Any(), "Bearer t", "p1", caller).
				Return(&interfaces.ProjectIndex{ObjectTypes: []interfaces.ProjectIndexItem{{ID: "orders",
					State: interfaces.INDEX_STATE_BUILDING, Build: &interfaces.ProjectIndexBuild{Outcome: interfaces.INDEX_BUILD_CREATED}}},
					Counts: map[string]int{interfaces.INDEX_STATE_BUILDING: 1}, TasksVisible: true}, nil)

			w := serve(handler, http.MethodPost, base+"/index:build")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"outcome":"created"`)
		})

		Convey("The index readiness is read as the caller\n", func() {
			drs.EXPECT().GetIndex(gomock.Any(), "Bearer t", "p1", caller).
				Return(&interfaces.ProjectIndex{ObjectTypes: []interfaces.ProjectIndexItem{{ID: "orders", State: interfaces.INDEX_STATE_READY}},
					Counts: map[string]int{interfaces.INDEX_STATE_READY: 1}, TasksVisible: true}, nil)
			w := serve(handler, http.MethodGet, base+"/index")
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"state":"ready"`)
		})

		Convey("The check is replied as is, or with the service's error\n", func() {
			drs.EXPECT().GetCheck(gomock.Any(), "Bearer t", "p1").
				Return(&interfaces.Check{ProjectID: "p1", Commit: "c1", RunID: "r1", Status: interfaces.CHECK_STATUS_PASSED}, nil)
			w := serve(handler, http.MethodGet, base+"/check")
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"commit":"c1"`)
			So(w.Body.String(), ShouldNotContainSubstring, "r1")

			drs.EXPECT().GetCheck(gomock.Any(), "Bearer t", "p1").
				Return(nil, httperrors.NewHTTPError(context.Background(), http.StatusNotFound, perrors.BknProject_Check_NotFound))
			w = serve(handler, http.MethodGet, base+"/check")
			So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Check_NotFound)
		})

		Convey("The status is computed for the caller\n", func() {
			drs.EXPECT().GetStatus(gomock.Any(), "Bearer t", "p1", caller).
				Return(&interfaces.ProjectStatus{ProjectID: "p1", Stage: interfaces.PROJECT_STAGE_DECIDING}, nil)

			w := serve(handler, http.MethodGet, "/api/bkn-project/v1/projects/p1/status")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"stage":"deciding"`)
		})

		Convey("The pending items are listed\n", func() {
			drs.EXPECT().GetPending(gomock.Any(), "Bearer t", "p1").
				Return(&interfaces.ProjectPending{ProjectID: "p1", Items: []interfaces.PendingItem{}}, nil)

			w := serve(handler, http.MethodGet, "/api/bkn-project/v1/projects/p1/pending")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"items":[]`)
		})

		Convey("Any other ontology action is not found\n", func() {
			So(serve(handler, http.MethodPost, base+"/draft:delete").Result().StatusCode, ShouldEqual, http.StatusNotFound)
		})

		Convey("The draft is replied as is, or with the service's error\n", func() {
			drs.EXPECT().GetDraft(gomock.Any(), "Bearer t", "p1").
				Return(&interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY}, nil)
			So(serve(handler, http.MethodGet, base+"/draft").Result().StatusCode, ShouldEqual, http.StatusOK)

			drs.EXPECT().GetDraft(gomock.Any(), "Bearer t", "p1").
				Return(nil, httperrors.NewHTTPError(context.Background(), http.StatusNotFound, perrors.BknProject_Draft_NotFound))
			w := serve(handler, http.MethodGet, base+"/draft")
			So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Draft_NotFound)
		})
	})
}
