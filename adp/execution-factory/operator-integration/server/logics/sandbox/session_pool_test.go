package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func TestExecuteCodeCreatesSessionWithBusinessContextEnv(t *testing.T) {
	Convey("ExecuteCode should pass business context env vars when creating a sandbox session", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := mocks.NewMockSandBoxControlPlane(ctrl)
		pool := &sessionPoolImpl{
			client:             mockClient,
			sessions:           map[string]*sessionItem{},
			maxSessions:        1,
			maxConcurrentTasks: 10,
			logger:             logger.DefaultLogger(),
			stopCh:             make(chan struct{}),
			templateID:         "python-basic",
			reqConfig:          config.SessionResourcesConfig{CPU: "1", Memory: "512Mi", Disk: "1Gi", Timeout: 3600},
		}

		expectedEnv := map[string]any{
			"source":          "function_debug",
			"task_id":         "task_e2e_001",
			"capability_id":   "cap_function_weather",
			"capability_name": "天气归一化函数",
			"user_id":         "user_001",
			"user_name":       "alice",
		}

		gomock.InOrder(
			mockClient.EXPECT().
				QuerySession(gomock.Any(), "sess_aoi_0").
				Return(false, nil, nil),
			mockClient.EXPECT().
				CreateSession(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.CreateSessionReq{})).
				DoAndReturn(func(ctx context.Context, req *interfaces.CreateSessionReq) (any, error) {
					So(req.ID, ShouldEqual, "sess_aoi_0")
					So(req.TemplateID, ShouldEqual, "python-basic")
					So(req.EnvVars, ShouldResemble, expectedEnv)
					return nil, nil
				}),
			mockClient.EXPECT().
				QuerySession(gomock.Any(), "sess_aoi_0").
				Return(true, &interfaces.SessionDetail{ID: "sess_aoi_0", Status: interfaces.SessionStatusRunning}, nil),
			mockClient.EXPECT().
				ExecuteCodeSync(gomock.Any(), "sess_aoi_0", gomock.AssignableToTypeOf(&interfaces.ExecuteCodeReq{})).
				Return(&interfaces.ExecuteCodeResp{SessionID: "sess_aoi_0", ReturnValue: map[string]any{"ok": true}}, nil),
		)

		resp, err := pool.ExecuteCode(context.Background(), &interfaces.ExecuteCodeReq{
			Code:     "def handler(event):\n    return event",
			Event:    map[string]any{"city": "beijing"},
			Language: "python",
			EnvVars:  expectedEnv,
		})

		So(err, ShouldBeNil)
		So(resp.SessionID, ShouldEqual, "sess_aoi_0")
	})
}

func TestGetDependenciesCachesFromSessionQuery(t *testing.T) {
	Convey("GetDependencies caches dependencies from QuerySession", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := mocks.NewMockSandBoxControlPlane(ctrl)
		pool := &sessionPoolImpl{
			client: mockClient,
			sessions: map[string]*sessionItem{
				"sess_aoi_0": {
					ID:           "sess_aoi_0",
					RunningTasks: 0,
					LastUsedAt:   time.Now(),
				},
			},
			maxConcurrentTasks: 10,
			logger:             logger.DefaultLogger(),
			stopCh:             make(chan struct{}),
		}

		firstDetail := &interfaces.SessionDetail{
			ID:     "sess_aoi_0",
			Status: interfaces.SessionStatusRunning,
			InstalledDependencies: []*interfaces.DependencyInfo{
				{Name: "requests", Version: "2.28.1"},
			},
		}
		secondDetail := &interfaces.SessionDetail{
			ID:     "sess_aoi_0",
			Status: interfaces.SessionStatusRunning,
		}

		gomock.InOrder(
			mockClient.EXPECT().
				QuerySession(gomock.Any(), "sess_aoi_0").
				Return(true, firstDetail, nil),
			mockClient.EXPECT().
				QuerySession(gomock.Any(), "sess_aoi_0").
				Return(true, secondDetail, nil),
		)

		resp, err := pool.GetDependencies(context.Background())
		So(err, ShouldBeNil)
		So(resp.SessionID, ShouldEqual, "sess_aoi_0")
		So(resp.Dependencies, ShouldResemble, []*interfaces.DependencyInfo{
			{Name: "requests", Version: "2.28.1"},
		})

		item, ok := pool.getSessionItem("sess_aoi_0")
		So(ok, ShouldBeTrue)
		So(item.Dependencies, ShouldResemble, firstDetail.InstalledDependencies)
	})
}
