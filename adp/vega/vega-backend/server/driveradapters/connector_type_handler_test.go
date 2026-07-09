// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func setupConnectorTypeHandlerTest(t *testing.T) (*gin.Engine, *vmock.MockConnectorTypeService) {
	t.Helper()

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
	return engine, cts
}

func Test_ConnectorTypeRestHandler_RegisterConnectorType(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/v1/connector-types"
	body := `{"type":"remote-api","name":"Remote API","mode":"remote","category":"api","endpoint":"https://example.com"}`

	t.Run("registers connector type", func(t *testing.T) {
		engine, cts := setupConnectorTypeHandlerTest(t)
		cts.EXPECT().CheckExistByType(gomock.Any(), "remote-api").Return(false, nil)
		cts.EXPECT().Register(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.ConnectorTypeReq) error {
				assert.Equal(t, "remote-api", req.Type)
				return nil
			})

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set(interfaces.CONTENT_TYPE_NAME, interfaces.CONTENT_TYPE_JSON)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"type":"remote-api"`)
	})

	t.Run("rejects duplicate type", func(t *testing.T) {
		engine, cts := setupConnectorTypeHandlerTest(t)
		cts.EXPECT().CheckExistByType(gomock.Any(), "remote-api").Return(true, nil)

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set(interfaces.CONTENT_TYPE_NAME, interfaces.CONTENT_TYPE_JSON)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.ConnectorType.TypeExists")
	})
}

func Test_ConnectorTypeRestHandler_UpdateConnectorType(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	setup := func(t *testing.T) (*gin.Engine, *vmock.MockConnectorTypeService) {
		t.Helper()

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
		return engine, cts
	}

	const tp = "mysql"
	const url = "/api/vega-backend/v1/connector-types/" + tp
	newPutRequest := func(t *testing.T, body any) *http.Request {
		t.Helper()
		reqParamByte, err := sonic.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPut, url, bytes.NewReader(reqParamByte))
		req.Header.Set(interfaces.CONTENT_TYPE_NAME, interfaces.CONTENT_TYPE_JSON)
		return req
	}

	t.Run("body type mismatch", func(t *testing.T) {
		engine, _ := setup(t)
		req := newPutRequest(t, interfaces.ConnectorTypeReq{
			Type:     "postgres",
			Name:     "MySQL",
			Mode:     interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable,
		})
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.ConnectorType.TypeMismatch")
	})

	t.Run("success update connector type", func(t *testing.T) {
		engine, cts := setup(t)
		reqData := interfaces.ConnectorTypeReq{
			Type:     tp,
			Name:     "MySQL",
			Mode:     interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable,
		}
		cts.EXPECT().GetByType(gomock.Any(), tp).
			Return(&interfaces.ConnectorType{
				Type:     tp,
				Name:     "MySQL",
				Mode:     interfaces.ConnectorModeLocal,
				Category: interfaces.ConnectorCategoryTable,
			}, nil)
		cts.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		w := httptest.NewRecorder()
		engine.ServeHTTP(w, newPutRequest(t, reqData))

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("success update fileset connector type", func(t *testing.T) {
		engine, cts := setup(t)
		reqData := interfaces.ConnectorTypeReq{
			Type:     tp,
			Name:     "AnyShare",
			Mode:     interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryFileset,
		}
		cts.EXPECT().GetByType(gomock.Any(), tp).
			Return(&interfaces.ConnectorType{
				Type:     tp,
				Name:     "AnyShare",
				Mode:     interfaces.ConnectorModeLocal,
				Category: interfaces.ConnectorCategoryFileset,
			}, nil)
		cts.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		w := httptest.NewRecorder()
		engine.ServeHTTP(w, newPutRequest(t, reqData))

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("body type omitted", func(t *testing.T) {
		engine, _ := setup(t)
		req := newPutRequest(t, map[string]any{
			"name":     "MySQL",
			"mode":     interfaces.ConnectorModeLocal,
			"category": interfaces.ConnectorCategoryTable,
		})
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.ConnectorType.InvalidParameter.Type")
	})
}

func Test_ConnectorTypeRestHandler_GetConnectorType(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("gets connector type", func(t *testing.T) {
		engine, cts := setupConnectorTypeHandlerTest(t)
		cts.EXPECT().GetByType(gomock.Any(), "mysql").
			Return(&interfaces.ConnectorType{
				Type:     "mysql",
				Name:     "MySQL",
				Mode:     interfaces.ConnectorModeLocal,
				Category: interfaces.ConnectorCategoryTable,
			}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/v1/connector-types/mysql", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"type":"mysql"`)
		assert.Contains(t, w.Body.String(), `"name":"MySQL"`)
	})

	t.Run("returns not found for nil connector type", func(t *testing.T) {
		engine, cts := setupConnectorTypeHandlerTest(t)
		cts.EXPECT().GetByType(gomock.Any(), "missing").Return(nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/v1/connector-types/missing", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.ConnectorType.NotFound")
	})
}

func Test_ConnectorTypeRestHandler_DeleteConnectorType(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("deletes remote connector type", func(t *testing.T) {
		engine, cts := setupConnectorTypeHandlerTest(t)
		cts.EXPECT().GetByType(gomock.Any(), "remote-api").
			Return(&interfaces.ConnectorType{Type: "remote-api", Mode: interfaces.ConnectorModeRemote}, nil)
		cts.EXPECT().DeleteByType(gomock.Any(), "remote-api").Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/v1/connector-types/remote-api", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("rejects local connector type", func(t *testing.T) {
		engine, cts := setupConnectorTypeHandlerTest(t)
		cts.EXPECT().GetByType(gomock.Any(), "mysql").
			Return(&interfaces.ConnectorType{Type: "mysql", Mode: interfaces.ConnectorModeLocal}, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/v1/connector-types/mysql", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusForbidden, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "can not delete local connector type")
	})
}

func Test_ConnectorTypeRestHandler_SetConnectorTypeEnabled(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("enables connector type", func(t *testing.T) {
		engine, cts := setupConnectorTypeHandlerTest(t)
		cts.EXPECT().CheckExistByType(gomock.Any(), "remote-api").Return(true, nil)
		cts.EXPECT().SetEnabled(gomock.Any(), "remote-api", true).Return(nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/v1/connector-types/remote-api/enable", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("returns not found for missing connector type", func(t *testing.T) {
		engine, cts := setupConnectorTypeHandlerTest(t)
		cts.EXPECT().CheckExistByType(gomock.Any(), "missing").Return(false, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/v1/connector-types/missing/disable", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.ConnectorType.NotFound")
	})
}

func Test_ConnectorTypeRestHandler_ListConnectorTypes(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	setup := func(t *testing.T) (*gin.Engine, *vmock.MockConnectorTypeService) {
		t.Helper()

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
		return engine, cts
	}

	const url = "/api/vega-backend/v1/connector-types"

	tests := []struct {
		name     string
		query    string
		wantBody string
	}{
		{name: "invalid enabled", query: "?enabled=maybe", wantBody: "invalid enabled: maybe"},
		{name: "invalid mode", query: "?mode=unknown", wantBody: "invalid mode: unknown"},
		{name: "invalid category", query: "?category=unknown", wantBody: "invalid category: unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, _ := setup(t)
			req := httptest.NewRequest(http.MethodGet, url+tt.query, nil)
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
			assert.Contains(t, w.Body.String(), "VegaBackend.ConnectorType.InvalidParameter")
			assert.Contains(t, w.Body.String(), tt.wantBody)
		})
	}

	t.Run("success list connector types with name mode category and enabled", func(t *testing.T) {
		engine, cts := setup(t)
		cts.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.ConnectorTypesQueryParams) ([]*interfaces.ConnectorType, int64, error) {
				assert.Equal(t, "share", params.Name)
				assert.Equal(t, interfaces.ConnectorModeLocal, params.Mode)
				assert.Equal(t, interfaces.ConnectorCategoryFileset, params.Category)
				require.NotNil(t, params.Enabled)
				assert.True(t, *params.Enabled)
				return []*interfaces.ConnectorType{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?name=share&mode=local&category=fileset&enabled=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})
}
