package common

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
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
// authz_test.go only covers the requireFunctionPermission auxiliary function itself; if someone refactors.
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
			resourceID := interfaces.ResourceIDAll
			if strings.HasSuffix(r.path, "/function/execute") {
				resourceID = "adhoc"
			}
			authService.EXPECT().
				OperationCheckAll(gomock.Any(), gomock.Any(), resourceID,
					interfaces.AuthResourceTypeFunction, gomock.Any()).
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
			OperationCheckAll(gomock.Any(), gomock.Any(), "adhoc",
				interfaces.AuthResourceTypeFunction, interfaces.AuthOperationTypeExecute).
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

// TestFunctionExecuteAuthorizationOutcomes guards #1533: the caller must be able
// to tell a missing execute grant from an authorization outage, and neither may
// reach the sandbox. The engine leaves SessionPool nil, so a request that slips
// past the gate panics instead of passing.
func TestFunctionExecuteAuthorizationOutcomes(t *testing.T) {
	const path = "/api/agent-operator-integration/v1/function/execute"
	const body = `{"code":"import datetime\nprint(datetime.datetime.now().year)","language":"python"}`

	serve := func(authService interfaces.IAuthorizationService) *httptest.ResponseRecorder {
		engine := newGatedPublicEngine(authService)
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, req)
		return recorder
	}

	Convey("a denial is a 403 that names the missing function permission", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		authService := mocks.NewMockIAuthorizationService(ctrl)
		authService.EXPECT().
			OperationCheckAll(gomock.Any(), gomock.Any(), "adhoc",
				interfaces.AuthResourceTypeFunction, interfaces.AuthOperationTypeExecute).
			Return(false, nil)

		recorder := serve(authService)

		So(recorder.Code, ShouldEqual, http.StatusForbidden)
		var reply struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		}
		So(json.Unmarshal(recorder.Body.Bytes(), &reply), ShouldBeNil)
		So(reply.Code, ShouldEndWith, "."+errors.ErrExtCommonUseForbidden.String())
		So(reply.Details, ShouldResemble, map[string]any{
			"resource_type": string(interfaces.AuthResourceTypeFunction),
			"resource_id":   "adhoc",
			"operation":     string(interfaces.AuthOperationTypeExecute),
		})
	})

	Convey("an authorization outage stays a 503 and never becomes a 403", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		authService := mocks.NewMockIAuthorizationService(ctrl)
		outage := errors.NewHTTPError(context.Background(), http.StatusServiceUnavailable,
			errors.ErrExtCommonAuthorizationUnavailable, nil)
		authService.EXPECT().
			OperationCheckAll(gomock.Any(), gomock.Any(), "adhoc",
				interfaces.AuthResourceTypeFunction, interfaces.AuthOperationTypeExecute).
			Return(false, outage)

		recorder := serve(authService)

		So(recorder.Code, ShouldEqual, http.StatusServiceUnavailable)
		So(recorder.Body.String(), ShouldContainSubstring, errors.ErrExtCommonAuthorizationUnavailable.String())
	})
}
