package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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

func (f *fakeManagementService) ListSessions(ctx context.Context, req *logicssandbox.SandboxSessionListReq) (*logicssandbox.SandboxSessionListResp, error) {
	return &logicssandbox.SandboxSessionListResp{Items: []*logicssandbox.SandboxSessionSummary{}, Total: 0}, nil
}

func (f *fakeManagementService) GetSessionDetail(ctx context.Context, sessionID string) (*logicssandbox.SandboxSessionDetailResp, error) {
	return &logicssandbox.SandboxSessionDetailResp{SandboxSessionSummary: &logicssandbox.SandboxSessionSummary{ID: sessionID}}, nil
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
