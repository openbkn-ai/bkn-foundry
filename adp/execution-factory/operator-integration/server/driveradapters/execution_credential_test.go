// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// serveDelegated runs the public authentication middleware on route, as the router registers it
// under the version prefix, with an execution credential and the network header given.
func serveDelegated(hydra interfaces.Hydra, appKeys interfaces.AppKeyVerifier, method, route, path, network string) (int, *interfaces.AccountAuthContext) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/agent-operator-integration/v1")
	group.Use(middlewareIntrospectVerify(hydra, appKeys))
	var seen *interfaces.AccountAuthContext
	group.Handle(method, route, func(c *gin.Context) {
		seen, _ = common.GetAccountAuthContextFromCtx(c.Request.Context())
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/agent-operator-integration/v1"+path, nil)
	req.Header.Set("Authorization", "Bearer bex_run-1_secret")
	if network != "" {
		req.Header.Set(interfaces.HeaderKnID, network)
	}
	engine.ServeHTTP(w, req)
	return w.Code, seen
}

func TestExecutionCredentialsRunOnlyWhatAnAnswerRunsOnTheirNetwork(t *testing.T) {
	Convey("An execution credential on the public routes", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		hydra := mocks.NewMockHydra(ctrl)
		appKeys := mocks.NewMockAppKeyVerifier(ctrl)
		verified := &interfaces.TokenInfo{Active: true, VisitorID: "delegator", VisitorTyp: interfaces.RealName,
			DelegationID: "run-1", DelegatedKnID: "kn-1"}

		Convey("runs a Function and a Skill named for its network, as its delegator", func() {
			appKeys.EXPECT().Verify(gomock.Any(), "bex_run-1_secret").Return(verified, nil).Times(2)
			code, seen := serveDelegated(hydra, appKeys, http.MethodPost, "/function/execute", "/function/execute", "kn-1")
			So(code, ShouldEqual, http.StatusOK)
			So(seen.AccountID, ShouldEqual, "delegator")
			code, _ = serveDelegated(hydra, appKeys, http.MethodPost, "/skills/:skill_id/execute", "/skills/s1/execute", "kn-1")
			So(code, ShouldEqual, http.StatusOK)
		})
		Convey("is refused for another network, or with none named", func() {
			appKeys.EXPECT().Verify(gomock.Any(), gomock.Any()).Return(verified, nil).Times(2)
			code, seen := serveDelegated(hydra, appKeys, http.MethodPost, "/function/execute", "/function/execute", "kn-2")
			So(code, ShouldEqual, http.StatusForbidden)
			So(seen, ShouldBeNil)
			code, _ = serveDelegated(hydra, appKeys, http.MethodPost, "/function/execute", "/function/execute", "")
			So(code, ShouldEqual, http.StatusForbidden)
		})
		Convey("is refused on any other route before it is verified, and never taken for a sign-in", func() {
			code, _ := serveDelegated(hydra, appKeys, http.MethodPost, "/impex/import/:type", "/impex/import/toolbox", "kn-1")
			So(code, ShouldEqual, http.StatusUnauthorized)
			code, _ = serveDelegated(hydra, appKeys, http.MethodPost, "/tool-box/:box_id/tool/:tool_id", "/tool-box/b/tool/t", "kn-1")
			So(code, ShouldEqual, http.StatusUnauthorized)
			code, _ = serveDelegated(hydra, nil, http.MethodPost, "/function/execute", "/function/execute", "kn-1")
			So(code, ShouldEqual, http.StatusUnauthorized)
		})
	})
	Convey("The audit names the credential an execution ran with", t, func() {
		So(executionAuditAuthMethod("Bearer bex_run-1_secret"), ShouldEqual, "execution_grant")
		So(executionAuditAuthMethod("Bearer bak_k_s"), ShouldEqual, "api_key")
		So(executionAuditAuthMethod("Bearer ory_at_x"), ShouldEqual, "oauth")
	})
}
