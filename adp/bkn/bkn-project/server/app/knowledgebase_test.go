// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
)

type coreRoutes struct{}

func (coreRoutes) RegisterPublic(engine *gin.Engine) {
	engine.GET("/health", func(c *gin.Context) { c.Status(204) })
}

func TestKnowledgeBaseHTTPAssembly(t *testing.T) {
	Convey("The actual application dispatcher preserves community responses", t, func() {
		knowledgebase.ResetForTest()
		entitlement.ResetForTest()
		defer knowledgebase.ResetForTest()
		defer entitlement.ResetForTest()
		edition := licverify.EditionCommunity
		entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot {
			return entitlement.Snapshot{Edition: edition}
		}))
		application := &Application{restHandler: coreRoutes{}}
		community := application.httpHandler()
		application.RegisterKnowledgeBaseRoutes(func(engine *gin.Engine) {
			engine.GET("/kb/catalogs/", func(c *gin.Context) { c.Status(204) })
		})
		So(func() { application.RegisterKnowledgeBaseRoutes(func(*gin.Engine) {}) }, ShouldPanic)
		handler := application.httpHandler()
		request := func(router http.Handler, method, path string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			return w
		}
		compare := func() {
			for _, method := range []string{"GET", "POST", "HEAD", "OPTIONS"} {
				for _, path := range []string{"/kb/catalogs/", "/kb/catalogs", "/kb//catalogs/", "/health"} {
					got, want := request(handler, method, path), request(community, method, path)
					So(got.Code, ShouldEqual, want.Code)
					So(got.Body.String(), ShouldEqual, want.Body.String())
					So(got.Header(), ShouldResemble, want.Header())
				}
			}
		}
		edition = licverify.EditionProfessional
		compare()
		knowledgebase.Register(knowledgebase.Providers{Source: knowledgebase.UnavailableSource{}, Concepts: knowledgebase.UnavailableConcepts{}})
		entitlement.Freeze()
		edition = licverify.EditionCommunity
		compare()
		edition = licverify.EditionProfessional
		So(request(handler, "GET", "/kb/catalogs/").Code, ShouldEqual, 204)
		So(request(handler, "GET", "/health").Code, ShouldEqual, 204)
		edition = licverify.EditionCommunity
		compare()
		So(func() { (&Application{}).RegisterKnowledgeBaseRoutes(func(*gin.Engine) {}) }, ShouldPanic)
	})
}
