// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func setupBuildTaskHandlerTest(t *testing.T) (*gin.Engine, *vmock.MockBuildTaskService, *vmock.MockResourceService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	bts := vmock.NewMockBuildTaskService(mockCtrl)
	rs := vmock.NewMockResourceService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, bts, nil, nil, nil, nil, nil)
	handler.RegisterPublic(engine)
	return engine, bts, rs
}

func Test_BuildTaskRestHandler_CreateBuildTask(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/build-tasks"

	t.Run("creates batch build task", func(t *testing.T) {
		engine, bts, _ := setupBuildTaskHandlerTest(t)
		bts.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.CreateBuildTaskRequest) (string, error) {
				assert.Equal(t, interfaces.BuildTaskModeBatch, req.Mode)
				assert.Equal(t, "res-1", req.ResourceID)
				return "task-1", nil
			})

		body := `{"resource_id":"res-1","mode":"batch"}`
		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"task-1"`)
		assert.NotContains(t, w.Body.String(), `"resource_id"`)
		assert.NotContains(t, w.Body.String(), `"status"`)
	})

	t.Run("creates batch task without index config fields", func(t *testing.T) {
		engine, bts, _ := setupBuildTaskHandlerTest(t)
		bts.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.CreateBuildTaskRequest) (string, error) {
				assert.Equal(t, interfaces.BuildTaskModeBatch, req.Mode)
				assert.Equal(t, "res-1", req.ResourceID)
				return "task-1", nil
			})

		body := `{"resource_id":"res-1","mode":"batch"}`
		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"task-1"`)
	})
}

func Test_BuildTaskRestHandler_GetBuildTask(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("gets build task by id", func(t *testing.T) {
		engine, bts, _ := setupBuildTaskHandlerTest(t)
		bts.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", ResourceID: "res-1", Status: interfaces.BuildTaskStatusRunning}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/build-tasks/task-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"task-1"`)
		assert.Contains(t, w.Body.String(), `"status":"running"`)
	})
}

// newBuildTaskListContext 造一个仅带 query 的 GET 测试上下文。
func newBuildTaskListContext(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/build-tasks?"+query, nil)
	return c
}

func TestParseBuildTaskListParams(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		query   string
		assert  func(t *testing.T, got interfaces.BuildTasksQueryParams)
		wantErr bool
	}{
		{
			name:  "defaults when no query",
			query: "",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, 0, got.Offset)
				assert.Equal(t, 20, got.Limit)
				assert.Equal(t, interfaces.BuildTaskSortCreateTime, got.Sort)
				assert.Equal(t, interfaces.DESC_DIRECTION, got.Direction)
				assert.Empty(t, got.Statuses)
			},
		},
		{
			name:  "removed active parameter does not override status",
			query: "active=true&status=completed",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, []string{interfaces.BuildTaskStatusCompleted}, got.Statuses)
			},
		},
		{
			name:  "multi-value status",
			query: "status=running&status=pending&status=running",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, []string{"running", "pending"}, got.Statuses)
			},
		},
		{
			name:  "sort and direction honored",
			query: "sort=create_time&direction=asc",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, interfaces.BuildTaskSortCreateTime, got.Sort)
				assert.Equal(t, interfaces.ASC_DIRECTION, got.Direction)
			},
		},
		{
			name:  "execute type honored",
			query: "execute_type=incremental",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, interfaces.BuildTaskExecuteTypeIncremental, got.ExecuteType)
			},
		},
		{name: "invalid sort returns error", query: "sort=bogus", wantErr: true},
		{name: "invalid direction returns error", query: "direction=sideways", wantErr: true},
		{name: "invalid status returns error", query: "status=running&status=nope", wantErr: true},
		{name: "comma-separated status returns error", query: "status=running,pending", wantErr: true},
		{name: "invalid mode returns error", query: "mode=nope", wantErr: true},
		{name: "invalid execute type returns error", query: "execute_type=nope", wantErr: true},
		{name: "negative offset returns error", query: "offset=-1", wantErr: true},
		{
			name:  "limit no-limit allowed",
			query: "limit=-1",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, -1, got.Limit)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBuildTaskListParams(ctx, newBuildTaskListContext(tt.query))

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.assert != nil {
				tt.assert(t, got)
			}
		})
	}
}

func Test_BuildTaskRestHandler_ListBuildTasks(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	setup := func(t *testing.T) (*gin.Engine, *vmock.MockBuildTaskService) {
		t.Helper()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		bts := vmock.NewMockBuildTaskService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, nil, bts, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)
		return engine, bts
	}

	const url = "/api/vega-backend/in/v1/build-tasks"

	tests := []struct {
		name     string
		query    string
		wantBody string
	}{
		{name: "invalid offset", query: "?offset=-1", wantBody: "VegaBackend.InvalidParameter.Offset"},
		{name: "invalid limit", query: "?limit=99999999", wantBody: "VegaBackend.InvalidParameter.Limit"},
		{name: "invalid sort", query: "?sort=unknown_field", wantBody: "VegaBackend.InvalidParameter.Sort"},
		{name: "removed default sort", query: "?sort=default", wantBody: "VegaBackend.InvalidParameter.Sort"},
		{name: "invalid direction", query: "?direction=foo", wantBody: "VegaBackend.InvalidParameter.Direction"},
		{name: "invalid status", query: "?status=foo", wantBody: "VegaBackend.BuildTask.InvalidStatus"},
		{name: "invalid mode", query: "?mode=foo", wantBody: "VegaBackend.BuildTask.InvalidParameter.Mode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, _ := setup(t)
			req := httptest.NewRequest(http.MethodGet, url+tt.query, nil)
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
			assert.Contains(t, w.Body.String(), tt.wantBody)
		})
	}

	t.Run("success with default pagination", func(t *testing.T) {
		engine, bts := setup(t)
		bts.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Equal(t, 0, params.Offset)
				assert.Equal(t, 20, params.Limit)
				assert.Equal(t, interfaces.BuildTaskSortCreateTime, params.Sort)
				assert.Equal(t, interfaces.DESC_DIRECTION, params.Direction)
				return []*interfaces.BuildTaskSummary{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("success with explicit query params", func(t *testing.T) {
		engine, bts := setup(t)
		bts.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
				assert.Equal(t, "res-1", params.ResourceID)
				assert.Equal(t, "cat-1", params.CatalogID)
				assert.Equal(t, []string{interfaces.BuildTaskStatusCompleted}, params.Statuses)
				assert.Equal(t, interfaces.BuildTaskModeBatch, params.Mode)
				assert.Equal(t, 5, params.Offset)
				assert.Equal(t, 10, params.Limit)
				assert.Equal(t, interfaces.BuildTaskSortCreateTime, params.Sort)
				assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
				return []*interfaces.BuildTaskSummary{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?resource_id=res-1&catalog_id=cat-1&status=completed&mode=batch&offset=5&limit=10&sort=create_time&direction=asc", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})
}

func Test_BuildTaskRestHandler_DeleteBuildTasks(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("deletes build tasks with ignore missing", func(t *testing.T) {
		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		bts := vmock.NewMockBuildTaskService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, nil, bts, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		bts.EXPECT().DeleteByIDs(gomock.Any(), []string{"t1", "t2"}, true).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/build-tasks/t1,t2,t1?ignore_missing=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}

func Test_BuildTaskRestHandler_StartBuildTask(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("starts build task with reset", func(t *testing.T) {
		engine, bts, _ := setupBuildTaskHandlerTest(t)
		bts.EXPECT().Start(gomock.Any(), "task-1", true).Return(nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/build-tasks/task-1/start", strings.NewReader(`{"reset":true}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusAccepted, w.Result().StatusCode)
	})

	t.Run("rejects invalid start body", func(t *testing.T) {
		engine, _, _ := setupBuildTaskHandlerTest(t)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/build-tasks/task-1/start", strings.NewReader(`{"reset":`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})
}

func Test_BuildTaskRestHandler_StopBuildTask(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("stops build task", func(t *testing.T) {
		engine, bts, _ := setupBuildTaskHandlerTest(t)
		bts.EXPECT().Stop(gomock.Any(), "task-1").Return(nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/build-tasks/task-1/stop", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusAccepted, w.Result().StatusCode)
	})
}
