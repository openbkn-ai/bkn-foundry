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

func setupResourceHandlerTest(t *testing.T) (*gin.Engine, *vmock.MockCatalogService, *vmock.MockResourceService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	cs := vmock.NewMockCatalogService(mockCtrl)
	rs := vmock.NewMockResourceService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, rs, nil, nil, nil, nil, nil, nil)
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
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, nil, nil, nil, nil, nil)
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

	t.Run("success list resources with schema", func(t *testing.T) {
		engine, rs := setup(t)
		rs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.ResourcesQueryParams) ([]*interfaces.Resource, int64, error) {
				assert.Equal(t, "orders", params.Name)
				assert.Equal(t, interfaces.ResourceCategoryDataset, params.Category)
				assert.Equal(t, interfaces.ResourceStatusActive, params.Status)
				assert.Equal(t, "external_data", params.Schema)
				assert.Equal(t, "update_time", params.Sort)
				assert.Equal(t, "DESC", params.Direction)
				return []*interfaces.Resource{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?name=orders&category=dataset&status=active&schema=external_data", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("keeps API sort field for data access mapping", func(t *testing.T) {
		engine, rs := setup(t)
		rs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.ResourcesQueryParams) ([]*interfaces.Resource, int64, error) {
				assert.Equal(t, "name", params.Sort)
				assert.Equal(t, "ASC", params.Direction)
				return []*interfaces.Resource{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?sort=name&direction=asc", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})
}

func Test_ResourceRestHandler_SetResourceEnabled(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	setup := func(t *testing.T) (*gin.Engine, *vmock.MockResourceService) {
		t.Helper()

		engine := gin.New()
		engine.Use(gin.Recovery())
		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		rs := vmock.NewMockResourceService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)
		return engine, rs
	}

	t.Run("enables disabled resource without changing discovery state", func(t *testing.T) {
		engine, rs := setup(t)
		resource := &interfaces.Resource{
			ID:                 "res-1",
			Name:               "orders",
			Enabled:            false,
			Status:             interfaces.ResourceStatusStale,
			LastDiscoverStatus: interfaces.DiscoverStatusMissing,
		}
		rs.EXPECT().SetEnabled(gomock.Any(), "res-1", true).
			DoAndReturn(func(_ context.Context, id string, enabled bool) (*interfaces.Resource, error) {
				assert.Equal(t, "res-1", id)
				assert.True(t, enabled)
				return resource, nil
			})

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/enable", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
		assert.False(t, resource.Enabled)
		assert.Equal(t, interfaces.ResourceStatusStale, resource.Status)
		assert.Equal(t, interfaces.DiscoverStatusMissing, resource.LastDiscoverStatus)
	})

	t.Run("disable is idempotent", func(t *testing.T) {
		engine, rs := setup(t)
		resource := &interfaces.Resource{
			ID: "res-1", Name: "orders", Enabled: false,
		}
		rs.EXPECT().SetEnabled(gomock.Any(), "res-1", false).Return(resource, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/disable", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}

func Test_ResourceRestHandler_CreateResource(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/resources"
	body := `{"id":"res-1","catalog_id":"catalog-1","name":"dataset","category":"dataset","schema_definition":[{"name":"title","type":"string"}]}`

	t.Run("creates dataset resource", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
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
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil,
			rest.NewHTTPError(context.Background(), http.StatusNotFound, verrors.VegaBackend_Resource_CatalogNotFound))

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.CatalogNotFound")
	})

	t.Run("allows duplicate name", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Create(gomock.Any(), gomock.Any()).
			Return(&interfaces.Resource{ID: "res-1", Name: "dataset"}, nil)

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"res-1"`)
	})
}

func Test_ResourceRestHandler_GetResources(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/resources/res-1,res-2"

	t.Run("gets resources by ids", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"res-1", "res-2"}, true).
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

	t.Run("adds dataset row count only to resource detail responses", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rowCount := int64(7)
		resource := &interfaces.Resource{
			ID:             "dataset-1",
			Name:           "dataset",
			Category:       interfaces.ResourceCategoryDataset,
			LocalIndexName: "dataset-index-1",
			RowCount:       &rowCount,
		}
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"dataset-1"}, true).Return([]*interfaces.Resource{resource}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/dataset-1", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"row_count":7`)
	})
	t.Run("normalizes empty, whitespace, and duplicate ids", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"res-1", "res-2"}, true).
			Return([]*interfaces.Resource{{ID: "res-1", Name: "one"}, {ID: "res-2", Name: "two"}}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/%20res-1%20,,res-2,res-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"res-1"`)
		assert.Contains(t, w.Body.String(), `"id":"res-2"`)
	})

	t.Run("rejects ids that normalize to empty", func(t *testing.T) {
		engine, _, _ := setupResourceHandlerTest(t)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/,,", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})

	t.Run("multi-id with a missing id 404s by default", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"res-1", "res-2"}, true).
			Return([]*interfaces.Resource{{ID: "res-1", Name: "one"}}, nil)

		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		// Default is strict all-or-nothing, same as the batch DELETE convention.
		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "id res-2 not found")
	})

	t.Run("ignore_missing skips missing ids in a multi-id batch", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"res-1", "res-2"}, true).
			Return([]*interfaces.Resource{{ID: "res-1", Name: "one"}}, nil)

		req := httptest.NewRequest(http.MethodGet, url+"?ignore_missing=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"res-1"`)
		assert.NotContains(t, w.Body.String(), `"id":"res-2"`)
		assert.NotContains(t, w.Body.String(), "not found")
	})

	t.Run("single missing id still returns 404", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"res-x"}, true).
			Return([]*interfaces.Resource{}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-x", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "id res-x not found")
	})

	t.Run("ignore_missing tolerates a missing single id", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"res-x"}, true).
			Return([]*interfaces.Resource{}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-x?ignore_missing=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.NotContains(t, w.Body.String(), "not found")
	})
}

func Test_ResourceRestHandler_UpdateResource(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/resources/res-1"
	body := `{"id":"res-1","catalog_id":"catalog-1","name":"dataset-new","category":"dataset","schema_definition":[{"name":"title","type":"string"}],"expected_update_time":1}`

	t.Run("uses path id when body id is omitted", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Update(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) error {
				assert.Equal(t, "res-1", req.ID)
				return nil
			})
		req := httptest.NewRequest(http.MethodPut, url,
			strings.NewReader(`{"catalog_id":"catalog-1","name":"dataset-new","category":"dataset","schema_definition":[{"name":"title","type":"string"}],"expected_update_time":1}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("rejects body id different from path", func(t *testing.T) {
		engine, _, _ := setupResourceHandlerTest(t)
		req := httptest.NewRequest(http.MethodPut, url,
			strings.NewReader(`{"id":"res-2","catalog_id":"catalog-1","name":"dataset-new","category":"dataset","schema_definition":[{"name":"title","type":"string"}],"expected_update_time":1}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `path id \"res-1\" != body id \"res-2\"`)
	})

	t.Run("updates resource", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Update(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) error {
				assert.Equal(t, "res-1", req.ID)
				assert.Equal(t, "dataset-new", req.Name)
				return nil
			})

		req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("rejects enabled change through put", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Update(gomock.Any(), gomock.Any()).Return(
			rest.NewHTTPError(context.Background(), http.StatusConflict,
				verrors.VegaBackend_Resource_EnabledFieldNotAllowed))

		req := httptest.NewRequest(http.MethodPut, url,
			strings.NewReader(`{"id":"res-1","catalog_id":"catalog-1","name":"dataset","category":"dataset","enabled":false,"schema_definition":[{"name":"title","type":"string"}],"expected_update_time":1}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.EnabledFieldNotAllowed")
	})

	t.Run("rejects missing expected update time", func(t *testing.T) {
		engine, _, _ := setupResourceHandlerTest(t)

		req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(`{"id":"res-1","catalog_id":"catalog-1","name":"dataset-new","category":"dataset","schema_definition":[{"name":"title","type":"string"}]}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "expected_update_time is required")
	})

	t.Run("rejects dataset ref property", func(t *testing.T) {
		engine, _, _ := setupResourceHandlerTest(t)

		body := `{"id":"res-1","catalog_id":"catalog-1","name":"dataset-new","category":"dataset","schema_definition":[{"name":"title_keyword","type":"string"},{"name":"title","type":"text","features":[{"name":"title.keyword","feature_type":"keyword","ref_property":"title_keyword"}]}]}`
		req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})

	t.Run("rejects missing category", func(t *testing.T) {
		engine, _, _ := setupResourceHandlerTest(t)

		body := `{"id":"res-1","catalog_id":"catalog-1","name":"dataset-new","schema_definition":[{"name":"title","type":"string"}]}`
		req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})

	t.Run("rejects category change", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Update(gomock.Any(), gomock.Any()).Return(
			rest.NewHTTPError(context.Background(), http.StatusBadRequest,
				verrors.VegaBackend_InvalidParameter_RequestBody))

		body := `{"id":"res-1","catalog_id":"catalog-1","name":"dataset-new","category":"table","schema_definition":[{"name":"title","type":"string"}],"expected_update_time":1}`
		req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})

	t.Run("allows duplicate renamed resource", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

		req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}

func Test_ResourceRestHandler_DeleteResources(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("normalizes duplicate ids before deleting resources", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().DeleteByIDs(gomock.Any(), []string{"res-1", "res-2"}, false).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1,res-2,res-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("ignores missing resources when requested", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().DeleteByIDs(gomock.Any(), []string{"res-1", "missing"}, true).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1,missing?ignore_missing=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}
