package sandbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func TestAcquireSessionTriesNextSlotAfterCreateConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockSandBoxControlPlane(ctrl)
	pool := &sessionPoolImpl{
		client:             client,
		sessions:           map[string]*sessionItem{},
		maxSessions:        3,
		maxConcurrentTasks: 10,
		logger:             logger.DefaultLogger(),
		templateID:         "python-basic",
	}

	gomock.InOrder(
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(false, nil, nil),
		client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.CreateSessionReq) (any, error) {
				if req.ID != "sess_aoi_0" {
					t.Fatalf("first attempted slot = %s", req.ID)
				}
				return nil, errors.New("orphan sandbox pod already exists")
			}),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_1").Return(false, nil, nil),
		client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.CreateSessionReq) (any, error) {
				if req.ID != "sess_aoi_1" {
					t.Fatalf("retry attempted slot = %s", req.ID)
				}
				return nil, nil
			}),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_1").
			Return(true, &interfaces.SessionDetail{ID: "sess_aoi_1", Status: interfaces.SessionStatusRunning}, nil),
	)

	id, err := pool.AcquireSession(context.Background())
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	if id != "sess_aoi_1" {
		t.Fatalf("acquired slot = %s, want sess_aoi_1", id)
	}
}

func TestAcquireSessionTriesEveryConfiguredSlotAfterCreateConflicts(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockSandBoxControlPlane(ctrl)
	pool := &sessionPoolImpl{
		client:             client,
		sessions:           map[string]*sessionItem{},
		maxSessions:        6,
		maxConcurrentTasks: 10,
		logger:             logger.DefaultLogger(),
		templateID:         "python-basic",
	}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("sess_aoi_%d", i)
		client.EXPECT().QuerySession(gomock.Any(), id).Return(false, nil, nil)
		if i < 5 {
			client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *interfaces.CreateSessionReq) (any, error) {
					if req.ID != id {
						t.Fatalf("attempted slot = %s, want %s", req.ID, id)
					}
					return nil, errors.New("orphan sandbox pod already exists")
				})
			continue
		}
		client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.CreateSessionReq) (any, error) {
				if req.ID != id {
					t.Fatalf("attempted slot = %s, want %s", req.ID, id)
				}
				return nil, nil
			})
		client.EXPECT().QuerySession(gomock.Any(), id).
			Return(true, &interfaces.SessionDetail{ID: id, Status: interfaces.SessionStatusRunning}, nil)
	}

	id, err := pool.AcquireSession(context.Background())
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	if id != "sess_aoi_5" {
		t.Fatalf("acquired slot = %s, want sess_aoi_5", id)
	}
}

func TestAcquireSessionStopsRetryWhenContextIsCanceled(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockSandBoxControlPlane(ctrl)
	pool := &sessionPoolImpl{
		client:             client,
		sessions:           map[string]*sessionItem{},
		maxSessions:        3,
		maxConcurrentTasks: 10,
		logger:             logger.DefaultLogger(),
		templateID:         "python-basic",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(false, nil, nil)
	client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *interfaces.CreateSessionReq) (any, error) {
			cancel()
			return nil, errors.New("orphan sandbox pod already exists")
		})
	start := time.Now()
	_, err := pool.AcquireSession(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireSession error = %v, want context canceled", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("AcquireSession did not stop promptly after cancellation")
	}
}

func TestAcquireSessionRetriesSameSlotAfterSuccessfulCreateStillStarting(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockSandBoxControlPlane(ctrl)
	pool := &sessionPoolImpl{
		client:             client,
		sessions:           map[string]*sessionItem{},
		maxSessions:        3,
		maxConcurrentTasks: 10,
		logger:             logger.DefaultLogger(),
		templateID:         "python-basic",
	}
	gomock.InOrder(
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(false, nil, nil),
		client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil, nil),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(false, nil, nil),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(false, nil, nil),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(true,
			&interfaces.SessionDetail{ID: "sess_aoi_0", Status: interfaces.SessionStatusCreating}, nil),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(true,
			&interfaces.SessionDetail{ID: "sess_aoi_0", Status: interfaces.SessionStatusRunning}, nil),
	)
	id, err := pool.AcquireSession(context.Background())
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	if id != "sess_aoi_0" {
		t.Fatalf("acquired slot = %s, want sess_aoi_0", id)
	}
}

func TestAcquireSessionRecreatesDeletedTerminalSession(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockSandBoxControlPlane(ctrl)
	pool := &sessionPoolImpl{
		client:             client,
		sessions:           map[string]*sessionItem{},
		maxSessions:        3,
		maxConcurrentTasks: 10,
		logger:             logger.DefaultLogger(),
		templateID:         "python-basic",
	}
	gomock.InOrder(
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(false, nil, nil),
		client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil, nil),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(true,
			&interfaces.SessionDetail{ID: "sess_aoi_0", Status: interfaces.SessionStatusFailed}, nil),
		client.EXPECT().DeleteSession(gomock.Any(), "sess_aoi_0").Return(nil),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(false, nil, nil),
		client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil, nil),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(true,
			&interfaces.SessionDetail{ID: "sess_aoi_0", Status: interfaces.SessionStatusRunning}, nil),
	)
	id, err := pool.AcquireSession(context.Background())
	if err != nil || id != "sess_aoi_0" {
		t.Fatalf("AcquireSession = %q, %v; want sess_aoi_0", id, err)
	}
}

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

func TestExecuteCodeRecordsExecutionContextWhenReusingSession(t *testing.T) {
	Convey("ExecuteCode should record request context even when reusing a prewarmed session", t, func() {
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
			maxSessions:        1,
			maxConcurrentTasks: 10,
			logger:             logger.DefaultLogger(),
			stopCh:             make(chan struct{}),
		}

		execEnv := map[string]any{
			"source":          "function_debug",
			"task_id":         "task_reuse_001",
			"capability_id":   "cap_reuse_weather",
			"capability_name": "weather_reuse",
			"user_id":         "user_reuse",
			"user_name":       "reuse-user",
		}

		gomock.InOrder(
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
			EnvVars:  execEnv,
		})

		So(err, ShouldBeNil)
		So(resp.SessionID, ShouldEqual, "sess_aoi_0")

		snapshot := pool.Snapshot()
		So(snapshot.Sessions, ShouldHaveLength, 1)
		So(snapshot.Sessions[0].ExecutionContext, ShouldResemble, execEnv)
	})
}
