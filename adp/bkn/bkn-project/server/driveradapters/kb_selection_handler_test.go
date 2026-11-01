// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/licverify"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

type emptyKBStore struct {
	interfaces.KBResourceAccess
	cleared bool
}

func (s *emptyKBStore) Read(context.Context, string) (*interfaces.KBResourceState, error) {
	return &interfaces.KBResourceState{}, nil
}
func (s *emptyKBStore) Clear(context.Context, string) error { s.cleared = true; return nil }
func (s *emptyKBStore) Cleanup(context.Context, string) ([]*interfaces.KBSelection, error) {
	return nil, nil
}

func TestSelectionHTTPBoundary(t *testing.T) {
	Convey("Source state uses the existing project and user boundary", t, func() {
		reset := setGinMode()
		defer reset()
		kb.ResetForTest()
		entitlement.ResetForTest()
		defer kb.ResetForTest()
		defer entitlement.ResetForTest()
		entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
		kb.Register(kb.Providers{Source: kb.UnavailableSource{}, Concepts: kb.UnavailableConcepts{}})
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		as := pmock.NewMockAuthService(ctrl)
		store := &emptyKBStore{}
		r := &restHandler{as: as, ps: ps, kbra: store}
		engine := gin.New()
		engine.Use(r.LanguageMiddleware())
		r.registerKnowledgeBaseResources(engine)
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{ID: "u", Type: hydra.VisitorType_User, TokenID: "Bearer test"}, nil)
		send := func(method, path, body string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/api/bkn-project/v1/projects/p/kb/selection"+path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(w, req)
			return w
		}
		owner := interfaces.AccountInfo{ID: "u", Type: "user"}
		Convey("A project without sources is an explicit empty process state", func() {
			ps.EXPECT().GetProject(gomock.Any(), "Bearer test", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: owner}}, nil)
			w := send(http.MethodGet, "", "")
			So(w.Code, ShouldEqual, 200)
			So(w.Body.String(), ShouldContainSubstring, `"current":null`)
		})
		Convey("Clear-all requires a concrete confirmation and owner identity", func() {
			So(send(http.MethodPost, "/clear", `{}`).Code, ShouldEqual, 400)
			So(store.cleared, ShouldBeFalse)
			ps.EXPECT().GetProjectForStatus(gomock.Any(), "Bearer test", "p", owner).Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: owner}}, false, nil)
			So(send(http.MethodPost, "/clear", `{"confirm_clear_all":true}`).Code, ShouldEqual, 200)
			So(store.cleared, ShouldBeTrue)
		})
	})
}
