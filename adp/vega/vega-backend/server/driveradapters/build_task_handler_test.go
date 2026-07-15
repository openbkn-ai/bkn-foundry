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
	handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, bts, nil, nil, nil, nil, nil, nil)
	handler.RegisterPublic(engine)
	return engine, bts, rs
}

func Test_BuildTaskRestHandler_CreateBuildTask(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/build-tasks"

	t.Run("creates batch build task", func(t *testing.T) {
		engine, bts, _ := setupBuildTaskHandlerTest(t)
		bts.EXPECT().CreateBuildTask(gomock.Any(), gomock.Any()).
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
		assert.Contains(t, w.Body.String(), `"status":"init"`)
	})

	t.Run("ignores legacy index config fields", func(t *testing.T) {
		engine, bts, _ := setupBuildTaskHandlerTest(t)
		bts.EXPECT().CreateBuildTask(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.CreateBuildTaskRequest) (string, error) {
				assert.Equal(t, interfaces.BuildTaskModeBatch, req.Mode)
				assert.Equal(t, "res-1", req.ResourceID)
				return "task-1", nil
			})

		body := `{"resource_id":"res-1","mode":"batch","build_key_fields":"id","embedding_fields":"title","embedding_model":"embedding","model_dimensions":1024,"fulltext_fields":"title","fulltext_analyzer":"ik_max_word"}`
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
		bts.EXPECT().GetBuildTaskByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", ResourceID: "res-1", Status: interfaces.BuildTaskStatusRunning}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/build-tasks/task-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"task-1"`)
		assert.Contains(t, w.Body.String(), `"status":"running"`)
	})
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
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, nil, bts, nil, nil, nil, nil, nil, nil)
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
		{name: "invalid order_by", query: "?order_by=unknown_field", wantBody: "VegaBackend.InvalidParameter.Sort"},
		{name: "invalid order", query: "?order=foo", wantBody: "VegaBackend.InvalidParameter.Direction"},
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
		bts.EXPECT().ListBuildTasks(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
				assert.Equal(t, 0, params.Offset)
				assert.Equal(t, 20, params.Limit)
				assert.Equal(t, interfaces.BuildTaskOrderByDefault, params.OrderBy)
				assert.Equal(t, interfaces.DESC_DIRECTION, params.Order)
				return []*interfaces.BuildTask{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("success with explicit query params", func(t *testing.T) {
		engine, bts := setup(t)
		bts.EXPECT().ListBuildTasks(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
				assert.Equal(t, "res-1", params.ResourceID)
				assert.Equal(t, "cat-1", params.CatalogID)
				assert.Equal(t, []string{interfaces.BuildTaskStatusCompleted}, params.Statuses)
				assert.Equal(t, interfaces.BuildTaskModeBatch, params.Mode)
				assert.Equal(t, 5, params.Offset)
				assert.Equal(t, 10, params.Limit)
				assert.Equal(t, interfaces.BuildTaskOrderByCreatedAt, params.OrderBy)
				assert.Equal(t, interfaces.ASC_DIRECTION, params.Order)
				return []*interfaces.BuildTask{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?resource_id=res-1&catalog_id=cat-1&status=completed&mode=batch&offset=5&limit=10&order_by=created_at&order=asc", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})
}

func Test_BuildTaskRestHandler_DeleteBuildTasks(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("deletes build tasks with flags", func(t *testing.T) {
		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		bts := vmock.NewMockBuildTaskService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, nil, bts, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		bts.EXPECT().DeleteBuildTasks(gomock.Any(), []string{"t1", "t2"}, true, true).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/build-tasks/t1,t2?ignore_missing=true&delete_active_index=true", nil)
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
		bts.EXPECT().StartBuildTask(gomock.Any(), "task-1", true).Return(nil)

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
		bts.EXPECT().StopBuildTask(gomock.Any(), "task-1").Return(nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/build-tasks/task-1/stop", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusAccepted, w.Result().StatusCode)
	})
}
