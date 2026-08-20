// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"errors"
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

func setupCatalogHealthCheckScheduleHandlerTest(t *testing.T) (*gin.Engine, *vmock.MockCatalogService, *vmock.MockCatalogHealthCheckScheduleService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())
	ctrl := gomock.NewController(t)
	cs := vmock.NewMockCatalogService(ctrl)
	hcss := vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
	handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, nil, nil, nil, nil, nil, nil, nil)
	handler.hcss = hcss
	handler.RegisterPublic(engine)
	return engine, cs, hcss
}

func TestCatalogHealthCheckScheduleHandlerGet(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("returns schedule for physical catalog", func(t *testing.T) {
		engine, cs, hcss := setupCatalogHealthCheckScheduleHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).Return(&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, nil)
		hcss.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(&interfaces.CatalogHealthCheckSchedule{
			CatalogID: "catalog-1",
			Mode:      interfaces.CatalogHealthCheckScheduleModeInherit,
		}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/catalogs/catalog-1/health-check-schedule", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"mode":"inherit"`)
	})

	t.Run("rejects logical catalog", func(t *testing.T) {
		engine, cs, _ := setupCatalogHealthCheckScheduleHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).Return(&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypeLogical}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/catalogs/catalog-1/health-check-schedule", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), verrors.VegaBackend_CatalogHealthCheckSchedule_InvalidParameter)
		assert.Contains(t, w.Body.String(), "only supported for physical catalogs")
	})

	t.Run("preserves catalog not found error", func(t *testing.T) {
		engine, cs, _ := setupCatalogHealthCheckScheduleHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "missing", false).Return(nil,
			rest.NewHTTPError(context.Background(), http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound))

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/catalogs/missing/health-check-schedule", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), verrors.VegaBackend_Catalog_NotFound)
	})

	t.Run("uses generic internal error for unexpected schedule service error", func(t *testing.T) {
		engine, cs, hcss := setupCatalogHealthCheckScheduleHandlerTest(t)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).Return(
			&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, nil)
		hcss.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(nil, errors.New("unexpected error"))

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/catalogs/catalog-1/health-check-schedule", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), verrors.VegaBackend_CatalogHealthCheckSchedule_InternalError)
	})

}

func TestCatalogHealthCheckScheduleHandlerUpdate(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("updates schedule", func(t *testing.T) {
		engine, _, hcss := setupCatalogHealthCheckScheduleHandlerTest(t)
		expectedUpdateTime := int64(1)
		hcss.EXPECT().Update(gomock.Any(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{
			Mode:               interfaces.CatalogHealthCheckScheduleModeEnabled,
			CronExpr:           "0 * * * *",
			ExpectedUpdateTime: expectedUpdateTime,
		}).Return(&interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1", Mode: interfaces.CatalogHealthCheckScheduleModeEnabled, CronExpr: "0 * * * *"}, nil)

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1/health-check-schedule", strings.NewReader(`{"mode":"enabled","cron_expr":"0 * * * *","expected_update_time":1}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"cron_expr":"0 * * * *"`)
	})

	t.Run("rejects missing expected update time", func(t *testing.T) {
		engine, _, _ := setupCatalogHealthCheckScheduleHandlerTest(t)
		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1/health-check-schedule", strings.NewReader(`{"mode":"enabled","cron_expr":"0 * * * *"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "expected_update_time is required")
	})

	t.Run("returns validation error from service", func(t *testing.T) {
		engine, _, hcss := setupCatalogHealthCheckScheduleHandlerTest(t)
		hcss.EXPECT().Update(gomock.Any(), "catalog-1", gomock.Any()).Return(nil,
			rest.NewHTTPError(context.Background(), http.StatusBadRequest, verrors.VegaBackend_CatalogHealthCheckSchedule_InvalidParameter).WithErrorDetails("cron_expr is required"))

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1/health-check-schedule", strings.NewReader(`{"mode":"enabled","expected_update_time":1}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), verrors.VegaBackend_CatalogHealthCheckSchedule_InvalidParameter)
		assert.Contains(t, w.Body.String(), "cron_expr is required")
	})

}
