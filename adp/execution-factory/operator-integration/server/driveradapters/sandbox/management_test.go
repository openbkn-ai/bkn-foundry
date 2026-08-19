package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	logicssandbox "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/sandbox"
	. "github.com/smartystreets/goconvey/convey"
)

type fakeManagementService struct{}

func (f *fakeManagementService) GetHealth(ctx context.Context) (*logicssandbox.SandboxHealthResp, error) {
	return &logicssandbox.SandboxHealthResp{Status: "healthy", ControlPlaneReachable: true}, nil
}

func (f *fakeManagementService) GetPool(ctx context.Context) (*logicssandbox.SandboxPoolResp, error) {
	return &logicssandbox.SandboxPoolResp{MaxSessions: 3, CurrentActiveSessions: 1}, nil
}

// sentinelWorkspacePath is the sentinel value for cross-tenant sensitive fields. It should not appear in the response when the gate is in effect;
// If the assertion is changed to always true (for example, let fake not fill in the field and rely on omitempty to make it disappear naturally), this set of use cases.
// It is impossible to detect whether requireAdmin actually blocks the request.
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

func performRequest(engine *gin.Engine, method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	engine.ServeHTTP(recorder, req)
	return recorder
}

// fakeAuthService allows or rejects based on the results given during construction, and is used to verify super-management access control on the public side.
type fakeAuthService struct {
	interfaces.IAuthorizationService
	adminErr error
	called   int
}

func (f *fakeAuthService) CheckAdminPermission(ctx context.Context, accessor *interfaces.AuthAccessor) error {
	f.called++
	return f.adminErr
}

// newPublicEngine constructs a public-side route with authentication context, simulating the output of middlewareIntrospectVerify.
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
			// The sentinel field does appear in the response when released - otherwise the following assertion of "no leakage" will always be true and the access control will not be detected.
			So(detail.Body.String(), ShouldContainSubstring, sentinelWorkspacePath)
		})

		Convey("非超管一律拒绝，且不泄露会话数据", func() {
			auth := &fakeAuthService{adminErr: errors.DefaultHTTPError(context.Background(), http.StatusForbidden, "forbidden")}
			engine := newPublicEngine(auth, "user-1")

			for _, path := range []string{"/health", "/pool", "/sessions", "/sessions/sess_1"} {
				resp := performRequest(engine, http.MethodGet, base+path)
				So(resp.Code, ShouldEqual, http.StatusForbidden)
				// fake service will return the sentinel value, so these two assertions are only required when requireAdmin.
				// This is true only when the request is actually aborted.
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

// TestInternalFaceNoLongerRegistersSandbox Keep #326 Step 3: Sandbox observation interface is only on the public face.
// Register. The token is not verified internally, and the identity is taken from the X-Account-ID header filled in by the caller. Once it is re-hung, it is equal to.
// Bypass the public access control. RegisterPrivate is no longer provided by ManagementHandler - this use case confirms.
// This method does not exist on the interface, and any reintroduction will be blocked by this assertion at compile time.
func TestInternalFaceNoLongerRegistersSandbox(t *testing.T) {
	Convey("沙箱处理器不再暴露内部面注册入口", t, func() {
		var handler ManagementHandler = NewManagementHandlerWithService(&fakeManagementService{})

		_, hasPrivateRegistration := any(handler).(interface {
			RegisterPrivate(engine *gin.RouterGroup)
		})

		So(hasPrivateRegistration, ShouldBeFalse)
	})
}
