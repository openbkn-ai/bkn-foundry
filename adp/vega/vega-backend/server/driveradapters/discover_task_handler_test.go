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
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func Test_DiscoverTaskRestHandler_ListDiscoverTasks(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	setup := func(t *testing.T) (*gin.Engine, *vmock.MockDiscoverTaskService) {
		t.Helper()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		dts := vmock.NewMockDiscoverTaskService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, nil, nil, nil, nil, dts, nil, nil, nil)
		handler.RegisterPublic(engine)
		return engine, dts
	}

	const url = "/api/vega-backend/in/v1/discover-tasks"

	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantBody   string
	}{
		{name: "invalid offset", query: "?offset=-1", wantStatus: http.StatusBadRequest, wantBody: "VegaBackend.InvalidParameter.Offset"},
		{name: "invalid limit", query: "?limit=99999999", wantStatus: http.StatusBadRequest, wantBody: "VegaBackend.InvalidParameter.Limit"},
		{name: "invalid sort field", query: "?sort=unknown_field", wantStatus: http.StatusBadRequest, wantBody: "VegaBackend.InvalidParameter.Sort"},
		{name: "invalid direction", query: "?direction=foo", wantStatus: http.StatusBadRequest, wantBody: "VegaBackend.InvalidParameter.Direction"},
		{name: "invalid trigger type", query: "?trigger_type=foo", wantStatus: http.StatusBadRequest, wantBody: "invalid trigger_type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, _ := setup(t)
			req := httptest.NewRequest(http.MethodGet, url+tt.query, nil)
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, tt.wantStatus, w.Result().StatusCode)
			assert.Contains(t, w.Body.String(), tt.wantBody)
		})
	}

	t.Run("success with default pagination", func(t *testing.T) {
		engine, dts := setup(t)
		dts.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTask, int64, error) {
				assert.Equal(t, 0, params.Offset)
				assert.Equal(t, 20, params.Limit)
				assert.Equal(t, "f_create_time", params.Sort)
				assert.Equal(t, interfaces.DESC_DIRECTION, params.Direction)
				return []*interfaces.DiscoverTask{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("success with explicit query params", func(t *testing.T) {
		engine, dts := setup(t)
		dts.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTask, int64, error) {
				assert.Equal(t, "cat-1", params.CatalogID)
				assert.Equal(t, "sch-1", params.ScheduleID)
				assert.Equal(t, interfaces.DiscoverTaskStatusCompleted, params.Status)
				assert.Equal(t, interfaces.DiscoverTaskTriggerScheduled, params.TriggerType)
				assert.Equal(t, 5, params.Offset)
				assert.Equal(t, 10, params.Limit)
				assert.Equal(t, "f_start_time", params.Sort)
				assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
				return []*interfaces.DiscoverTask{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?catalog_id=cat-1&schedule_id=sch-1&status=completed&trigger_type=scheduled&offset=5&limit=10&sort=start_time&direction=asc", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})
}
