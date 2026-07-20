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

	"github.com/agiledragon/gomonkey/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
	"vega-backend/logics/query"
)

func setupRawQueryHandlerTest(t *testing.T) *gin.Engine {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.RegisterPublic(engine)
	return engine
}

func Test_RawQueryRestHandler_RawQuery(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/resources/query"

	t.Run("executes raw query with defaults", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		service := vmock.NewMockRawQueryService(ctrl)
		service.EXPECT().
			Execute(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.RawQueryRequest{})).
			DoAndReturn(func(_ context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
				assert.Equal(t, interfaces.QueryFormatSQL, req.QueryFormat)
				assert.Equal(t, "postgres", req.EffectiveInputDialect())
				assert.Equal(t, 60, req.QueryTimeoutSec)
				return &interfaces.RawQueryResponse{
					Columns: []interfaces.ColumnInfo{{Name: "id", Type: "string"}},
					Entries: []map[string]any{{"id": "1"}},
				}, nil
			})
		engine := setupRawQueryHandlerTest(t)
		patches := gomonkey.ApplyFunc(query.NewRawQueryService, func(*common.AppSetting) interfaces.RawQueryService {
			return service
		})
		defer patches.Reset()

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"query_format":"sql","query":"select 1"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"columns"`)
		assert.Contains(t, w.Body.String(), `"entries"`)
	})

	t.Run("rejects invalid query timeout", func(t *testing.T) {
		engine := setupRawQueryHandlerTest(t)

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"query_format":"sql","query":"select 1","query_timeout_sec":3601}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Query.InvalidParameter.QueryTimeout")
	})
}
