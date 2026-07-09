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

func setupCatalogHandlerTest(t *testing.T) (*gin.Engine, *vmock.MockCatalogService, *vmock.MockDiscoverTaskService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	cs := vmock.NewMockCatalogService(mockCtrl)
	dts := vmock.NewMockDiscoverTaskService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, nil, nil, nil, nil, dts, nil, nil, nil)
	handler.RegisterPublic(engine)
	return engine, cs, dts
}

func Test_CatalogRestHandler_ListCatalogs(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/catalogs"

	tests := []struct {
		name     string
		query    string
		wantBody string
	}{
		{name: "invalid type", query: "?type=unknown", wantBody: "invalid type: unknown"},
		{name: "invalid health check status", query: "?health_check_status=unknown", wantBody: "invalid health_check_status: unknown"},
		{name: "invalid enabled", query: "?enabled=maybe", wantBody: "invalid enabled: maybe"},
		{name: "invalid disabled health check status", query: "?health_check_status=disabled", wantBody: "invalid health_check_status: disabled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, _, _ := setupCatalogHandlerTest(t)
			req := httptest.NewRequest(http.MethodGet, url+tt.query, nil)
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
			assert.Contains(t, w.Body.String(), "VegaBackend.Catalog.InvalidParameter")
			assert.Contains(t, w.Body.String(), tt.wantBody)
		})
	}

	t.Run("success list catalogs with name type and health check status", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.Catalog, int64, error) {
				assert.Equal(t, "lake", params.Name)
				assert.Equal(t, interfaces.CatalogTypePhysical, params.Type)
				assert.Equal(t, interfaces.CatalogHealthStatusHealthy, params.HealthCheckStatus)
				return []*interfaces.Catalog{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?name=lake&type=physical&health_check_status=healthy", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("success list catalogs with enabled filter", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.Catalog, int64, error) {
				require.NotNil(t, params.Enabled)
				assert.False(t, *params.Enabled)
				return []*interfaces.Catalog{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?enabled=false", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("success list catalogs with unchecked health check status", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.Catalog, int64, error) {
				assert.Equal(t, interfaces.CatalogHealthStatusUnchecked, params.HealthCheckStatus)
				return []*interfaces.Catalog{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?health_check_status=unchecked", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})
}

func Test_CatalogRestHandler_SetCatalogEnabled(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("enable disabled catalog", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Enabled: false}, nil)
		cs.EXPECT().SetEnabled(gomock.Any(), gomock.Any(), true).Return(nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/enable", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("disable enabled catalog", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Enabled: true}, nil)
		cs.EXPECT().SetEnabled(gomock.Any(), gomock.Any(), false).Return(nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/disable", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("enable already enabled catalog is idempotent", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Enabled: true}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/enable", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}

func Test_CatalogRestHandler_UpdateRejectsEnabledChange(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine, cs, _ := setupCatalogHandlerTest(t)
	cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{
			ID:            "catalog-1",
			Name:          "catalog",
			Enabled:       false,
			ConnectorType: "mariadb",
			ConnectorCfg:  interfaces.ConnectorConfig{},
		}, nil)

	body := `{"id":"catalog-1","name":"catalog","enabled":true,"connector_type":"mariadb","connector_config":{}}`
	req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Result().StatusCode)
	assert.Contains(t, w.Body.String(), "use POST /catalogs/{id}/enable or /disable to change enabled state")
}

func Test_CatalogRestHandler_UpdateAllowsDatabaseChange(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine, cs, _ := setupCatalogHandlerTest(t)
	cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{
			ID:            "catalog-1",
			Name:          "catalog",
			Enabled:       true,
			ConnectorType: "mariadb",
			ConnectorCfg: interfaces.ConnectorConfig{
				"host":     "localhost",
				"database": "db1",
			},
		}, nil)
	cs.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *interfaces.Catalog, req *interfaces.CatalogRequest) error {
			assert.Equal(t, "db2", req.ConnectorCfg["database"])
			return nil
		})

	body := `{"id":"catalog-1","name":"catalog","enabled":true,"connector_type":"mariadb","connector_config":{"host":"localhost","database":"db2"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
}

func Test_CatalogRestHandler_DiscoverRejectsDisabledCatalog(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine, cs, _ := setupCatalogHandlerTest(t)
	cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Enabled: false}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/discover", nil)
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Result().StatusCode)
	assert.Contains(t, w.Body.String(), "VegaBackend.Catalog.IsDisabled")
}

func Test_CatalogRestHandler_DiscoverRejectsLogicalCatalog(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine, cs, _ := setupCatalogHandlerTest(t)
	cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Type: interfaces.CatalogTypeLogical, Enabled: true}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/discover", nil)
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	assert.Contains(t, w.Body.String(), "VegaBackend.Catalog.InvalidParameter.Type")
	assert.Contains(t, w.Body.String(), "discover only supports physical catalogs")
}
