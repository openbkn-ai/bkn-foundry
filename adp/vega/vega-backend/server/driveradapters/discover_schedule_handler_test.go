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

func setupDiscoverScheduleHandlerTest(
	t *testing.T,
) (*gin.Engine, *vmock.MockCatalogService, *vmock.MockDiscoverScheduleService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	cs := vmock.NewMockCatalogService(mockCtrl)
	dss := vmock.NewMockDiscoverScheduleService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, nil, nil, nil, nil, nil, dss, nil, nil)
	handler.RegisterPublic(engine)
	return engine, cs, dss
}

func Test_DiscoverScheduleRestHandler_CreateDiscoverSchedule(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/discover-schedules"
	body := `{"name":"daily","catalog_id":"catalog-1","cron_expr":"0 0 * * *","strategy":"full_sync","enabled":false}`

	t.Run("creates disabled discover schedule", func(t *testing.T) {
		engine, cs, dss := setupDiscoverScheduleHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).Return(&interfaces.Catalog{ID: "catalog-1"}, nil)
		dss.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.DiscoverScheduleRequest) (string, error) {
				assert.Equal(t, "daily", req.Name)
				assert.False(t, req.Enabled)
				return "schedule-1", nil
			})

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"schedule-1"`)
	})

	t.Run("rejects missing catalog", func(t *testing.T) {
		engine, cs, _ := setupDiscoverScheduleHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).Return(nil, nil)

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Catalog.NotFound")
	})

	t.Run("rejects invalid cron expression", func(t *testing.T) {
		engine, _, _ := setupDiscoverScheduleHandlerTest(t)

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"name":"daily","catalog_id":"catalog-1","cron_expr":"bad","enabled":false}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.DiscoverSchedule.InvalidCronExpr")
	})
}

func Test_DiscoverScheduleRestHandler_ListDiscoverSchedules(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	setup := func(t *testing.T) (*gin.Engine, *vmock.MockDiscoverScheduleService) {
		t.Helper()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		dss := vmock.NewMockDiscoverScheduleService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, nil, nil, nil, nil, nil, dss, nil, nil)
		handler.RegisterPublic(engine)
		return engine, dss
	}

	const url = "/api/vega-backend/in/v1/discover-schedules"

	tests := []struct {
		name     string
		query    string
		wantBody string
	}{
		{name: "invalid offset", query: "?offset=-1", wantBody: "VegaBackend.InvalidParameter.Offset"},
		{name: "invalid offset non-numeric", query: "?offset=abc", wantBody: "VegaBackend.InvalidParameter.Offset"},
		{name: "invalid limit exceeds max", query: "?limit=99999999", wantBody: "VegaBackend.InvalidParameter.Limit"},
		{name: "invalid sort field", query: "?sort=unknown_field", wantBody: "VegaBackend.InvalidParameter.Sort"},
		{name: "invalid direction", query: "?direction=foo", wantBody: "VegaBackend.InvalidParameter.Direction"},
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
		engine, dss := setup(t)
		dss.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.DiscoverScheduleQueryParams) ([]*interfaces.DiscoverSchedule, int64, error) {
				assert.Equal(t, 0, params.Offset)
				assert.Equal(t, 20, params.Limit)
				assert.Equal(t, "f_update_time", params.Sort)
				assert.Equal(t, interfaces.DESC_DIRECTION, params.Direction)
				return []*interfaces.DiscoverSchedule{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("success with explicit sort and direction", func(t *testing.T) {
		engine, dss := setup(t)
		dss.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.DiscoverScheduleQueryParams) ([]*interfaces.DiscoverSchedule, int64, error) {
				assert.Equal(t, "f_next_run", params.Sort)
				assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
				assert.Equal(t, 5, params.Offset)
				assert.Equal(t, 10, params.Limit)
				return []*interfaces.DiscoverSchedule{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?sort=next_run&direction=asc&offset=5&limit=10", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("success with catalog_id and enabled filters preserved", func(t *testing.T) {
		engine, dss := setup(t)
		dss.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.DiscoverScheduleQueryParams) ([]*interfaces.DiscoverSchedule, int64, error) {
				assert.Equal(t, "cat-1", params.CatalogID)
				require.NotNil(t, params.Enabled)
				assert.True(t, *params.Enabled)
				return []*interfaces.DiscoverSchedule{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?catalog_id=cat-1&enabled=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})
}

func Test_DiscoverScheduleRestHandler_GetDiscoverSchedule(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("gets discover schedule by id", func(t *testing.T) {
		engine, _, dss := setupDiscoverScheduleHandlerTest(t)
		dss.EXPECT().GetByID(gomock.Any(), "schedule-1").
			Return(&interfaces.DiscoverSchedule{ID: "schedule-1", Name: "daily", CatalogID: "catalog-1"}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/discover-schedules/schedule-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"schedule-1"`)
		assert.Contains(t, w.Body.String(), `"name":"daily"`)
	})

	t.Run("returns not found for nil schedule", func(t *testing.T) {
		engine, _, dss := setupDiscoverScheduleHandlerTest(t)
		dss.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/discover-schedules/missing", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.DiscoverSchedule.NotFound")
	})
}

func Test_DiscoverScheduleRestHandler_UpdateDiscoverSchedule(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	body := `{"name":"daily-new","catalog_id":"catalog-1","cron_expr":"0 1 * * *","strategy":"full_sync","enabled":false}`

	t.Run("updates disabled discover schedule", func(t *testing.T) {
		engine, _, dss := setupDiscoverScheduleHandlerTest(t)
		current := &interfaces.DiscoverSchedule{ID: "schedule-1", Name: "daily", CatalogID: "catalog-1", Enabled: false}
		dss.EXPECT().GetByID(gomock.Any(), "schedule-1").Return(current, nil)
		dss.EXPECT().Update(gomock.Any(), current, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *interfaces.DiscoverSchedule, req *interfaces.DiscoverScheduleRequest) error {
				assert.Equal(t, "daily-new", req.Name)
				return nil
			})

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/discover-schedules/schedule-1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("rejects catalog change", func(t *testing.T) {
		engine, _, dss := setupDiscoverScheduleHandlerTest(t)
		current := &interfaces.DiscoverSchedule{ID: "schedule-1", Name: "daily", CatalogID: "catalog-1", Enabled: false}
		dss.EXPECT().GetByID(gomock.Any(), "schedule-1").Return(current, nil)

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/discover-schedules/schedule-1", strings.NewReader(`{"name":"daily","catalog_id":"catalog-2","cron_expr":"0 1 * * *","strategy":"full_sync","enabled":false}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.DiscoverSchedule.CatalogMismatch")
	})
}

func Test_DiscoverScheduleRestHandler_ToggleDiscoverSchedule(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("enable already enabled schedule is idempotent", func(t *testing.T) {
		engine, _, dss := setupDiscoverScheduleHandlerTest(t)
		dss.EXPECT().GetByID(gomock.Any(), "schedule-1").
			Return(&interfaces.DiscoverSchedule{ID: "schedule-1", CatalogID: "catalog-1", Enabled: true}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/discover-schedules/schedule-1/enable", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("disable already disabled schedule is idempotent", func(t *testing.T) {
		engine, _, dss := setupDiscoverScheduleHandlerTest(t)
		dss.EXPECT().GetByID(gomock.Any(), "schedule-1").
			Return(&interfaces.DiscoverSchedule{ID: "schedule-1", CatalogID: "catalog-1", Enabled: false}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/discover-schedules/schedule-1/disable", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}
