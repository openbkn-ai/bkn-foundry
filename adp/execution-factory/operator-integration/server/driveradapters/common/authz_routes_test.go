package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// newGatedPublicEngine 按 rest_public_handler.go 的接线方式注册三条受门禁保护的公开面
// 路由，并模拟 middlewareIntrospectVerify 的产出（公开面标记 + 已校验身份）。
//
// 处理器的其余依赖留空：授权被拒时应当在触达任何业务逻辑之前返回，因此若门禁调用被误删，
// 请求会继续往下走并因空依赖 panic 或返回非 403，两种情况本用例都会失败。
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

// TestGatedPublicRoutesRejectUnauthorized 守住门禁的接线点。
//
// authz_test.go 只覆盖了 requireOperatorTypePermission 这个辅助函数本身；若有人重构
// proxy.go / ai_generation.go 时删掉了 handler 里的那行调用，那些用例仍会全绿。本用例
// 走完整的 gin 路由，因此调用点被撤掉时会立刻失败。
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

		// 若门禁排在 ShouldBindJSON 之后，这里会是 400 而不是 403。
		So(recorder.Code, ShouldEqual, http.StatusForbidden)
	})
}
