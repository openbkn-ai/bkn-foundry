package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// newGatedPublicEngine registers three public faces protected by access control according to the wiring method of rest_public_handler.go.
// route, and emulates the output of middlewareIntrospectVerify (exposed face tag + verified identity).
//
// Leave the remaining dependencies of the processor blank: when authorization is denied, it should return before touching any business logic, so if the access control call is accidentally deleted,
// The request will continue to go down and panic due to empty dependencies or return a non-403. In both cases, this use case will fail.
func newGatedPublicEngine(authService interfaces.IAuthorizationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		ctx := common.SetPublicAPIToCtx(c.Request.Context(), true)
		ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
			AccountID:   testAccountID,
			AccountType: interfaces.AccessorTypeUser,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})

	proxy := &unifiedProxyHandler{AuthService: authService}
	aiGen := &aiGenerationHandler{AuthService: authService}

	group := engine.Group("/api/agent-operator-integration/v1")
	group.POST("/function/execute", proxy.FunctionExecute)
	group.POST("/ai_generate/function/:type", aiGen.FunctionAIGeneration)
	group.GET("/ai_generate/prompt/:type", aiGen.GetPromptTemplate)
	return engine
}

// TestGatedPublicRoutesRejectUnauthorized Guards the gated wiring points.
//
// authz_test.go only covers the requireOperatorTypePermission auxiliary function itself; if someone refactors.
// When proxy.go / ai_generation.go deletes the call line in handler, those use cases will still be all green. This use case.
// Takes the full gin route, so it will fail immediately when the call point is removed.
func TestGatedPublicRoutesRejectUnauthorized(t *testing.T) {
	type route struct {
		method string
		path   string
		body   string
	}
	routes := []route{
		{http.MethodPost, "/api/agent-operator-integration/v1/function/execute", `{"code":"print(1)","language":"python"}`},
		{http.MethodPost, "/api/agent-operator-integration/v1/ai_generate/function/code", `{}`},
		{http.MethodGet, "/api/agent-operator-integration/v1/ai_generate/prompt/code", ""},
	}

	Convey("缺权限时三条公开面路由一律拒绝", t, func() {
		for _, r := range routes {
			ctrl := gomock.NewController(t)
			authService := mocks.NewMockIAuthorizationService(ctrl)
			authService.EXPECT().
				OperationCheckAll(gomock.Any(), gomock.Any(), interfaces.ResourceIDAll,
					interfaces.AuthResourceTypeOperator, gomock.Any()).
				Return(false, nil).
				Times(1)

			engine := newGatedPublicEngine(authService)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, req)

			So(recorder.Code, ShouldEqual, http.StatusForbidden)
			ctrl.Finish()
		}
	})

	Convey("门禁在解析请求体之前生效，畸形请求体同样先被拒", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		authService := mocks.NewMockIAuthorizationService(ctrl)
		authService.EXPECT().
			OperationCheckAll(gomock.Any(), gomock.Any(), interfaces.ResourceIDAll,
				interfaces.AuthResourceTypeOperator, interfaces.AuthOperationTypeExecute).
			Return(false, nil)

		engine := newGatedPublicEngine(authService)
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost,
			"/api/agent-operator-integration/v1/function/execute",
			strings.NewReader("not-json-at-all"))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, req)

		// If the access control comes after ShouldBindJSON, this will be 400 instead of 403.
		So(recorder.Code, ShouldEqual, http.StatusForbidden)
	})
}
