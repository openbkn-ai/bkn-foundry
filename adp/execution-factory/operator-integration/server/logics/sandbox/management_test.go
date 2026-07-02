package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	. "github.com/smartystreets/goconvey/convey"
)

type fakeSandboxControlPlane struct {
	listReq      *interfaces.ListSessionsReq
	listResp     *interfaces.ListSessionsResp
	detail       *interfaces.SessionDetail
	queryExists  bool
	querySession string
	queryErr     error
}

func (f *fakeSandboxControlPlane) GetTemplateDetail(ctx context.Context, tempID string) (any, error) {
	return nil, nil
}

func (f *fakeSandboxControlPlane) CreateSession(ctx context.Context, req *interfaces.CreateSessionReq) (any, error) {
	return nil, nil
}

func (f *fakeSandboxControlPlane) QuerySession(ctx context.Context, sessionID string) (bool, *interfaces.SessionDetail, error) {
	f.querySession = sessionID
	return f.queryExists, f.detail, f.queryErr
}

func (f *fakeSandboxControlPlane) DeleteSession(ctx context.Context, sessionID string) error {
	return nil
}

func (f *fakeSandboxControlPlane) ListSessions(ctx context.Context, req *interfaces.ListSessionsReq) (*interfaces.ListSessionsResp, error) {
	f.listReq = req
	return f.listResp, nil
}

func (f *fakeSandboxControlPlane) ExecuteCodeSync(ctx context.Context, sessionID string, req *interfaces.ExecuteCodeReq) (*interfaces.ExecuteCodeResp, error) {
	return nil, nil
}

func (f *fakeSandboxControlPlane) InstallPythonDependencies(ctx context.Context, sessionID string, req *interfaces.InstallDependenciesReq) (*interfaces.SessionDetail, error) {
	return nil, nil
}

func (f *fakeSandboxControlPlane) UploadSkillArchive(ctx context.Context, sessionID string, req *interfaces.UploadSkillArchiveReq) (*interfaces.UploadSkillArchiveResp, error) {
	return nil, nil
}

func (f *fakeSandboxControlPlane) ExecuteShell(ctx context.Context, sessionID string, req *interfaces.ExecuteShellReq) (*interfaces.ExecuteShellResp, error) {
	return nil, nil
}

func TestSandboxManagementService(t *testing.T) {
	Convey("Sandbox management service should expose read-only pool and session observability", t, func() {
		now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
		client := &fakeSandboxControlPlane{
			listResp: &interfaces.ListSessionsResp{
				Sessions: []*interfaces.SessionDetail{
					{
						ID:          "sess_aoi_0",
						Status:      interfaces.SessionStatusRunning,
						TemplateID:  "python-basic",
						RuntimeType: "python",
						EnvVars: map[string]any{
							"source":          "function_debug",
							"task_id":         "task_debug_001",
							"capability_id":   "cap_weather",
							"capability_name": "天气查询函数",
							"user_id":         "user_001",
							"user_name":       "alice",
						},
						DependencyInstallStatus:    "ready",
						DependencyInstallError:     "",
						LastActivityAt:             "2026-07-01T09:59:00Z",
						InstalledDependencies:      []*interfaces.DependencyInfo{{Name: "numpy", Version: "1.26.4"}},
						PythonPackageIndexURL:      "https://pypi.org/simple/",
						DependencyInstallStartedAt: "2026-07-01T09:58:00Z",
					},
					{
						ID:                      "sess_aoi_1",
						Status:                  interfaces.SessionStatusFailed,
						TemplateID:              "python-basic",
						RuntimeType:             "python",
						DependencyInstallStatus: "failed",
						DependencyInstallError:  "numpy version conflict",
					},
				},
				Total:   2,
				Limit:   20,
				Offset:  0,
				HasMore: false,
			},
			detail: &interfaces.SessionDetail{
				ID:          "sess_aoi_1",
				Status:      interfaces.SessionStatusFailed,
				TemplateID:  "python-basic",
				RuntimeType: "python",
				EnvVars: map[string]any{
					"execution_source": "skill_execution",
					"task_id":          "task_skill_404",
					"capability_id":    "cap_skill_summary",
					"capability_name":  "总结 Skill",
					"user_id":          "user_002",
					"user_name":        "bob",
				},
				ResourceLimit:           map[string]any{"cpu": "1", "memory": "512Mi"},
				WorkspacePath:           "/workspace/sess_aoi_1",
				DependencyInstallStatus: "failed",
				DependencyInstallError:  "numpy version conflict",
				LastActivityAt:          "2026-07-01T09:55:00Z",
			},
			queryExists: true,
		}
		pool := &sessionPoolImpl{
			client:             client,
			sessions:           map[string]*sessionItem{"sess_aoi_0": {ID: "sess_aoi_0", RunningTasks: 2, LastUsedAt: now}},
			maxSessions:        3,
			activeSessions:     1,
			maxConcurrentTasks: 100,
			templateID:         "python-basic",
			reqConfig:          config.SessionResourcesConfig{CPU: "1", Memory: "512Mi", Disk: "1Gi", Timeout: 3600},
		}
		service := NewSandboxManagementService(client, pool)

		health, err := service.GetHealth(context.Background())
		So(err, ShouldBeNil)
		So(health.Status, ShouldEqual, "degraded")
		So(health.ControlPlaneReachable, ShouldBeTrue)
		So(health.FailedSessions, ShouldEqual, 1)

		poolResp, err := service.GetPool(context.Background())
		So(err, ShouldBeNil)
		So(poolResp.MaxSessions, ShouldEqual, 3)
		So(poolResp.ActiveSessions, ShouldEqual, 1)
		So(poolResp.CurrentActiveSessions, ShouldEqual, 1)
		So(poolResp.CurrentRunningTasks, ShouldEqual, 2)
		So(poolResp.SessionResources.Memory, ShouldEqual, "512Mi")

		sessions, err := service.ListSessions(context.Background(), &SandboxSessionListReq{
			Limit:  20,
			Offset: 0,
			Status: interfaces.SessionStatusFailed,
		})
		So(err, ShouldBeNil)
		So(client.listReq.Status, ShouldEqual, interfaces.SessionStatusFailed)
		So(sessions.Total, ShouldEqual, 2)
		So(sessions.Items[0].Source, ShouldEqual, "function_debug")
		So(sessions.Items[0].TaskID, ShouldEqual, "task_debug_001")
		So(sessions.Items[0].CapabilityID, ShouldEqual, "cap_weather")
		So(sessions.Items[0].CapabilityName, ShouldEqual, "天气查询函数")
		So(sessions.Items[0].UserID, ShouldEqual, "user_001")
		So(sessions.Items[0].UserName, ShouldEqual, "alice")
		So(sessions.Items[1].Source, ShouldEqual, "unknown")
		So(sessions.Items[1].RecentErrorSummary, ShouldEqual, "numpy version conflict")
		So(sessions.Items[1].DependencyInstallStatus, ShouldEqual, "failed")

		detail, err := service.GetSessionDetail(context.Background(), "sess_aoi_1")
		So(err, ShouldBeNil)
		So(client.querySession, ShouldEqual, "sess_aoi_1")
		So(detail.ID, ShouldEqual, "sess_aoi_1")
		So(detail.Source, ShouldEqual, "skill_execution")
		So(detail.TaskID, ShouldEqual, "task_skill_404")
		So(detail.CapabilityID, ShouldEqual, "cap_skill_summary")
		So(detail.CapabilityName, ShouldEqual, "总结 Skill")
		So(detail.UserID, ShouldEqual, "user_002")
		So(detail.UserName, ShouldEqual, "bob")
		So(detail.RecentErrorSummary, ShouldEqual, "numpy version conflict")
		So(detail.ResourceLimit["memory"], ShouldEqual, "512Mi")
	})
}

func TestSandboxManagementServiceUsesPoolExecutionContextOverSessionEnv(t *testing.T) {
	Convey("Sandbox management service should prefer latest execution context over reusable session env", t, func() {
		client := &fakeSandboxControlPlane{
			listResp: &interfaces.ListSessionsResp{
				Sessions: []*interfaces.SessionDetail{
					{
						ID:          "sess_aoi_0",
						Status:      interfaces.SessionStatusRunning,
						TemplateID:  "python-basic",
						RuntimeType: "python",
						EnvVars: map[string]any{
							"source":          "function_debug",
							"task_id":         "task_old",
							"capability_id":   "cap_old",
							"capability_name": "old capability",
							"user_id":         "user_old",
							"user_name":       "old-user",
						},
					},
				},
				Total: 1,
				Limit: 20,
			},
			detail: &interfaces.SessionDetail{
				ID:          "sess_aoi_0",
				Status:      interfaces.SessionStatusRunning,
				TemplateID:  "python-basic",
				RuntimeType: "python",
				EnvVars: map[string]any{
					"source":        "function_debug",
					"task_id":       "task_old",
					"capability_id": "cap_old",
					"user_id":       "user_old",
					"user_name":     "old-user",
				},
			},
			queryExists: true,
		}
		pool := &sessionPoolImpl{
			client: client,
			sessions: map[string]*sessionItem{
				"sess_aoi_0": {
					ID:           "sess_aoi_0",
					RunningTasks: 0,
					LastUsedAt:   time.Now(),
					ExecutionContext: map[string]any{
						"source":          "function_debug",
						"task_id":         "task_new",
						"capability_id":   "cap_new",
						"capability_name": "new capability",
						"user_id":         "user_new",
						"user_name":       "new-user",
					},
				},
			},
			maxSessions:        1,
			activeSessions:     1,
			maxConcurrentTasks: 10,
		}
		service := NewSandboxManagementService(client, pool)

		sessions, err := service.ListSessions(context.Background(), &SandboxSessionListReq{Limit: 20})
		So(err, ShouldBeNil)
		So(sessions.Items, ShouldHaveLength, 1)
		So(sessions.Items[0].TaskID, ShouldEqual, "task_new")
		So(sessions.Items[0].CapabilityID, ShouldEqual, "cap_new")
		So(sessions.Items[0].CapabilityName, ShouldEqual, "new capability")
		So(sessions.Items[0].UserID, ShouldEqual, "user_new")
		So(sessions.Items[0].UserName, ShouldEqual, "new-user")

		detail, err := service.GetSessionDetail(context.Background(), "sess_aoi_0")
		So(err, ShouldBeNil)
		So(detail.TaskID, ShouldEqual, "task_new")
		So(detail.CapabilityID, ShouldEqual, "cap_new")
		So(detail.UserID, ShouldEqual, "user_new")
		So(detail.UserName, ShouldEqual, "new-user")
	})
}
