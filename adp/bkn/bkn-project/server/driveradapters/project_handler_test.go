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

func Test_ProjectRestHandler(t *testing.T) {
	Convey("Test project handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		ps := pmock.NewMockProjectService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, ps: ps}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
			Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User, TokenID: "Bearer t"}, nil)
		caller := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
		const base = "/api/bkn-project/v1/projects"
		entry := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "orders"}, OtherProjects: 1}

		Convey("Create passes the body, the caller as owner and author, and replies 201\n", func() {
			ps.EXPECT().CreateProject(gomock.Any(), "Bearer t", interfaces.CreateProjectRequest{
				Name: "Orders", NewKN: &interfaces.NewKNRequest{Name: "Orders"}, CatalogIDs: []string{"c1"},
			}, caller, interfaces.GitAuthor{Name: "u1"}).Return(entry, nil)

			w := serveBody(handler, http.MethodPost, base,
				[]byte(`{"name":"Orders","new_kn":{"name":"Orders"},"catalog_ids":["c1"]}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
			So(w.Body.String(), ShouldContainSubstring, `"id":"p1"`)
			So(w.Body.String(), ShouldContainSubstring, `"other_projects":1`)
		})

		Convey("Create refuses a body that is not JSON\n", func() {
			w := serveBody(handler, http.MethodPost, base, []byte(`{`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Project_InvalidRequestBody)
		})

		Convey("List passes the filters, limit and cursor\n", func() {
			ps.EXPECT().ListProjects(gomock.Any(), "Bearer t", "all", "ord", 5, "c1").
				Return(&interfaces.ProjectList{Entries: []*interfaces.ProjectEntry{entry}, Total: 1}, nil)

			w := serve(handler, http.MethodGet, base+"?archived=all&name=ord&limit=5&cursor=c1")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"total":1`)
			So(w.Body.String(), ShouldContainSubstring, `"next_cursor":null`)
		})

		Convey("List uses the default limit and rejects one out of range\n", func() {
			ps.EXPECT().ListProjects(gomock.Any(), "Bearer t", "", "", interfaces.DEFAULT_PROJECT_LIMIT, "").
				Return(&interfaces.ProjectList{Entries: []*interfaces.ProjectEntry{}}, nil)

			So(serve(handler, http.MethodGet, base).Result().StatusCode, ShouldEqual, http.StatusOK)

			w := serve(handler, http.MethodGet, base+"?limit=101")
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_InvalidParameter_Limit)
		})

		Convey("Get replies the project, or the service's not found\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), "Bearer t", "p1").Return(entry, nil)
			So(serve(handler, http.MethodGet, base+"/p1").Result().StatusCode, ShouldEqual, http.StatusOK)

			ps.EXPECT().GetProject(gomock.Any(), "Bearer t", "p2").
				Return(nil, httperrors.NewHTTPError(context.Background(), http.StatusNotFound, perrors.BknProject_Project_NotFound))
			w := serve(handler, http.MethodGet, base+"/p2")
			So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Project_NotFound)
		})

		Convey(":archive and :unarchive set the flag for the caller\n", func() {
			ps.EXPECT().SetProjectArchived(gomock.Any(), "Bearer t", "p1", true, caller).Return(entry, nil)
			So(serve(handler, http.MethodPost, base+"/p1:archive").Result().StatusCode, ShouldEqual, http.StatusOK)

			ps.EXPECT().SetProjectArchived(gomock.Any(), "Bearer t", "p1", false, caller).Return(entry, nil)
			So(serve(handler, http.MethodPost, base+"/p1:unarchive").Result().StatusCode, ShouldEqual, http.StatusOK)
		})

		Convey("PUT catalogs replaces the catalogs for the caller; the field is required\n", func() {
			ps.EXPECT().SetProjectCatalogs(gomock.Any(), "Bearer t", "p1", []string{"c2"}, caller).Return(entry, nil)
			So(serveBody(handler, http.MethodPut, base+"/p1/catalogs", []byte(`{"catalog_ids":["c2"]}`)).Result().StatusCode,
				ShouldEqual, http.StatusOK)

			ps.EXPECT().SetProjectCatalogs(gomock.Any(), "Bearer t", "p1", []string{}, caller).Return(entry, nil)
			So(serveBody(handler, http.MethodPut, base+"/p1/catalogs", []byte(`{"catalog_ids":[]}`)).Result().StatusCode,
				ShouldEqual, http.StatusOK)

			for _, body := range []string{`{}`, `{"catalog_ids":null}`, `not json`} {
				So(serveBody(handler, http.MethodPut, base+"/p1/catalogs", []byte(body)).Result().StatusCode,
					ShouldEqual, http.StatusBadRequest)
			}
		})

		Convey("Any other POST on a project is not found\n", func() {
			for _, path := range []string{base + "/p1", base + "/p1:delete", base + "/:archive"} {
				So(serve(handler, http.MethodPost, path).Result().StatusCode, ShouldEqual, http.StatusNotFound)
			}
		})
	})
}
