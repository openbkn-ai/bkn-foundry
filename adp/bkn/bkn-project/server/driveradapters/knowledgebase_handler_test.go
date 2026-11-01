// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/licverify"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

type resourceSource struct {
	kb.UnavailableSource
	summary func(context.Context, kb.SourceScope, kb.FileQuery) (kb.FileSummary, error)
}

func (s resourceSource) SummarizeFiles(ctx context.Context, scope kb.SourceScope, q kb.FileQuery) (kb.FileSummary, error) {
	return s.summary(ctx, scope, q)
}

func TestKnowledgeBaseResourceReads(t *testing.T) {
	Convey("Resource reads use the authenticated operator and authorize the project first", t, func() {
		cleanup := setGinMode()
		defer cleanup()
		kb.ResetForTest()
		entitlement.ResetForTest()
		defer kb.ResetForTest()
		defer entitlement.ResetForTest()
		entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		as := pmock.NewMockAuthService(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		r := &restHandler{as: as, ps: ps}
		upstreamErr := error(nil)
		called := false
		kb.Register(kb.Providers{Source: resourceSource{summary: func(ctx context.Context, scope kb.SourceScope, q kb.FileQuery) (kb.FileSummary, error) {
			called = true
			So(kb.OperatorAuthorization(ctx), ShouldEqual, "Bearer synthetic-operator")
			So(scope.KnowledgeBaseID, ShouldEqual, "2")
			So(scope.NetworkID, ShouldBeEmpty)
			So(q.CatalogID, ShouldEqual, "opaque/catalog+")
			So(q.Recursive, ShouldBeTrue)
			So(q.MimeTypes, ShouldResemble, []string{"text/plain", "application/pdf"})
			return kb.FileSummary{FileCount: 12, VersionedFileCount: 10, ChunkCount: 56}, upstreamErr
		}}, Concepts: kb.UnavailableConcepts{}})
		engine := gin.New()
		engine.Use(r.LanguageMiddleware())
		r.registerKnowledgeBaseResources(engine)
		request := func(query string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/bkn-project/v1/projects/p/kb/summary"+query, nil)
			req.Header.Set("Accept-Language", "en-US")
			engine.ServeHTTP(w, req)
			return w
		}
		query := "?knowledge_base_id=2&catalog_id=opaque%2Fcatalog%2B&include_subcatalogs=true&mime_type=text%2Fplain&mime_type=application%2Fpdf"
		visitor := hydra.Visitor{ID: "u", Type: hydra.VisitorType_User, TokenID: "Bearer synthetic-operator"}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(context.Context, *gin.Context) (hydra.Visitor, error) { return visitor, nil })
		Convey("The source is not contacted when the project is hidden", func() {
			ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic-operator", "p").Return(nil, httperrors.NewHTTPError(context.Background(), 404, httperrors.PublicError_NotFound))
			So(request(query).Code, ShouldEqual, 404)
			So(called, ShouldBeFalse)
		})
		Convey("An application cannot borrow the interactive user's path", func() {
			visitor.Type = hydra.VisitorType_App
			So(request(query).Code, ShouldEqual, 403)
			So(called, ShouldBeFalse)
		})
		Convey("Summary reports current counts, not a fabricated elapsed-time estimate", func() {
			ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic-operator", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p"}}, nil)
			w := request(query)
			So(w.Code, ShouldEqual, 200)
			So(w.Body.String(), ShouldContainSubstring, `"file_count":12`)
			So(w.Body.String(), ShouldNotContainSubstring, "synthetic-operator")
		})
		Convey("Version loss and authorization failures remain distinct", func() {
			for _, status := range []int{401, 403, 404, 409, 410, 503} {
				if status == 410 {
					upstreamErr = kb.ErrVersionGone
				} else {
					upstreamErr = &kb.SourceError{StatusCode: status}
				}
				ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic-operator", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p"}}, nil)
				w := request(query)
				So(w.Code, ShouldEqual, status)
				if status == 410 {
					So(w.Body.String(), ShouldContainSubstring, "no longer kept")
				}
			}
		})
		Convey("Invalid filters are rejected before reading the source", func() {
			ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic-operator", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p"}}, nil)
			So(request("?knowledge_base_id=2&include_subcatalogs=maybe").Code, ShouldEqual, 400)
			So(called, ShouldBeFalse)
		})
	})
}
