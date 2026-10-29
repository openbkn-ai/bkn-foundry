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
	"strings"
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
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/requirement"
)

// multipartUpload builds a multipart body with files under field.
func multipartUpload(field string, files map[string]string, order []string) (*bytes.Buffer, string) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, name := range order {
		part, _ := w.CreateFormFile(field, name)
		_, _ = part.Write([]byte(files[name]))
	}
	_ = w.Close()
	return &body, w.FormDataContentType()
}

func serveUpload(handler *restHandler, path string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	engine := gin.New()
	engine.Use(gin.Recovery())
	handler.RegisterPublic(engine)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", contentType)
	engine.ServeHTTP(w, req)
	return w
}

func Test_RequirementRestHandler(t *testing.T) {
	Convey("Test requirement handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		rqs := pmock.NewMockRequirementService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, rqs: rqs}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
			Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User, TokenID: "Bearer t"}, nil)
		caller := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
		const base = "/api/bkn-project/v1/projects/p1/requirements/documents"

		Convey("An upload hands every file under files to the service, in order\n", func() {
			body, contentType := multipartUpload("files", map[string]string{"prd.md": "# Goal", "notes.txt": "n"},
				[]string{"prd.md", "notes.txt"})
			rqs.EXPECT().UploadDocuments(gomock.Any(), "Bearer t", "p1", []interfaces.RequirementUpload{
				{Name: "prd.md", Content: []byte("# Goal")}, {Name: "notes.txt", Content: []byte("n")},
			}, caller).Return([]interfaces.RequirementUploadResult{
				{Name: "prd.md", Outcome: interfaces.REQUIREMENT_UPLOAD_CREATED, DocumentID: "d1"},
				{Name: "notes.txt", Outcome: interfaces.REQUIREMENT_UPLOAD_DUPLICATE, DocumentID: "d0"},
			}, nil)

			w := serveUpload(handler, base, body, contentType)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldEqual, `{"results":[{"name":"prd.md","outcome":"created","document_id":"d1"},`+
				`{"name":"notes.txt","outcome":"duplicate","document_id":"d0"}]}`)
		})

		Convey("A file over the limit reaches the service cut just past the limit, so it is refused as too large\n", func() {
			big := strings.Repeat("a", requirement.MaxDocumentBytes+100)
			body, contentType := multipartUpload("files", map[string]string{"big.txt": big}, []string{"big.txt"})
			rqs.EXPECT().UploadDocuments(gomock.Any(), "Bearer t", "p1", gomock.Any(), caller).DoAndReturn(
				func(_ context.Context, _, _ string, files []interfaces.RequirementUpload,
					_ interfaces.AccountInfo) ([]interfaces.RequirementUploadResult, error) {
					So(files, ShouldHaveLength, 1)
					So(len(files[0].Content), ShouldEqual, requirement.MaxDocumentBytes+1)
					return []interfaces.RequirementUploadResult{}, nil
				})

			w := serveUpload(handler, base, body, contentType)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})

		Convey("An upload whose body is over the limit is refused as too large\n", func() {
			huge := strings.Repeat("a", int(maxUploadBytes))
			body, contentType := multipartUpload("files", map[string]string{"huge.txt": huge}, []string{"huge.txt"})

			w := serveUpload(handler, base, body, contentType)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_DocumentTooLarge)
		})

		Convey("An upload without files, with too many, or not multipart is refused\n", func() {
			empty, contentType := multipartUpload("other", map[string]string{"a.md": "a"}, []string{"a.md"})
			w := serveUpload(handler, base, empty, contentType)
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_InvalidRequestBody)

			files, order := map[string]string{}, []string{}
			for i := 0; i <= requirement.MaxDocuments; i++ {
				name := string(rune('a'+i)) + ".md"
				files[name], order = "x", append(order, name)
			}
			many, contentType := multipartUpload("files", files, order)
			w = serveUpload(handler, base, many, contentType)
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_TooManyDocuments)

			w = serveBody(handler, http.MethodPost, base, []byte(`{"name":"a.md"}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_InvalidRequestBody)
		})

		Convey("The service's refusal of an upload is replied as it is\n", func() {
			body, contentType := multipartUpload("files", map[string]string{"a.md": "a"}, []string{"a.md"})
			rqs.EXPECT().UploadDocuments(gomock.Any(), "Bearer t", "p1", gomock.Any(), caller).
				Return(nil, httperrors.NewHTTPError(context.Background(), http.StatusForbidden, perrors.BknProject_Requirement_NotOwner))

			w := serveUpload(handler, base, body, contentType)

			So(w.Result().StatusCode, ShouldEqual, http.StatusForbidden)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_NotOwner)
		})

		Convey("Documents are listed and read\n", func() {
			rqs.EXPECT().ListDocuments(gomock.Any(), "Bearer t", "p1").
				Return([]*interfaces.RequirementDocument{{ID: "d1", Name: "prd.md"}}, nil)
			rqs.EXPECT().GetDocument(gomock.Any(), "Bearer t", "p1", "d1").
				Return(&interfaces.RequirementDocument{ID: "d1", Content: "body"}, nil)

			w := serve(handler, http.MethodGet, base)
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldStartWith, `{"entries":[{"id":"d1"`)
			w = serve(handler, http.MethodGet, base+"/d1")
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"content":"body"`)
		})

		Convey("A document's type is set from the body\n", func() {
			rqs.EXPECT().SetDocumentType(gomock.Any(), "Bearer t", "p1", "d1", interfaces.REQUIREMENT_DOC_POLICY, caller).
				Return(&interfaces.RequirementDocument{ID: "d1", DocType: interfaces.REQUIREMENT_DOC_POLICY}, nil)

			w := serveBody(handler, http.MethodPatch, base+"/d1", []byte(`{"doc_type":"policy"}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"doc_type":"policy"`)

			w = serveBody(handler, http.MethodPatch, base+"/d1", []byte(`{"doc_type":`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_InvalidRequestBody)
		})

		Convey("A document is deleted with 204\n", func() {
			rqs.EXPECT().DeleteDocument(gomock.Any(), "Bearer t", "p1", "d1", caller).Return(nil)

			w := serve(handler, http.MethodDelete, base+"/d1")

			So(w.Result().StatusCode, ShouldEqual, http.StatusNoContent)
		})
	})
}
