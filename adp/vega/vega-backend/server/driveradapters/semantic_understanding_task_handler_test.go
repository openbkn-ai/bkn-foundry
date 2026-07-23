// Copyright openbkn.ai
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
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

const semanticUnderstandingTaskURL = "/api/vega-backend/in/v1/semantic-understanding-tasks"
const semanticUnderstandingTaskExternalURL = "/api/vega-backend/v1/semantic-understanding-tasks"

func setupSemanticUnderstandingTaskHandlerTest(t *testing.T) (*gin.Engine, *vmock.MockSemanticUnderstandingTaskService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	suts := vmock.NewMockSemanticUnderstandingTaskService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.suts = suts
	handler.RegisterPublic(engine)
	return engine, suts
}

func setupSemanticUnderstandingTaskExternalHandlerTest(t *testing.T) (*gin.Engine, *vmock.MockAuthService, *vmock.MockSemanticUnderstandingTaskService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	as := vmock.NewMockAuthService(mockCtrl)
	suts := vmock.NewMockSemanticUnderstandingTaskService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, as, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.suts = suts
	handler.RegisterPublic(engine)
	return engine, as, suts
}

func Test_SemanticUnderstandingTaskRestHandler_CreateTask(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("creates resource task", func(t *testing.T) {
		engine, suts := setupSemanticUnderstandingTaskHandlerTest(t)
		suts.EXPECT().CreateResourceTask(gomock.Any(), "res-1", gomock.Any()).
			DoAndReturn(func(_ context.Context, resourceID string, req *interfaces.CreateSemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, error) {
				assert.Equal(t, "res-1", resourceID)
				assert.Equal(t, interfaces.SemanticUnderstandingApplyModeDryRun, req.ApplyMode)
				require.NotNil(t, req.ConfidenceThreshold)
				assert.Equal(t, 0.9, *req.ConfidenceThreshold)
				return &interfaces.SemanticUnderstandingTask{
					ID:         "task-1",
					Scope:      interfaces.SemanticUnderstandingTaskScopeResource,
					CatalogID:  "catalog-1",
					ResourceID: "res-1",
					Status:     interfaces.SemanticUnderstandingTaskStatusPending,
					InputHash:  "hash-1",
					ResultJSON: `{"result":"ok"}`,
				}, nil
			})

		body := `{"scope":"resource","resource_id":"res-1","apply_mode":"dry_run","confidence_threshold":0.9}`
		req := httptest.NewRequest(http.MethodPost, semanticUnderstandingTaskURL, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"task-1"`)
		assert.Contains(t, w.Body.String(), `"input_hash":"hash-1"`)
		assert.Contains(t, w.Body.String(), `"result_json":"{\"result\":\"ok\"}"`)
	})

	t.Run("creates catalog task", func(t *testing.T) {
		engine, suts := setupSemanticUnderstandingTaskHandlerTest(t)
		suts.EXPECT().CreateCatalogTask(gomock.Any(), "catalog-1", gomock.Any()).
			DoAndReturn(func(_ context.Context, catalogID string, req *interfaces.CreateSemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, error) {
				assert.Equal(t, "catalog-1", catalogID)
				assert.Equal(t, "", req.ApplyMode)
				return &interfaces.SemanticUnderstandingTask{
					ID:        "task-1",
					Scope:     interfaces.SemanticUnderstandingTaskScopeCatalog,
					CatalogID: "catalog-1",
					Status:    interfaces.SemanticUnderstandingTaskStatusPending,
				}, nil
			})

		req := httptest.NewRequest(http.MethodPost, semanticUnderstandingTaskURL, strings.NewReader(`{"scope":"catalog","catalog_id":"catalog-1"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"scope":"catalog"`)
		assert.Contains(t, w.Body.String(), `"catalog_id":"catalog-1"`)
	})

	t.Run("creates resource task by external api", func(t *testing.T) {
		engine, as, suts := setupSemanticUnderstandingTaskExternalHandlerTest(t)
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).
			Return(hydra.Visitor{ID: "user-1", Type: hydra.VisitorType_User}, nil)
		suts.EXPECT().CreateResourceTask(gomock.Any(), "res-1", gomock.Any()).
			Return(&interfaces.SemanticUnderstandingTask{
				ID:         "task-1",
				Scope:      interfaces.SemanticUnderstandingTaskScopeResource,
				CatalogID:  "catalog-1",
				ResourceID: "res-1",
				Status:     interfaces.SemanticUnderstandingTaskStatusPending,
			}, nil)

		req := httptest.NewRequest(http.MethodPost, semanticUnderstandingTaskExternalURL, strings.NewReader(`{"scope":"resource","resource_id":"res-1"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"task-1"`)
	})
}

func Test_SemanticUnderstandingTaskRestHandler_ListTasks(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	tests := []struct {
		name     string
		query    string
		wantBody string
	}{
		{name: "invalid offset", query: "?offset=-1", wantBody: "VegaBackend.InvalidParameter.Offset"},
		{name: "invalid limit", query: "?limit=99999999", wantBody: "VegaBackend.InvalidParameter.Limit"},
		{name: "invalid sort field", query: "?sort=unknown_field", wantBody: "VegaBackend.InvalidParameter.Sort"},
		{name: "invalid direction", query: "?direction=foo", wantBody: "VegaBackend.InvalidParameter.Direction"},
		{name: "invalid scope", query: "?scope=unknown", wantBody: "scope must be resource or catalog"},
		{name: "invalid status", query: "?status=unknown", wantBody: "invalid status"},
		{name: "invalid apply mode", query: "?apply_mode=unknown", wantBody: "invalid apply_mode"},
		{name: "invalid applied", query: "?applied=unknown", wantBody: "applied must be true or false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, _ := setupSemanticUnderstandingTaskHandlerTest(t)
			req := httptest.NewRequest(http.MethodGet, semanticUnderstandingTaskURL+tt.query, nil)
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
			assert.Contains(t, w.Body.String(), tt.wantBody)
		})
	}

	t.Run("success with explicit query params", func(t *testing.T) {
		engine, suts := setupSemanticUnderstandingTaskHandlerTest(t)
		suts.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTask, int64, error) {
				assert.Equal(t, interfaces.SemanticUnderstandingTaskScopeResource, params.Scope)
				assert.Equal(t, "catalog-1", params.CatalogID)
				assert.Equal(t, "res-1", params.ResourceID)
				assert.Equal(t, []string{
					interfaces.SemanticUnderstandingTaskStatusPending,
					interfaces.SemanticUnderstandingTaskStatusRunning,
				}, params.Statuses)
				assert.Equal(t, interfaces.SemanticUnderstandingApplyModeFillEmpty, params.ApplyMode)
				require.NotNil(t, params.Applied)
				assert.True(t, *params.Applied)
				assert.Equal(t, 5, params.Offset)
				assert.Equal(t, 10, params.Limit)
				assert.Equal(t, "create_time", params.Sort)
				assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
				return []*interfaces.SemanticUnderstandingTask{
					{
						ID:         "task-1",
						Scope:      interfaces.SemanticUnderstandingTaskScopeResource,
						CatalogID:  "catalog-1",
						ResourceID: "res-1",
						Status:     interfaces.SemanticUnderstandingTaskStatusPending,
						Input:      `{"private":"snapshot"}`,
					},
				}, int64(1), nil
			})

		req := httptest.NewRequest(http.MethodGet, semanticUnderstandingTaskURL+"?scope=resource&catalog_id=catalog-1&resource_id=res-1&status=pending,running&apply_mode=fill_empty&applied=true&offset=5&limit=10&sort=create_time&direction=asc", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"total_count":1`)
		assert.Contains(t, w.Body.String(), `"id":"task-1"`)
		assert.Contains(t, w.Body.String(), "private")
	})

	t.Run("success by external api with active shortcut", func(t *testing.T) {
		engine, as, suts := setupSemanticUnderstandingTaskExternalHandlerTest(t)
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).
			Return(hydra.Visitor{ID: "user-1", Type: hydra.VisitorType_User}, nil)
		suts.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTask, int64, error) {
				assert.Equal(t, interfaces.SemanticUnderstandingTaskScopeCatalog, params.Scope)
				assert.Equal(t, []string(interfaces.SemanticUnderstandingTaskActiveStatuses), params.Statuses)
				return []*interfaces.SemanticUnderstandingTask{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, semanticUnderstandingTaskExternalURL+"?scope=catalog&active=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"entries":[]`)
		assert.Contains(t, w.Body.String(), `"total_count":0`)
	})
}

func Test_SemanticUnderstandingTaskRestHandler_GetTask(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("gets task by id", func(t *testing.T) {
		engine, suts := setupSemanticUnderstandingTaskHandlerTest(t)
		suts.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.SemanticUnderstandingTask{
			ID:         "task-1",
			Scope:      interfaces.SemanticUnderstandingTaskScopeResource,
			CatalogID:  "catalog-1",
			ResourceID: "res-1",
			Status:     interfaces.SemanticUnderstandingTaskStatusSucceeded,
			Confidence: 0.82,
			Input:      `{"private":"snapshot"}`,
			ResultJSON: `{"private":"result"}`,
		}, nil)

		req := httptest.NewRequest(http.MethodGet, semanticUnderstandingTaskURL+"/task-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"task-1"`)
		assert.Contains(t, w.Body.String(), `"confidence":0.82`)
		assert.Contains(t, w.Body.String(), "result_json")
	})

	t.Run("returns not found for nil task", func(t *testing.T) {
		engine, suts := setupSemanticUnderstandingTaskHandlerTest(t)
		suts.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		req := httptest.NewRequest(http.MethodGet, semanticUnderstandingTaskURL+"/missing", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.SemanticUnderstandingTask.NotFound")
	})

	t.Run("gets task by external api", func(t *testing.T) {
		engine, as, suts := setupSemanticUnderstandingTaskExternalHandlerTest(t)
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).
			Return(hydra.Visitor{ID: "user-1", Type: hydra.VisitorType_User}, nil)
		suts.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.SemanticUnderstandingTask{
			ID:        "task-1",
			Scope:     interfaces.SemanticUnderstandingTaskScopeCatalog,
			CatalogID: "catalog-1",
			Status:    interfaces.SemanticUnderstandingTaskStatusRunning,
			Input:     `{"private":"snapshot"}`,
		}, nil)

		req := httptest.NewRequest(http.MethodGet, semanticUnderstandingTaskExternalURL+"/task-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"status":"running"`)
		assert.Contains(t, w.Body.String(), "private")
	})
}

func Test_SemanticUnderstandingTaskRestHandler_DeleteTasks(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("deletes tasks", func(t *testing.T) {
		engine, suts := setupSemanticUnderstandingTaskHandlerTest(t)
		suts.EXPECT().Delete(gomock.Any(), []string{"task-1", "task-2"}, true).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, semanticUnderstandingTaskURL+"/task-1,task-2?ignore_missing=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("deletes task by external api", func(t *testing.T) {
		engine, as, suts := setupSemanticUnderstandingTaskExternalHandlerTest(t)
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).
			Return(hydra.Visitor{ID: "user-1", Type: hydra.VisitorType_User}, nil)
		suts.EXPECT().Delete(gomock.Any(), []string{"task-1"}, false).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, semanticUnderstandingTaskExternalURL+"/task-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}
