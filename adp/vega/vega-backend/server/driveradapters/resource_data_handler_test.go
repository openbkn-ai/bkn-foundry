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

func setupResourceDataHandlerTest(
	t *testing.T,
) (*gin.Engine, *vmock.MockResourceService, *vmock.MockDatasetService, *vmock.MockResourceDataService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	rs := vmock.NewMockResourceService(mockCtrl)
	ds := vmock.NewMockDatasetService(mockCtrl)
	rds := vmock.NewMockResourceDataService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, ds, nil, nil, nil, rds, nil)
	handler.RegisterPublic(engine)
	return engine, rs, ds, rds
}

func sampleDatasetResource() *interfaces.Resource {
	return &interfaces.Resource{
		ID:       "res-1",
		Name:     "dataset",
		Category: interfaces.ResourceCategoryDataset,
		Status:   interfaces.ResourceStatusActive,
	}
}

func Test_ResourceDataRestHandler_PostResourceData(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/resources/res-1/data"

	t.Run("rejects unsupported override method", func(t *testing.T) {
		engine, _, _, _ := setupResourceDataHandlerTest(t)

		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodPatch)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.InvalidParameter.OverrideMethod")
	})
}

func Test_ResourceDataRestHandler_QueryResourceData(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("queries resource data", func(t *testing.T) {
		engine, rs, _, rds := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				assert.True(t, params.NeedTotal)
				assert.Equal(t, 0, params.Offset)
				assert.Equal(t, 2, params.Limit)
				return &interfaces.ResourceDataQueryResult{
					Entries:    []map[string]any{{"id": "doc-1"}},
					TotalCount: 1,
					Paging:     &interfaces.PagingResponse{},
				}, nil
			})

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"paging":{"size":2},"need_total":true}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"entries"`)
		assert.Contains(t, w.Body.String(), `"total_count":1`)
	})

	t.Run("returns not found for missing resource", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/missing/data", strings.NewReader(`{}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.NotFound")
	})
}

func Test_ResourceDataRestHandler_CreateResourceData(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("creates documents", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().CreateDocuments(gomock.Any(), "res-1", []map[string]any{{"title": "one"}}).
			Return([]string{"doc-1"}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`[{"title":"one"}]`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodPost)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"ids":["doc-1"]`)
	})
}

func Test_ResourceDataRestHandler_DeleteResourceDataByQuery(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("deletes documents by query", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		ds.EXPECT().DeleteDocumentsByQuery(gomock.Any(), "res-1", resource, gomock.Any()).Return(nil)

		body := `{"filter_condition":{"name":"title","operation":"==","value":"old"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(body))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodDelete)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("rejects empty delete filter", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodDelete)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "filter is required")
	})
}

func Test_ResourceDataRestHandler_PutResourceData(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("upserts documents", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().UpsertDocuments(gomock.Any(), "res-1", []map[string]any{{"id": "doc-1", "title": "one"}}).
			Return([]string{"doc-1"}, nil)

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`[{"id":"doc-1","title":"one"}]`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"ids":["doc-1"]`)
	})

	t.Run("rejects document without id", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`[{"title":"one"}]`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "every document must carry")
	})
}

func Test_ResourceDataRestHandler_GetResourceDataDoc(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("gets document", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().GetDocument(gomock.Any(), "res-1", "doc-1").Return(map[string]any{"id": "doc-1"}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/doc-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"doc-1"`)
	})

	t.Run("returns not found for nil document", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().GetDocument(gomock.Any(), "res-1", "missing").Return(nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/missing", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "document missing not found")
	})
}

func Test_ResourceDataRestHandler_PutResourceDataDoc(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("upserts document with path id", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().UpsertDocuments(gomock.Any(), "res-1", []map[string]any{{"id": "doc-1", "title": "one"}}).
			Return([]string{"doc-1"}, nil)

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data/doc-1", strings.NewReader(`{"title":"one"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"doc-1"`)
	})
}

func Test_ResourceDataRestHandler_DeleteResourceData(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("deletes documents by ids", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().DeleteDocuments(gomock.Any(), "res-1", "doc-1,doc-2").Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1/data/doc-1,doc-2", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}

func Test_ResourceDataRestHandler_RequireDatasetResource(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("rejects non dataset resource", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").
			Return(&interfaces.Resource{ID: "res-1", Category: interfaces.ResourceCategoryTable}, nil)

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`[{"id":"doc-1"}]`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "operation requires resource category=dataset")
	})
}
