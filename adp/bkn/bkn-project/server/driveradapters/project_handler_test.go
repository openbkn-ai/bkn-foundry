// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
		ps.EXPECT().MarkManageable(gomock.Any(), "Bearer t", caller, gomock.Any()).AnyTimes()
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

		Convey("Create passes the confirmation to turn versioning on\n", func() {
			ps.EXPECT().CreateProject(gomock.Any(), "Bearer t", interfaces.CreateProjectRequest{
				Name: "Orders", KNID: "orders", EnableVersioning: true,
			}, caller, interfaces.GitAuthor{Name: "u1"}).Return(entry, nil)

			w := serveBody(handler, http.MethodPost, base, []byte(`{"name":"Orders","kn_id":"orders","enable_versioning":true}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
		})

		Convey("Create refuses a body that is not JSON\n", func() {
			w := serveBody(handler, http.MethodPost, base, []byte(`{`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Project_InvalidRequestBody)
		})

		Convey("List passes the filters, limit and cursor\n", func() {
			ps.EXPECT().ListProjects(gomock.Any(), "Bearer t", "all", "ord", 5, 0, "c1").
				Return(&interfaces.ProjectList{Entries: []*interfaces.ProjectEntry{entry}, Total: 1}, nil)

			w := serve(handler, http.MethodGet, base+"?archived=all&name=ord&limit=5&cursor=c1")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"total":1`)
			So(w.Body.String(), ShouldContainSubstring, `"next_cursor":null`)
		})

		Convey("List uses the default limit and rejects one out of range\n", func() {
			ps.EXPECT().ListProjects(gomock.Any(), "Bearer t", "", "", interfaces.DEFAULT_PROJECT_LIMIT, 0, "").
				Return(&interfaces.ProjectList{Entries: []*interfaces.ProjectEntry{}}, nil)

			So(serve(handler, http.MethodGet, base).Result().StatusCode, ShouldEqual, http.StatusOK)

			w := serve(handler, http.MethodGet, base+"?limit=101")
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_InvalidParameter_Limit)
		})

		Convey("List passes the offset of a page by number and rejects one that is not a count\n", func() {
			ps.EXPECT().ListProjects(gomock.Any(), "Bearer t", "false", "", 12, 24, "").
				Return(&interfaces.ProjectList{Entries: []*interfaces.ProjectEntry{entry}, Total: 30}, nil)

			So(serve(handler, http.MethodGet, base+"?archived=false&limit=12&offset=24").Result().StatusCode,
				ShouldEqual, http.StatusOK)

			for _, offset := range []string{"-1", "x"} {
				w := serve(handler, http.MethodGet, base+"?offset="+offset)
				So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
				So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_InvalidParameter_Offset)
			}
		})

		Convey("Get replies the project with the catalogs it reads, or the service's not found\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), "Bearer t", "p1").Return(entry, nil)
			ps.EXPECT().ResolveCatalogs(gomock.Any(), "Bearer t", entry, caller).
				Do(func(_ context.Context, _ string, e *interfaces.ProjectEntry, _ interfaces.AccountInfo) {
					e.NetworkCatalogIDs = []string{"c-network"}
				})
			w := serve(handler, http.MethodGet, base+"/p1")
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"network_catalog_ids":["c-network"]`)

			ps.EXPECT().GetProject(gomock.Any(), "Bearer t", "p2").
				Return(nil, httperrors.NewHTTPError(context.Background(), http.StatusNotFound, perrors.BknProject_Project_NotFound))
			w = serve(handler, http.MethodGet, base+"/p2")
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

		Convey("PATCH renames, DELETE deletes with 204, for the caller\n", func() {
			ps.EXPECT().RenameProject(gomock.Any(), "Bearer t", "p1", "New", caller).Return(entry, nil)
			So(serveBody(handler, http.MethodPatch, base+"/p1", []byte(`{"name":"New"}`)).Result().StatusCode,
				ShouldEqual, http.StatusOK)
			So(serveBody(handler, http.MethodPatch, base+"/p1", []byte(`not json`)).Result().StatusCode,
				ShouldEqual, http.StatusBadRequest)

			ps.EXPECT().DeleteProject(gomock.Any(), "Bearer t", "p1", caller).Return(nil)
			So(serve(handler, http.MethodDelete, base+"/p1").Result().StatusCode, ShouldEqual, http.StatusNoContent)
		})

		Convey("Export replies the package as an attachment named after the project\n", func() {
			ps.EXPECT().ExportProject(gomock.Any(), "Bearer t", "p1", caller).Return([]byte("PK"), "订单 项目.bknproj", nil)
			w := serve(handler, http.MethodGet, base+"/p1/export")
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Header().Get("Content-Type"), ShouldEqual, "application/zip")
			So(w.Header().Get("Content-Disposition"), ShouldStartWith, "attachment; filename*=utf-8''")
			So(w.Body.String(), ShouldEqual, "PK")
		})

		Convey("Preview and import read the package of the multipart field file\n", func() {
			upload := func(path string, fields map[string]string, file []byte) *httptest.ResponseRecorder {
				body := &bytes.Buffer{}
				mw := multipart.NewWriter(body)
				if file != nil {
					fw, _ := mw.CreateFormFile("file", "p.bknproj")
					_, _ = fw.Write(file)
				}
				for k, v := range fields {
					_ = mw.WriteField(k, v)
				}
				_ = mw.Close()
				return serveUpload(handler, path, body, mw.FormDataContentType())
			}
			preview := &interfaces.ProjectPackagePreview{Name: "Orders"}
			ps.EXPECT().PreviewPackage(gomock.Any(), []byte("PK")).Return(preview, nil)
			So(upload("/api/bkn-project/v1/project-packages/preview", nil, []byte("PK")).Code, ShouldEqual, http.StatusOK)
			So(upload("/api/bkn-project/v1/project-packages/preview", nil, nil).Code, ShouldEqual, http.StatusBadRequest)

			options := interfaces.ImportProjectOptions{KNID: "orders", CatalogMapping: map[string]string{"c1": "c9"}}
			ps.EXPECT().ImportPackage(gomock.Any(), "Bearer t", []byte("PK"), options, caller, gomock.Any()).Return(entry, nil)
			So(upload("/api/bkn-project/v1/project-packages/import",
				map[string]string{"options": `{"kn_id":"orders","catalog_mapping":{"c1":"c9"}}`}, []byte("PK")).Code,
				ShouldEqual, http.StatusCreated)
			So(upload("/api/bkn-project/v1/project-packages/import", map[string]string{"options": "nope"}, []byte("PK")).Code,
				ShouldEqual, http.StatusBadRequest)
		})

		Convey("Setting the naming language is gone: a project keeps the language it was created in\n", func() {
			So(serveBody(handler, http.MethodPut, base+"/p1/naming-language", []byte(`{"naming_language":"en-US"}`)).
				Result().StatusCode, ShouldEqual, http.StatusNotFound)
		})

		Convey("Any other POST on a project is not found\n", func() {
			for _, path := range []string{base + "/p1", base + "/p1:delete", base + "/:archive"} {
				So(serve(handler, http.MethodPost, path).Result().StatusCode, ShouldEqual, http.StatusNotFound)
			}
		})
	})
}
