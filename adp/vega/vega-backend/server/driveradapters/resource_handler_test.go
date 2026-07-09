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

func Test_ResourceRestHandler_ListResources(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	setup := func(t *testing.T) (*gin.Engine, *vmock.MockResourceService) {
		t.Helper()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		rs := vmock.NewMockResourceService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)
		return engine, rs
	}

	const url = "/api/vega-backend/in/v1/resources"

	t.Run("invalid category", func(t *testing.T) {
		engine, _ := setup(t)
		req := httptest.NewRequest(http.MethodGet, url+"?category=unknown", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.InvalidParameter")
		assert.Contains(t, w.Body.String(), "invalid category: unknown")
	})

	t.Run("invalid status", func(t *testing.T) {
		engine, _ := setup(t)
		req := httptest.NewRequest(http.MethodGet, url+"?status=unknown", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.InvalidParameter")
		assert.Contains(t, w.Body.String(), "invalid status: unknown")
	})

	t.Run("success list resources with name category and status", func(t *testing.T) {
		engine, rs := setup(t)
		rs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.ResourcesQueryParams) ([]*interfaces.Resource, int64, error) {
				assert.Equal(t, "orders", params.Name)
				assert.Equal(t, interfaces.ResourceCategoryDataset, params.Category)
				assert.Equal(t, interfaces.ResourceStatusActive, params.Status)
				return []*interfaces.Resource{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?name=orders&category=dataset&status=active", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})
}
