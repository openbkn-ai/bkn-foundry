package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	logicssandbox "github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/sandbox"
	. "github.com/smartystreets/goconvey/convey"
)

type fakeManagementService struct{}

func (f *fakeManagementService) GetHealth(ctx context.Context) (*logicssandbox.SandboxHealthResp, error) {
	return &logicssandbox.SandboxHealthResp{Status: "healthy", ControlPlaneReachable: true}, nil
}

func (f *fakeManagementService) GetPool(ctx context.Context) (*logicssandbox.SandboxPoolResp, error) {
	return &logicssandbox.SandboxPoolResp{MaxSessions: 3, CurrentActiveSessions: 1}, nil
}

// sentinelWorkspacePath 是跨租户敏感字段的哨兵值。门禁生效时它不应出现在响应里；
// 若断言改成恒真（例如让 fake 不填该字段，靠 omitempty 让它天然消失），这组用例
// 就测不出 requireAdmin 是否真的拦住了请求。
const sentinelWorkspacePath = "/workspace/sess_leak_probe"

func (f *fakeManagementService) ListSessions(ctx context.Context, req *logicssandbox.SandboxSessionListReq) (*logicssandbox.SandboxSessionListResp, error) {
	return &logicssandbox.SandboxSessionListResp{
		Items: []*logicssandbox.SandboxSessionSummary{
			{ID: "sess_leak_probe", UserID: "other-tenant-user"},
		},
		Total: 1,
	}, nil
}

func (f *fakeManagementService) GetSessionDetail(ctx context.Context, sessionID string) (*logicssandbox.SandboxSessionDetailResp, error) {
	return &logicssandbox.SandboxSessionDetailResp{
		SandboxSessionSummary: &logicssandbox.SandboxSessionSummary{
			ID:     sessionID,
			UserID: "other-tenant-user",
		},
		WorkspacePath: sentinelWorkspacePath,
		PodName:       "sandbox-pod-leak-probe",
	}, nil
}

func TestManagementHandlerReadOnlyRoutes(t *testing.T) {
	Convey("Sandbox management handler should expose only read-only routes", t, func() {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		group := engine.Group("/api/agent-operator-integration/internal-v1")
		NewManagementHandlerWithService(&fakeManagementService{}).RegisterPrivate(group)

		So(performRequest(engine, http.MethodGet, "/api/agent-operator-integration/internal-v1/sandbox/health").Code, ShouldEqual, http.StatusOK)
		So(performRequest(engine, http.MethodGet, "/api/agent-operator-integration/internal-v1/sandbox/pool").Code, ShouldEqual, http.StatusOK)
		So(performRequest(engine, http.MethodGet, "/api/agent-operator-integration/internal-v1/sandbox/sessions?status=failed").Code, ShouldEqual, http.StatusOK)
		So(performRequest(engine, http.MethodGet, "/api/agent-operator-integration/internal-v1/sandbox/sessions/sess_aoi_1").Code, ShouldEqual, http.StatusOK)

		So(performRequest(engine, http.MethodDelete, "/api/agent-operator-integration/internal-v1/sandbox/sessions/sess_aoi_1").Code, ShouldEqual, http.StatusNotFound)
		So(performRequest(engine, http.MethodPost, "/api/agent-operator-integration/internal-v1/sandbox/pool/prewarm").Code, ShouldEqual, http.StatusNotFound)
	})
}

func performRequest(engine *gin.Engine, method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	engine.ServeHTTP(recorder, req)
	return recorder
}

// fakeAuthService 按构造时给定的结果放行或拒绝，用于验证公开面的超管门禁。
type fakeAuthService struct {
	interfaces.IAuthorizationService
	adminErr error
	called   int
}

func (f *fakeAuthService) CheckAdminPermission(ctx context.Context, accessor *interfaces.AuthAccessor) error {
	f.called++
	return f.adminErr
}

// newPublicEngine 构造带认证上下文的公开面路由，模拟 middlewareIntrospectVerify 的产出。
func newPublicEngine(authService interfaces.IAuthorizationService, accountID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if accountID != "" {
		engine.Use(func(c *gin.Context) {
			ctx := common.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{
				AccountID:   accountID,
				AccountType: interfaces.AccessorTypeUser,
			})
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		})
	}
	group := engine.Group("/api/agent-operator-integration/v1")
	NewManagementHandlerWithAuth(&fakeManagementService{}, authService).RegisterPublic(group)
	return engine
}

func TestManagementHandlerPublicRoutesRequireAdmin(t *testing.T) {
	const base = "/api/agent-operator-integration/v1/sandbox"

	Convey("公开面沙箱观测接口限定超管可见", t, func() {
		Convey("超管可访问全部四条只读接口", func() {
			auth := &fakeAuthService{}
			engine := newPublicEngine(auth, "admin-1")

			So(performRequest(engine, http.MethodGet, base+"/health").Code, ShouldEqual, http.StatusOK)
			So(performRequest(engine, http.MethodGet, base+"/pool").Code, ShouldEqual, http.StatusOK)
			So(performRequest(engine, http.MethodGet, base+"/sessions").Code, ShouldEqual, http.StatusOK)
			detail := performRequest(engine, http.MethodGet, base+"/sessions/sess_1")
			So(detail.Code, ShouldEqual, http.StatusOK)
			So(auth.called, ShouldEqual, 4)
			// 放行时哨兵字段确实出现在响应里——否则下面「不泄露」的断言恒为真，测不出门禁
			So(detail.Body.String(), ShouldContainSubstring, sentinelWorkspacePath)
		})

		Convey("非超管一律拒绝，且不泄露会话数据", func() {
			auth := &fakeAuthService{adminErr: errors.DefaultHTTPError(context.Background(), http.StatusForbidden, "forbidden")}
			engine := newPublicEngine(auth, "user-1")

			for _, path := range []string{"/health", "/pool", "/sessions", "/sessions/sess_1"} {
				resp := performRequest(engine, http.MethodGet, base+path)
				So(resp.Code, ShouldEqual, http.StatusForbidden)
				// fake service 会返回哨兵值，因此这两条断言只有在 requireAdmin
				// 真的中止了请求时才成立
				So(resp.Body.String(), ShouldNotContainSubstring, sentinelWorkspacePath)
				So(resp.Body.String(), ShouldNotContainSubstring, "other-tenant-user")
			}
		})

		Convey("无认证上下文时返回 401，不进入授权判定", func() {
			auth := &fakeAuthService{}
			engine := newPublicEngine(auth, "")

			So(performRequest(engine, http.MethodGet, base+"/health").Code, ShouldEqual, http.StatusUnauthorized)
			So(auth.called, ShouldEqual, 0)
		})

		Convey("公开面同样只有只读路由", func() {
			engine := newPublicEngine(&fakeAuthService{}, "admin-1")

			So(performRequest(engine, http.MethodDelete, base+"/sessions/sess_1").Code, ShouldEqual, http.StatusNotFound)
			So(performRequest(engine, http.MethodPost, base+"/pool/prewarm").Code, ShouldEqual, http.StatusNotFound)
		})
	})
}
