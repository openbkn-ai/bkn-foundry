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

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
)

func TestAuthResourceRestHandlerListAuthorizationResourcesByIn(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("lists catalog resources without OAuth or sensitive fields", func(t *testing.T) {
		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		as := vmock.NewMockAuthService(mockCtrl)
		cs := vmock.NewMockCatalogService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, as, cs, nil, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		cs.EXPECT().ListAuthResourceEntries(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.AuthResourceQueryParams) ([]*interfaces.AuthResourceEntry, int64, error) {
				assert.Equal(t, "supply", params.Name)
				assert.Equal(t, 5, params.Offset)
				assert.Equal(t, 10, params.Limit)
				assert.Equal(t, "name", params.Sort)
				assert.Equal(t, "ASC", params.Direction)
				assert.False(t, params.IncludeBuiltin)
				return []*interfaces.AuthResourceEntry{{ID: "catalog-1", Name: "Supply Chain"}}, 1, nil
			})

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/authorization-resources?resource_type=catalog&name=supply&offset=5&limit=10", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"catalog-1"`)
		assert.Contains(t, w.Body.String(), `"name":"Supply Chain"`)
		assert.Contains(t, w.Body.String(), `"total":1`)
		assert.NotContains(t, w.Body.String(), `"type"`)
	})

	t.Run("rejects unsupported resource type", func(t *testing.T) {
		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		handler := MockNewRestHandler(&common.AppSetting{}, vmock.NewMockAuthService(mockCtrl), nil, nil, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/authorization-resources?resource_type=connector-type", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "valid values: catalog, resource, connector_type")
	})

	t.Run("lists connector type resources", func(t *testing.T) {
		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		cts := vmock.NewMockConnectorTypeService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, vmock.NewMockAuthService(mockCtrl), nil, nil, nil, nil, cts, nil, nil, nil)
		handler.RegisterPublic(engine)

		cts.EXPECT().ListAuthResourceEntries(gomock.Any(), gomock.Any()).
			Return([]*interfaces.AuthResourceEntry{{ID: "postgres", Name: "PostgreSQL"}}, int64(1), nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/authorization-resources?resource_type=connector_type", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"postgres"`)
		assert.Contains(t, w.Body.String(), `"total":1`)
	})

	t.Run("rejects invalid pagination query", func(t *testing.T) {
		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		handler := MockNewRestHandler(&common.AppSetting{}, vmock.NewMockAuthService(mockCtrl), nil, nil, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/authorization-resources?resource_type=resource&sort=update_time", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.InvalidParameter.Sort")
	})

	t.Run("does not register the replaced public endpoint", func(t *testing.T) {
		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		handler := MockNewRestHandler(&common.AppSetting{}, vmock.NewMockAuthService(mockCtrl), nil, nil, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/v1/auth-resources?resource_type=catalog", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
	})
}
