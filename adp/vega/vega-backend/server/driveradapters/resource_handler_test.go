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

func setupResourceHandlerTest(t *testing.T) (*gin.Engine, *vmock.MockCatalogService, *vmock.MockResourceService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	cs := vmock.NewMockCatalogService(mockCtrl)
	rs := vmock.NewMockResourceService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, rs, nil, nil, nil, nil, nil, nil, nil)
	handler.RegisterPublic(engine)
	return engine, cs, rs
}

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

func Test_ResourceRestHandler_CreateResource(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/resources"
	body := `{"id":"res-1","catalog_id":"catalog-1","name":"dataset","category":"dataset","schema_definition":[{"name":"title","type":"string"}]}`

	t.Run("creates dataset resource", func(t *testing.T) {
		engine, cs, rs := setupResourceHandlerTest(t)
		cs.EXPECT().CheckExistByID(gomock.Any(), "catalog-1").Return(true, nil)
		rs.EXPECT().CheckExistByName(gomock.Any(), "catalog-1", "dataset").Return(false, nil)
		rs.EXPECT().CheckExistByID(gomock.Any(), "res-1").Return(false, nil)
		rs.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
				assert.Equal(t, "dataset", req.Name)
				return &interfaces.Resource{ID: "res-1", Name: req.Name}, nil
			})

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"res-1"`)
	})

	t.Run("rejects missing catalog", func(t *testing.T) {
		engine, cs, _ := setupResourceHandlerTest(t)
		cs.EXPECT().CheckExistByID(gomock.Any(), "catalog-1").Return(false, nil)

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.CatalogNotFound")
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		engine, cs, rs := setupResourceHandlerTest(t)
		cs.EXPECT().CheckExistByID(gomock.Any(), "catalog-1").Return(true, nil)
		rs.EXPECT().CheckExistByName(gomock.Any(), "catalog-1", "dataset").Return(true, nil)

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.NameExists")
	})
}

func Test_ResourceRestHandler_GetResources(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/resources/res-1,res-2"

	t.Run("gets resources by ids", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"res-1", "res-2"}).
			Return([]*interfaces.Resource{
				{ID: "res-1", Name: "one"},
				{ID: "res-2", Name: "two"},
			}, nil)

		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"res-1"`)
		assert.Contains(t, w.Body.String(), `"id":"res-2"`)
	})

	t.Run("returns not found when any id is missing", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"res-1", "res-2"}).
			Return([]*interfaces.Resource{{ID: "res-1", Name: "one"}}, nil)

		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "id res-2 not found")
	})
}

func Test_ResourceRestHandler_UpdateResource(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/resources/res-1"
	body := `{"catalog_id":"catalog-1","name":"dataset-new","category":"dataset","schema_definition":[{"name":"title","type":"string"}]}`

	t.Run("updates resource", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		current := &interfaces.Resource{ID: "res-1", CatalogID: "catalog-1", Name: "dataset", Category: interfaces.ResourceCategoryDataset}
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(current, nil)
		rs.EXPECT().CheckExistByName(gomock.Any(), "catalog-1", "dataset-new").Return(false, nil)
		rs.EXPECT().Update(gomock.Any(), current, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *interfaces.Resource, req *interfaces.ResourceRequest) error {
				assert.Equal(t, "dataset-new", req.Name)
				return nil
			})

		req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("rejects duplicate renamed resource", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		current := &interfaces.Resource{ID: "res-1", CatalogID: "catalog-1", Name: "dataset", Category: interfaces.ResourceCategoryDataset}
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(current, nil)
		rs.EXPECT().CheckExistByName(gomock.Any(), "catalog-1", "dataset-new").Return(true, nil)

		req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.NameExists")
	})
}

func Test_ResourceRestHandler_DeleteResources(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("deletes existing resources", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().CheckExistByID(gomock.Any(), "res-1").Return(true, nil)
		rs.EXPECT().CheckExistByID(gomock.Any(), "res-2").Return(true, nil)
		rs.EXPECT().DeleteByIDs(gomock.Any(), []string{"res-1", "res-2"}).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1,res-2", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("ignores missing resources when requested", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().CheckExistByID(gomock.Any(), "res-1").Return(true, nil)
		rs.EXPECT().CheckExistByID(gomock.Any(), "missing").Return(false, nil)
		rs.EXPECT().DeleteByIDs(gomock.Any(), []string{"res-1"}).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1,missing?ignore_missing=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}
