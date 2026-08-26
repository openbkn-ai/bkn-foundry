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
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	verrors "vega-backend/errors"
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
	handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, nil, nil, nil, nil, dts, nil, nil)
	handler.RegisterPublic(engine)
	return engine, cs, dts
}

func setupCatalogHandlerWithResourceTest(
	t *testing.T,
) (*gin.Engine, *vmock.MockCatalogService, *vmock.MockDiscoverTaskService, *vmock.MockResourceService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	cs := vmock.NewMockCatalogService(mockCtrl)
	dts := vmock.NewMockDiscoverTaskService(mockCtrl)
	rs := vmock.NewMockResourceService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, rs, nil, nil, nil, dts, nil, nil)
	handler.RegisterPublic(engine)
	return engine, cs, dts, rs
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
			DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.CatalogSummary, int64, error) {
				assert.Equal(t, "lake", params.Name)
				assert.Equal(t, interfaces.CatalogTypePhysical, params.Type)
				assert.Equal(t, interfaces.CatalogHealthStatusHealthy, params.HealthCheckStatus)
				assert.Equal(t, "update_time", params.Sort)
				assert.Equal(t, interfaces.DESC_DIRECTION, params.Direction)
				return []*interfaces.CatalogSummary{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?name=lake&type=physical&health_check_status=healthy", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("success list catalogs with connector type", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.CatalogSummary, int64, error) {
				assert.Equal(t, "postgresql", params.ConnectorType)
				return []*interfaces.CatalogSummary{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?connector_type=postgresql", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("success list catalogs with enabled filter", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.CatalogSummary, int64, error) {
				require.NotNil(t, params.Enabled)
				assert.False(t, *params.Enabled)
				return []*interfaces.CatalogSummary{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?enabled=false", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("success list catalogs with unchecked health check status", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.CatalogSummary, int64, error) {
				assert.Equal(t, interfaces.CatalogHealthStatusUnchecked, params.HealthCheckStatus)
				return []*interfaces.CatalogSummary{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?health_check_status=unchecked", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("keeps API sort field for data access mapping", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.CatalogSummary, int64, error) {
				assert.Equal(t, "name", params.Sort)
				assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
				return []*interfaces.CatalogSummary{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?sort=name&direction=asc", nil)
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

func Test_CatalogRestHandler_CreateCatalog(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/catalogs"
	body := `{"id":"catalog-1","name":"catalog","enabled":true,"connector_type":"mariadb","connector_config":{}}`

	t.Run("creates catalog", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().CheckExistByName(gomock.Any(), "catalog").Return(false, nil)
		cs.EXPECT().CheckExistByID(gomock.Any(), "catalog-1").Return(false, nil)
		cs.EXPECT().Create(gomock.Any(), gomock.Any(), false).
			DoAndReturn(func(_ context.Context, req *interfaces.CatalogRequest, _ bool) (string, error) {
				assert.Equal(t, "catalog", req.Name)
				return "catalog-1", nil
			})

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"catalog-1"`)
	})

	t.Run("passes allow_unhealthy query parameter to service", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().CheckExistByName(gomock.Any(), "catalog").Return(false, nil)
		cs.EXPECT().CheckExistByID(gomock.Any(), "catalog-1").Return(false, nil)
		cs.EXPECT().Create(gomock.Any(), gomock.Any(), true).Return("catalog-1", nil)

		req := httptest.NewRequest(http.MethodPost, url+"?allow_unhealthy=true", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
	})

	t.Run("rejects invalid allow_unhealthy query parameter", func(t *testing.T) {
		engine, _, _ := setupCatalogHandlerTest(t)

		req := httptest.NewRequest(http.MethodPost, url+"?allow_unhealthy=invalid", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().CheckExistByName(gomock.Any(), "catalog").Return(true, nil)

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Catalog.NameExists")
	})

	t.Run("rejects duplicate id", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().CheckExistByName(gomock.Any(), "catalog").Return(false, nil)
		cs.EXPECT().CheckExistByID(gomock.Any(), "catalog-1").Return(true, nil)

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Catalog.IDExists")
	})
}

func Test_CatalogRestHandler_GetCatalogs(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/catalogs/catalog-1,catalog-2"

	t.Run("gets catalogs by ids", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().GetByIDs(gomock.Any(), []string{"catalog-1", "catalog-2"}).
			Return([]*interfaces.Catalog{
				{ID: "catalog-1", Name: "one"},
				{ID: "catalog-2", Name: "two"},
			}, nil)

		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"catalog-1"`)
		assert.Contains(t, w.Body.String(), `"id":"catalog-2"`)
	})

	t.Run("returns not found when any id is missing", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().GetByIDs(gomock.Any(), []string{"catalog-1", "catalog-2"}).
			Return([]*interfaces.Catalog{{ID: "catalog-1", Name: "one"}}, nil)

		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "id catalog-2 not found")
	})
}

func Test_CatalogRestHandler_UpdateRejectsEnabledChange(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("rejects enabled change through update API", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{
				ID:            "catalog-1",
				Name:          "catalog",
				Enabled:       false,
				ConnectorType: "mariadb",
				ConnectorCfg:  interfaces.ConnectorConfig{},
			}, nil)

		body := `{"id":"catalog-1","name":"catalog","enabled":true,"connector_type":"mariadb","connector_config":{},"expected_update_time":1}`
		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "use POST /catalogs/{id}/enable or /disable to change enabled state")
	})
}

func Test_CatalogRestHandler_UpdateRequiresExpectedUpdateTime(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine, _, _ := setupCatalogHandlerTest(t)
	body := `{"id":"catalog-1","name":"catalog","enabled":true,"connector_type":"mariadb","connector_config":{}}`
	req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	assert.Contains(t, w.Body.String(), "expected_update_time is required")
}

func Test_CatalogRestHandler_DeleteCatalog(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("deletes catalog through catalog service", func(t *testing.T) {
		engine, cs, _, _ := setupCatalogHandlerWithResourceTest(t)
		cs.EXPECT().CheckExistByID(gomock.Any(), "catalog-1").Return(true, nil)
		cs.EXPECT().DeleteByID(gomock.Any(), "catalog-1").Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/catalogs/catalog-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("dry run reports catalog deletion impact without deleting", func(t *testing.T) {
		engine := gin.New()
		engine.Use(gin.Recovery())
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		cs := vmock.NewMockCatalogService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, nil, nil, nil, nil, dts, nil, nil)
		handler.RegisterPublic(engine)

		cs.EXPECT().CheckExistByID(gomock.Any(), "catalog-1").Return(true, nil)
		cs.EXPECT().GetDeletionImpact(gomock.Any(), "catalog-1").Return(&interfaces.CatalogDeletionImpact{
			Blockers:                    []string{interfaces.CatalogDeletionBlockerProtectedResources},
			CanDelete:                   false,
			CatalogHealthCheckSchedules: 1,
			CatalogID:                   "catalog-1",
			BuildTasks:                  interfaces.CatalogDeletionTaskImpact{WillCancel: 1, Blocking: 2},
			DiscoverSchedules:           2,
			DiscoverTasks:               interfaces.CatalogDeletionTaskImpact{WillCancel: 1, Blocking: 2},
			ProtectedResources:          1,
			SemanticUnderstandingTasks:  interfaces.CatalogDeletionTaskImpact{WillCancel: 1, Blocking: 1},
		}, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/catalogs/catalog-1?dry_run=true", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"can_delete":false`)
		assert.Contains(t, w.Body.String(), `"blockers":["protected_resources"]`)
		assert.Contains(t, w.Body.String(), `"protected_resources":1`)
		assert.Contains(t, w.Body.String(), `"build_tasks":{"will_cancel":1,"blocking":2}`)
		assert.Contains(t, w.Body.String(), `"catalog_health_check_schedules":1`)
		assert.Contains(t, w.Body.String(), `"discover_schedules":2`)
		assert.Contains(t, w.Body.String(), `"discover_tasks":{"will_cancel":1,"blocking":2}`)
		assert.Contains(t, w.Body.String(), `"semantic_understanding_tasks":{"will_cancel":1,"blocking":1}`)
	})

	t.Run("rejects comma-separated catalog ids", func(t *testing.T) {
		engine, _, _, _ := setupCatalogHandlerWithResourceTest(t)
		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/catalogs/catalog-1,catalog-2", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "catalog delete accepts exactly one id")
	})

	t.Run("rejects missing catalog", func(t *testing.T) {
		engine, cs, _, _ := setupCatalogHandlerWithResourceTest(t)
		cs.EXPECT().CheckExistByID(gomock.Any(), "missing").Return(false, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/catalogs/missing", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Catalog.NotFound")
	})

	t.Run("returns catalog service deletion guard", func(t *testing.T) {
		engine, cs, _, _ := setupCatalogHandlerWithResourceTest(t)
		cs.EXPECT().CheckExistByID(gomock.Any(), "catalog-1").Return(true, nil)
		cs.EXPECT().DeleteByID(gomock.Any(), "catalog-1").Return(
			rest.NewHTTPError(context.Background(), http.StatusConflict, verrors.VegaBackend_Catalog_InvalidParameter).
				WithErrorDetails("catalog has active dependencies"),
		)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/catalogs/catalog-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "catalog has active dependencies")
	})
}

func Test_CatalogRestHandler_GetCatalogHealthStatus(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("gets catalog health status", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{
				ID: "catalog-1",
				CatalogHealthCheckStatus: interfaces.CatalogHealthCheckStatus{
					HealthCheckStatus: interfaces.CatalogHealthStatusHealthy,
					LastCheckTime:     123,
					HealthCheckResult: "ok",
				},
			}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/catalogs/catalog-1/health-status", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"health_check_status":"healthy"`)
		assert.Contains(t, w.Body.String(), `"health_check_result":"ok"`)
	})
}

func Test_CatalogRestHandler_TestConnection(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("returns success for healthy status", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().TestConnection(gomock.Any(), "catalog-1").
			Return(&interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusHealthy,
				HealthCheckResult: "ok",
			}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/test-connection", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"success":true`)
		assert.Contains(t, w.Body.String(), `"message":"ok"`)
	})

	t.Run("returns false for unhealthy status", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().TestConnection(gomock.Any(), "catalog-1").
			Return(&interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusUnhealthy,
				HealthCheckResult: "failed",
			}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/test-connection", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"success":false`)
		assert.Contains(t, w.Body.String(), `"message":"failed"`)
	})
}

func Test_CatalogRestHandler_UpdateAllowsDatabaseChange(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("allows database change through update API", func(t *testing.T) {
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
		cs.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any(), false).
			DoAndReturn(func(_ context.Context, _ *interfaces.Catalog, req *interfaces.CatalogRequest, _ bool) error {
				assert.Equal(t, "db2", req.ConnectorCfg["database"])
				return nil
			})

		body := `{"id":"catalog-1","name":"catalog","enabled":true,"connector_type":"mariadb","connector_config":{"host":"localhost","database":"db2"},"expected_update_time":1}`
		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}

func Test_CatalogRestHandler_UpdatePassesAllowUnhealthy(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine, cs, _ := setupCatalogHandlerTest(t)
	cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).Return(&interfaces.Catalog{
		ID:            "catalog-1",
		Name:          "catalog",
		Enabled:       true,
		ConnectorType: "mariadb",
	}, nil)
	cs.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any(), true).Return(nil)

	body := `{"id":"catalog-1","name":"catalog","enabled":true,"connector_type":"mariadb","connector_config":{},"expected_update_time":1}`
	req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1?allow_unhealthy=true", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
}

func Test_CatalogRestHandler_DiscoverRejectsDisabledCatalog(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("rejects disabled catalog", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Enabled: false}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/discover", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Catalog.IsDisabled")
	})
}

func Test_CatalogRestHandler_DiscoverRejectsLogicalCatalog(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("rejects logical catalog", func(t *testing.T) {
		engine, cs, _ := setupCatalogHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Type: interfaces.CatalogTypeLogical, Enabled: true}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/discover", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Catalog.InvalidParameter.Type")
		assert.Contains(t, w.Body.String(), "discover only supports physical catalogs")
	})
}
