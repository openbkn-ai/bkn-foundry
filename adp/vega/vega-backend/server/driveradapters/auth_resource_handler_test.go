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
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func Test_AuthResourceRestHandler_ListAuthResourcesRoute(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	as := vmock.NewMockAuthService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, as, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.RegisterPublic(engine)

	as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
		Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/v1/auth-resources", nil)
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	assert.Contains(t, w.Body.String(), "resource_type is invalid")
}

func Test_AuthResourceRestHandler_ListConnectorTypeResources(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	as := vmock.NewMockAuthService(mockCtrl)
	cts := vmock.NewMockConnectorTypeService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, as, nil, nil, nil, nil, cts, nil, nil, nil, nil)
	handler.RegisterPublic(engine)

	as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
		Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User}, nil)

	cts.EXPECT().ListAuthResources(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.AuthResourceQueryParams) ([]*interfaces.AuthResourceEntry, int64, error) {
			assert.Equal(t, "mysql", params.Keyword)
			return []*interfaces.AuthResourceEntry{
				{ID: interfaces.ConnectorTypeMySQL, Type: interfaces.AuthResourceTypeConnectorType, Name: "MySQL"},
			}, int64(1), nil
		})

	req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/v1/auth-resources?resource_type=connector-type&keyword=mysql", nil)
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	assert.Contains(t, w.Body.String(), `"id":"mysql"`)
	assert.Contains(t, w.Body.String(), `"type":"connector-type"`)
}

func Test_AuthResourceRestHandler_RejectUnsupportedSort(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	as := vmock.NewMockAuthService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, as, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.RegisterPublic(engine)

	as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
		Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/v1/auth-resources?resource_type=resource&sort=update_time", nil)
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	assert.Contains(t, w.Body.String(), "VegaBackend.InvalidParameter.Sort")
}
