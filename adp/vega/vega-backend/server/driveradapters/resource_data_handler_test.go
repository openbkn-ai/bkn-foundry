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
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
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
	handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, ds, nil, nil, nil, rds)
	handler.RegisterPublic(engine)
	return engine, rs, ds, rds
}

func sampleDatasetResource() *interfaces.Resource {
	return &interfaces.Resource{
		ID:               "res-1",
		Name:             "dataset",
		Category:         interfaces.ResourceCategoryDataset,
		Status:           interfaces.ResourceStatusActive,
		SchemaDefinition: []*interfaces.Property{{Name: "id"}},
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

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"paging":{"limit":2},"need_total":true}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"entries"`)
		assert.Contains(t, w.Body.String(), `"total_count":1`)
	})

	t.Run("rejects negative offset before querying", func(t *testing.T) {
		engine, _, _, _ := setupResourceDataHandlerTest(t)
		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"paging":{"offset":-1,"limit":10}}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.InvalidParameter.Offset")
	})

	t.Run("preserves total count requested by cursor session", func(t *testing.T) {
		engine, rs, _, rds := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				assert.False(t, params.NeedTotal)
				return &interfaces.ResourceDataQueryResult{
					Entries:    []map[string]any{{"id": "doc-1"}},
					TotalCount: 2,
					NeedTotal:  true,
					Paging:     &interfaces.PagingResponse{},
				}, nil
			})

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"paging":{"cursor":"opaque-cursor"}}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"total_count":2`)
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

	t.Run("rejects document query when resource metadata is unavailable", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.SchemaDefinition = nil
		resource.LastDiscoverStatus = interfaces.DiscoverStatusError
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/doc-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.MetadataUnavailable")
	})

	t.Run("rejects document query when resource is missing with retained metadata", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.Status = interfaces.ResourceStatusStale
		resource.LastDiscoverStatus = interfaces.DiscoverStatusMissing
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/doc-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.MetadataUnavailable")
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

// Test_ResourceDataRestHandler_S2SInternalAccessMarker 锁定 /in/ 与外部端点的鉴权语义差异：
// 内网端点必须带 S2S 标记，否则内部基础设施数据集会按 per-account view_detail 校验而误拒 403；
// 外部端点必须不带，否则等于让普通用户绕过资源鉴权。
func Test_ResourceDataRestHandler_S2SInternalAccessMarker(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	// captureS2S 记录 GetByID 收到的 ctx 是否被标记为 S2S 内部访问。
	captureS2S := func(rs *vmock.MockResourceService, got *bool) {
		rs.EXPECT().GetByID(gomock.Any(), "res-1").
			DoAndReturn(func(ctx context.Context, _ string) (*interfaces.Resource, error) {
				*got = interfaces.IsS2SInternalAccess(ctx)
				return sampleDatasetResource(), nil
			})
	}

	internalCases := []struct {
		name       string
		method     string
		url        string
		body       string
		expectCode int
		expectCall func(ds *vmock.MockDatasetService)
	}{
		{
			name:       "DELETE /in/ documents by ids",
			method:     http.MethodDelete,
			url:        "/api/vega-backend/in/v1/resources/res-1/data/doc-1",
			expectCode: http.StatusNoContent,
			expectCall: func(ds *vmock.MockDatasetService) {
				ds.EXPECT().DeleteDocuments(gomock.Any(), "res-1", "doc-1").Return(nil)
			},
		},
		{
			name:       "PUT /in/ documents",
			method:     http.MethodPut,
			url:        "/api/vega-backend/in/v1/resources/res-1/data",
			body:       `[{"id":"doc-1"}]`,
			expectCode: http.StatusOK,
			expectCall: func(ds *vmock.MockDatasetService) {
				ds.EXPECT().UpsertDocuments(gomock.Any(), "res-1", gomock.Any()).Return([]string{"doc-1"}, nil)
			},
		},
		{
			name:       "GET /in/ single document",
			method:     http.MethodGet,
			url:        "/api/vega-backend/in/v1/resources/res-1/data/doc-1",
			expectCode: http.StatusOK,
			expectCall: func(ds *vmock.MockDatasetService) {
				ds.EXPECT().GetDocument(gomock.Any(), "res-1", "doc-1").Return(map[string]any{"id": "doc-1"}, nil)
			},
		},
		{
			name:       "PUT /in/ single document",
			method:     http.MethodPut,
			url:        "/api/vega-backend/in/v1/resources/res-1/data/doc-1",
			body:       `{"title":"one"}`,
			expectCode: http.StatusOK,
			expectCall: func(ds *vmock.MockDatasetService) {
				ds.EXPECT().UpsertDocuments(gomock.Any(), "res-1", gomock.Any()).Return([]string{"doc-1"}, nil)
			},
		},
	}

	for _, tc := range internalCases {
		t.Run(tc.name+" marks S2S internal access", func(t *testing.T) {
			engine, rs, ds, _ := setupResourceDataHandlerTest(t)

			var gotS2S bool
			captureS2S(rs, &gotS2S)
			tc.expectCall(ds)

			var bodyReader *strings.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			} else {
				bodyReader = strings.NewReader("")
			}
			req := httptest.NewRequest(tc.method, tc.url, bodyReader)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, tc.expectCode, w.Result().StatusCode)
			assert.True(t, gotS2S, "internal /in/ endpoint must mark the context as S2S internal access")
		})
	}

	// 外部端点走 OAuth 校验，无法经路由直达；直接调用内部方法验证 s2sInternal=false 的语义。
	t.Run("external delete does not mark S2S internal access", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)

		var gotS2S bool
		captureS2S(rs, &gotS2S)
		ds.EXPECT().DeleteDocuments(gomock.Any(), "res-1", "doc-1").Return(nil)

		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, ds, nil, nil, nil, nil)
		engine.DELETE("/ex/resources/:id/data/:doc_ids", func(c *gin.Context) {
			handler.deleteResourceData(c, hydra.Visitor{ID: "user-1"}, false)
		})

		req := httptest.NewRequest(http.MethodDelete, "/ex/resources/res-1/data/doc-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
		assert.False(t, gotS2S, "external endpoint must keep per-account authorization")
	})
}
