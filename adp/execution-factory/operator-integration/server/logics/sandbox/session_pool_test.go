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

func newSpreadPool(t *testing.T, threshold int, running map[string]int) (*sessionPoolImpl, *mocks.MockSandBoxControlPlane) {
	t.Helper()
	ctrl := gomock.NewController(t)
	client := mocks.NewMockSandBoxControlPlane(ctrl)
	pool := &sessionPoolImpl{
		client:             client,
		sessions:           map[string]*sessionItem{},
		maxSessions:        3,
		maxConcurrentTasks: 100,
		spreadThreshold:    threshold,
		logger:             logger.DefaultLogger(),
		templateID:         "python-basic",
	}
	for id, n := range running {
		pool.sessions[id] = &sessionItem{ID: id, RunningTasks: n, LastUsedAt: time.Now()}
		client.EXPECT().QuerySession(gomock.Any(), id).
			Return(true, &interfaces.SessionDetail{ID: id, Status: interfaces.SessionStatusRunning}, nil).AnyTimes()
	}
	return pool, client
}

func TestFindBestSessionStacksBelowSpreadThreshold(t *testing.T) {
	pool, _ := newSpreadPool(t, 3, map[string]int{"sess_aoi_0": 1, "sess_aoi_1": 2})
	got := pool.findBestSession(true)
	if got == nil || got.ID != "sess_aoi_1" {
		t.Fatalf("best = %v, want the busiest session still under the threshold (sess_aoi_1)", got)
	}
}

func TestFindBestSessionAsksForNewSessionOnceSaturated(t *testing.T) {
	pool, _ := newSpreadPool(t, 2, map[string]int{"sess_aoi_0": 2})
	if got := pool.findBestSession(true); got != nil {
		t.Fatalf("best = %s, want nil so the pool grows", got.ID)
	}
}

func TestFindBestSessionSpreadsToLeastLoadedWhenPoolCannotGrow(t *testing.T) {
	pool, _ := newSpreadPool(t, 2, map[string]int{"sess_aoi_0": 5, "sess_aoi_1": 3, "sess_aoi_2": 4})
	got := pool.findBestSession(false)
	if got == nil || got.ID != "sess_aoi_1" {
		t.Fatalf("best = %v, want the least-loaded session (sess_aoi_1)", got)
	}
}

func TestFindBestSessionKeepsLegacyStackingWithoutThreshold(t *testing.T) {
	pool, _ := newSpreadPool(t, 0, map[string]int{"sess_aoi_0": 7, "sess_aoi_1": 2})
	got := pool.findBestSession(true)
	if got == nil || got.ID != "sess_aoi_0" {
		t.Fatalf("best = %v, want the busiest session (sess_aoi_0)", got)
	}
}

func TestAcquireSessionRunsNowAndGrowsInBackgroundWhenSaturated(t *testing.T) {
	pool, client := newSpreadPool(t, 1, map[string]int{"sess_aoi_0": 1})
	created := make(chan struct{})
	gomock.InOrder(
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_1").Return(false, nil, nil),
		client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
			Return(nil, nil),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_1").
			DoAndReturn(func(context.Context, string) (bool, *interfaces.SessionDetail, error) {
				defer close(created)
				return true, &interfaces.SessionDetail{ID: "sess_aoi_1", Status: interfaces.SessionStatusRunning}, nil
			}),
	)
	id, err := pool.AcquireSession(context.Background())
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	if id != "sess_aoi_0" {
		t.Fatalf("acquired %s, want the existing session so the call does not wait for a pod", id)
	}
	select {
	case <-created:
	case <-time.After(5 * time.Second):
		t.Fatal("no background session was created")
	}
}

func TestFindBestSessionReservesSlotsForConcurrentCallers(t *testing.T) {
	pool, _ := newSpreadPool(t, 1, map[string]int{"sess_aoi_0": 0, "sess_aoi_1": 0, "sess_aoi_2": 0})
	const callers = 3
	got := make(chan string, callers)
	for i := 0; i < callers; i++ {
		go func() {
			s := pool.findBestSession(true)
			if s == nil {
				got <- ""
				return
			}
			got <- s.ID
		}()
	}
	seen := map[string]bool{}
	for i := 0; i < callers; i++ {
		id := <-got
		if id == "" || seen[id] {
			t.Fatalf("concurrent callers did not get distinct sessions (got %q, seen %v)", id, seen)
		}
		seen[id] = true
	}
}

func TestExecuteCodeRunsInSharedDirectoryWhenCallerNamesNone(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockSandBoxControlPlane(ctrl)
	pool := newCredentialTestPool(client)
	gomock.InOrder(expectRunningSession(client)...)
	var gotDir string
	client.EXPECT().ExecuteCodeSync(gomock.Any(), "sess_aoi_0", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, req *interfaces.ExecuteCodeReq) (*interfaces.ExecuteCodeResp, error) {
			gotDir = req.WorkingDirectory
			return &interfaces.ExecuteCodeResp{}, nil
		})
	if _, err := pool.ExecuteCode(context.Background(), &interfaces.ExecuteCodeReq{Code: "x", Language: "python"}); err != nil {
		t.Fatalf("ExecuteCode: %v", err)
	}
	if gotDir != DefaultWorkingDirectory {
		t.Fatalf("working directory = %q, want %q so the executor does not scan the workspace root", gotDir, DefaultWorkingDirectory)
	}
}
