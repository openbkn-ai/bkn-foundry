// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/worker"
)

func setGinMode() func() {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	return func() {
		gin.SetMode(oldMode)
	}
}

func MockNewRestHandler(
	appSetting *common.AppSetting,
	as interfaces.AuthService,
	cs interfaces.CatalogService,
	rs interfaces.ResourceService,
	bts interfaces.BuildTaskService,
	ds interfaces.DatasetService,
	cts interfaces.ConnectorTypeService,
	dts interfaces.DiscoverTaskService,
	dss interfaces.DiscoverScheduleService,
	rds interfaces.ResourceDataService,
	sw *worker.ScheduleWorker,
) *restHandler {
	return &restHandler{
		appSetting: appSetting,
		as:         as,
		cs:         cs,
		rs:         rs,
		bts:        bts,
		ds:         ds,
		cts:        cts,
		dts:        dts,
		dss:        dss,
		rds:        rds,
		sw:         sw,
	}
}

func Test_RestHandler_HealthCheck(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("returns server metadata", func(t *testing.T) {
		engine := gin.New()
		handler := &restHandler{}
		engine.GET("/health", handler.HealthCheck)

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "ServerName")
		assert.Contains(t, w.Body.String(), "ServerVersion")
	})
}

func Test_RestHandler_VerifyJsonContentType(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("allows json content type", func(t *testing.T) {
		engine := gin.New()
		handler := &restHandler{}
		engine.POST("/json", handler.verifyJsonContentType(), func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		})

		req := httptest.NewRequest(http.MethodPost, "/json", nil)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("rejects non json content type", func(t *testing.T) {
		engine := gin.New()
		handler := &restHandler{}
		engine.POST("/json", handler.verifyJsonContentType(), func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		})

		req := httptest.NewRequest(http.MethodPost, "/json", nil)
		req.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotAcceptable, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.InvalidRequestHeader.ContentType")
	})
}

func Test_RestHandler_TraceContextMiddleware(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine := gin.New()
	handler := &restHandler{}
	engine.Use(handler.TraceContextMiddleware())
	engine.GET("/trace", func(c *gin.Context) {
		traceCtx, ok := common.GetTraceContextFromCtx(c.Request.Context())
		require.True(t, ok)
		assert.Equal(t, "req_01JZVALIDREQUESTID000000024", traceCtx.RequestID)
		assert.Equal(t, map[string]string{
			"bkn.account.type": "service",
			"bkn.runtime.env":  "test",
		}, traceCtx.Baggage)
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	req.Header.Set(common.HeaderBKNRequestID, "req_01JZVALIDREQUESTID000000024")
	req.Header.Set(common.HeaderBaggage, "bkn.account.type=service,bkn.account.id=user-1,bkn.runtime.env=test,prompt=raw")
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
}
