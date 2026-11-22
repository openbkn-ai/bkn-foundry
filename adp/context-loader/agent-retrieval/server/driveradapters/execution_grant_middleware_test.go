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
	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type stubExecutionGrants struct{ called *bool }

func (s stubExecutionGrants) Verify(_ context.Context, _ string) (*interfaces.TokenInfo, error) {
	*s.called = true
	return &interfaces.TokenInfo{VisitorID: "delegator", VisitorTyp: interfaces.RealName, DelegationID: "run-1", DelegatedKnID: "kn-1"}, nil
}

// serve runs the middleware on route, as the router matched it.
func serve(appKeys interfaces.AppKeyVerifier, route, token string) (int, *interfaces.AccountAuthContext) {
	engine := gin.New()
	var auth *interfaces.AccountAuthContext
	engine.Any(route, middlewareIntrospectVerify(stubPublicHydra{}, appKeys), func(c *gin.Context) {
		auth, _ = common.GetAccountAuthContextFromCtx(c.Request.Context())
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	path := map[string]string{"/api/agent-retrieval/v1/mcp/*path": "/api/agent-retrieval/v1/mcp/",
		"/api/agent-retrieval/v1/kn/search_schema": "/api/agent-retrieval/v1/kn/search_schema"}[route]
	req := httptest.NewRequest(http.MethodPost, path, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(w, req)
	return w.Code, auth
}

func TestExecutionCredentialsActThroughMCPOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	convey.Convey("An execution credential", t, func() {
		convey.Convey("is verified by bkn-safe on an MCP route and acts for its delegator", func() {
			called := false
			code, auth := serve(stubExecutionGrants{called: &called}, "/api/agent-retrieval/v1/mcp/*path", "bex_run-1_secret")
			convey.So(code, convey.ShouldEqual, http.StatusOK)
			convey.So(called, convey.ShouldBeTrue)
			convey.So(auth.AccountID, convey.ShouldEqual, "delegator")
			convey.So(auth.AuthMethod, convey.ShouldEqual, "execution_grant")
			convey.So(auth.TokenInfo.DelegatedKnID, convey.ShouldEqual, "kn-1")
		})
		convey.Convey("is refused on any other route, before it is verified", func() {
			called := false
			code, _ := serve(stubExecutionGrants{called: &called}, "/api/agent-retrieval/v1/kn/search_schema", "bex_run-1_secret")
			convey.So(code, convey.ShouldEqual, http.StatusUnauthorized)
			convey.So(called, convey.ShouldBeFalse)
		})
		convey.Convey("is never taken for a sign-in when bkn-safe cannot verify it", func() {
			code, _ := serve(nil, "/api/agent-retrieval/v1/mcp/*path", "bex_run-1_secret")
			convey.So(code, convey.ShouldEqual, http.StatusUnauthorized)
		})
	})
}
