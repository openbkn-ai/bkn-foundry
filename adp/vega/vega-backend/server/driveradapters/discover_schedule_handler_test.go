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
