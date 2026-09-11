// Copyright openbkn.ai
// Licensed under the Apache License, Version 2.0.

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

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestGetResourceSchemaForDependencyChecksExactOperation(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	ctrl := gomock.NewController(t)
	resources := vmock.NewMockResourceService(ctrl)
	resources.EXPECT().InternalGetByID(gomock.Any(), nil, "resource-1").Return(&interfaces.Resource{
		ID:               "resource-1",
		SchemaDefinition: []*interfaces.Property{{Name: "order_id", Type: "integer"}},
	}, nil)
	resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_QUERY_DATA).
		DoAndReturn(func(ctx context.Context, _ string, _ string) error {
			account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
			assert.Equal(t, interfaces.AccountInfo{ID: "historical-grantor", Type: interfaces.ACCESSOR_TYPE_USER}, account)
			return nil
		})

	engine := gin.New()
	engine.GET("/api/vega-backend/in/v1/resources/:id/schema", (&restHandler{rs: resources}).GetResourceSchemaForDependency)
	req := httptest.NewRequest(http.MethodGet,
		"/api/vega-backend/in/v1/resources/resource-1/schema?operation=query_data", nil)
	req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "historical-grantor")
	req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, interfaces.ACCESSOR_TYPE_USER)
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"schema_definition"`)
	assert.Contains(t, w.Body.String(), `"order_id"`)
}

func TestGetResourceSchemaForDependencyRejectsUnknownOperation(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine := gin.New()
	engine.GET("/api/vega-backend/in/v1/resources/:id/schema", (&restHandler{}).GetResourceSchemaForDependency)
	req := httptest.NewRequest(http.MethodGet,
		"/api/vega-backend/in/v1/resources/resource-1/schema?operation=modify", nil)
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetResourceSchemaForDependencyReturnsNotFoundBeforeAuthorization(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	ctrl := gomock.NewController(t)
	resources := vmock.NewMockResourceService(ctrl)
	resources.EXPECT().InternalGetByID(gomock.Any(), nil, "missing").Return(nil, nil)

	engine := gin.New()
	engine.GET("/api/vega-backend/in/v1/resources/:id/schema", (&restHandler{rs: resources}).GetResourceSchemaForDependency)
	req := httptest.NewRequest(http.MethodGet,
		"/api/vega-backend/in/v1/resources/missing/schema?operation=view_detail", nil)
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
}
