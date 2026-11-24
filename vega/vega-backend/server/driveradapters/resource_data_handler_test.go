// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
)

func setupResourceDataHandlerTest(
	t *testing.T,
) (*gin.Engine, *vmock.MockResourceService, *vmock.MockDatasetService, *vmock.MockResourceDataService) {
	t.Helper()
	// 取数要 query_data（#571）；这些用例验的是查询本身，统一放行。
	return setupResourceDataHandlerTestWithPermission(t, nil)
}

// setupResourceDataHandlerTestWithPermission builds the same harness with the
// query_data check answering permErr, so a test can drive the endpoint as a
// caller who may see the table but not read its rows.
func setupResourceDataHandlerTestWithPermission(
	t *testing.T, permErr error,
) (*gin.Engine, *vmock.MockResourceService, *vmock.MockDatasetService, *vmock.MockResourceDataService) {
	t.Helper()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	rs := vmock.NewMockResourceService(mockCtrl)
	rs.EXPECT().CheckResourcePermission(gomock.Any(), gomock.Any(), interfaces.OPERATION_TYPE_QUERY_DATA).
		Return(permErr).AnyTimes()
	rs.EXPECT().CheckResourcePermission(gomock.Any(), gomock.Any(), interfaces.OPERATION_TYPE_DATA_WRITE).
		Return(nil).AnyTimes()
	// Keep the existing endpoint fixtures focused on response behavior while
	// the production path loads the already-authorized Resource internally.
	rs.EXPECT().InternalGetByID(gomock.Any(), gomock.Nil(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *sql.Tx, id string) (*interfaces.Resource, error) {
			return rs.GetByID(ctx, id)
		}).AnyTimes()
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
		Enabled:          true,
		Status:           interfaces.ResourceStatusActive,
		LocalIndexName:   "vega-dataset-res-1",
		SchemaDefinition: []*interfaces.Property{{Name: "id"}},
	}
}

func Test_ResourceDataRestHandler_PostResourceData(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/resources/res-1/data"

	t.Run("trusted internal caller owns quota without changing access account", func(t *testing.T) {
		engine, rs, _, rds := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _ *interfaces.Resource, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				account := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
				assert.Equal(t, interfaces.BuiltinAdminID, account.ID)
				assert.Equal(t, "caller-1", interfaces.CursorQuotaOwner(ctx))
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{}}, nil
			})
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, nil, nil, nil, nil, rds)
		engine.POST("/test/resources/:id/data", func(c *gin.Context) {
			handler.postResourceData(c, hydra.Visitor{ID: interfaces.BuiltinAdminID}, true)
		})
		req := httptest.NewRequest(http.MethodPost, "/test/resources/res-1/data", strings.NewReader(`{"paging":{"limit":1}}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set(interfaces.HTTPHeaderBKNCallerID, "caller-1")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("trusted internal caller does not require an admin access account", func(t *testing.T) {
		engine, rs, _, rds := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _ *interfaces.Resource, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				account := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
				assert.Equal(t, "service-account", account.ID)
				assert.Equal(t, "caller-1", interfaces.CursorQuotaOwner(ctx))
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{}}, nil
			})
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, nil, nil, nil, nil, rds)
		engine.POST("/test/resources/:id/data", func(c *gin.Context) {
			handler.postResourceData(c, hydra.Visitor{ID: "service-account"}, true)
		})
		req := httptest.NewRequest(http.MethodPost, "/test/resources/res-1/data", strings.NewReader(`{"paging":{"limit":1}}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set(interfaces.HTTPHeaderBKNCallerID, "caller-1")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})

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
				assert.Equal(t, 0, params.Paging.Offset)
				assert.Equal(t, 2, params.Paging.Limit)
				return &interfaces.ResourceDataQueryResult{
					Entries:     []map[string]any{{"id": "doc-1"}},
					TotalCount:  1,
					Paging:      &interfaces.PagingResponse{},
					QuerySource: interfaces.ResourceQuerySourceLocalIndex,
				}, nil
			})

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"paging":{"limit":2},"need_total":true}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"entries"`)
		assert.Contains(t, w.Body.String(), `"query_source":"local_index"`)
		assert.Contains(t, w.Body.String(), `"total_count":1`)
	})

	t.Run("rejects negative offset before querying", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"paging":{"offset":-1,"limit":10}}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.InvalidParameter.Offset")
	})

	t.Run("rejects group by fields outside the resource schema", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.Category = interfaces.ResourceCategoryTable
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
			strings.NewReader(`{"group_by":[{"property":"createdAt","calendar_interval":"day"}]}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.InvalidParameter.GroupBy")
		assert.Contains(t, w.Body.String(), "createdAt")
	})

	t.Run("rejects index subfields absent from schema definition", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.Category = interfaces.ResourceCategoryIndex
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
			strings.NewReader(`{"group_by":[{"property":"title.keyword"}]}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.InvalidParameter.GroupBy")
	})

	t.Run("rejects local index subfields outside the resource schema", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.Category = interfaces.ResourceCategoryTable
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
		resource.LocalIndexName = "resource-index"
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
			strings.NewReader(`{"group_by":[{"property":"title.keyword"}]}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.InvalidParameter.GroupBy")
	})

	t.Run("rejects group by fields addressed by original name", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.Category = interfaces.ResourceCategoryTable
		resource.SchemaDefinition = []*interfaces.Property{{Name: "createdAt", OriginalName: "created_at"}}
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
			strings.NewReader(`{"group_by":[{"property":"created_at"}]}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.InvalidParameter.GroupBy")
	})

	t.Run("rejects binary group by fields", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.SchemaDefinition = []*interfaces.Property{{Name: "payload", Type: interfaces.DataType_Binary}}
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
			strings.NewReader(`{"group_by":[{"property":"payload"}]}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "binary field \\\"payload\\\" cannot be used in group_by")
	})

	t.Run("rejects other group by fields", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.SchemaDefinition = []*interfaces.Property{{Name: "metadata", Type: interfaces.DataType_Other}}
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
			strings.NewReader(`{"group_by":[{"property":"metadata"}]}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "other field \\\"metadata\\\" cannot be used in group_by")
	})

	t.Run("rejects binary output fields in aggregation queries", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.SchemaDefinition = []*interfaces.Property{
			{Name: "score", Type: interfaces.DataType_Integer},
			{Name: "payload", Type: interfaces.DataType_Binary},
		}
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
			strings.NewReader(`{"aggregation":{"property":"score","aggr":"sum"},"output_fields":["payload"]}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "Binary field \\\"payload\\\" cannot be requested by an aggregation query")
	})

	t.Run("rejects other output fields in aggregation queries", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.SchemaDefinition = []*interfaces.Property{
			{Name: "score", Type: interfaces.DataType_Integer},
			{Name: "metadata", Type: interfaces.DataType_Other},
		}
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
			strings.NewReader(`{"aggregation":{"property":"score","aggr":"sum"},"output_fields":["metadata"]}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "Other field \\\"metadata\\\" cannot be requested by an aggregation query")
	})

	t.Run("accepts group fields and the aggregate alias as output fields", func(t *testing.T) {
		engine, rs, _, rds := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.SchemaDefinition = []*interfaces.Property{
			{Name: "department", Type: interfaces.DataType_String},
			{Name: "amount", Type: interfaces.DataType_Integer},
		}
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				assert.Equal(t, []string{"department", "total"}, params.OutputFields)
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{}}, nil
			})

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
			strings.NewReader(`{"group_by":[{"property":"department"}],"aggregation":{"property":"amount","aggr":"sum","alias":"total"},"output_fields":["department","total"]}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	for _, tt := range []struct {
		name      string
		fieldType string
		wantError string
	}{
		{
			name:      "binary",
			fieldType: interfaces.DataType_Binary,
			wantError: "Binary field \\\"result\\\" cannot be requested by an aggregation query",
		},
		{
			name:      "other",
			fieldType: interfaces.DataType_Other,
			wantError: "Other field \\\"result\\\" cannot be requested by an aggregation query",
		},
	} {
		t.Run("rejects aggregate aliases shadowing "+tt.name+" fields", func(t *testing.T) {
			engine, rs, _, _ := setupResourceDataHandlerTest(t)
			resource := sampleDatasetResource()
			resource.SchemaDefinition = []*interfaces.Property{
				{Name: "score", Type: interfaces.DataType_Integer},
				{Name: "result", Type: tt.fieldType},
			}
			rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

			req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
				strings.NewReader(`{"aggregation":{"property":"score","aggr":"count","alias":"result"},"output_fields":["result"]}`))
			req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
			assert.Contains(t, w.Body.String(), tt.wantError)
		})
	}

	t.Run("rejects non-grouped plain output fields in aggregation queries", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.SchemaDefinition = []*interfaces.Property{
			{Name: "department", Type: interfaces.DataType_String},
			{Name: "region", Type: interfaces.DataType_String},
			{Name: "amount", Type: interfaces.DataType_Integer},
		}
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data",
			strings.NewReader(`{"group_by":[{"property":"department"}],"aggregation":{"property":"amount","aggr":"sum","alias":"total"},"output_fields":["region"]}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "must be a group_by field or aggregate alias")
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

	t.Run("API paging and response contract", func(t *testing.T) {
		restore := setGinMode()
		defer restore()
		engine, rs, _, rds := setupResourceDataHandlerTest(t)
		resource := &interfaces.Resource{ID: "res-1", Category: interfaces.ResourceCategoryAPI, APIEffectType: interfaces.APIEffectRead, Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id", OriginalName: "id", Type: interfaces.DataType_Integer}}}
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).DoAndReturn(func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			assert.Equal(t, interfaces.PagingRequest{Mode: interfaces.PagingModeSingle, Limit: 2}, params.Paging)
			assert.True(t, params.NeedTotal)
			assert.Equal(t, new(interfaces.BinaryModeMetadata), params.BinaryMode)
			assert.Equal(t, interfaces.Format_Original, params.Format)
			return &interfaces.ResourceDataQueryResult{QuerySource: interfaces.ResourceQuerySourceSource, Entries: []map[string]any{}, TotalCount: 8, Paging: &interfaces.PagingResponse{}}, nil
		})
		request := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"paging":{"mode":"single","offset":0,"limit":2},"need_total":true}`))
		request.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code)
		assert.JSONEq(t, `{"query_source":"source","is_approximate":false,"entries":[],"total_count":8,"paging":{"next_cursor":null,"expires_at_sec":null}}`, recorder.Body.String())
	})
}

func TestResourceDataOperationsDoNotRequireViewDetail(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	for _, tc := range []struct {
		name          string
		permissionErr error
		deleted       bool
		wantStatus    int
		wantCode      string
	}{
		{name: "query uses query_data", wantStatus: http.StatusOK},
		{name: "deleted resource returns not found", permissionErr: httperrors.NewHTTPError(context.Background(), http.StatusNotFound, verrors.VegaBackend_Resource_NotFound), wantStatus: http.StatusNotFound, wantCode: verrors.VegaBackend_Resource_NotFound},
		{name: "existing resource without query_data remains forbidden", permissionErr: httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden), wantStatus: http.StatusForbidden, wantCode: httperrors.PublicError_Forbidden},
		{name: "resource deleted after authorization returns not found", deleted: true, wantStatus: http.StatusNotFound, wantCode: verrors.VegaBackend_Resource_NotFound},
		{name: "resource read failure remains internal error", permissionErr: httperrors.NewHTTPError(context.Background(), http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed), wantStatus: http.StatusInternalServerError, wantCode: verrors.VegaBackend_Resource_InternalError_GetFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := gin.New()
			ctrl := gomock.NewController(t)
			rs := vmock.NewMockResourceService(ctrl)
			rds := vmock.NewMockResourceDataService(ctrl)
			resource := sampleDatasetResource()
			rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1", interfaces.OPERATION_TYPE_QUERY_DATA).Return(tc.permissionErr)
			if tc.permissionErr == nil {
				if tc.deleted {
					resource = nil
				}
				rs.EXPECT().InternalGetByID(gomock.Any(), gomock.Nil(), "res-1").Return(resource, nil)
			} else {
				rs.EXPECT().InternalGetByID(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			}
			if tc.wantStatus == http.StatusOK {
				rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).
					Return(&interfaces.ResourceDataQueryResult{Entries: []map[string]any{}}, nil)
			} else {
				rds.EXPECT().QueryWithPaging(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			}
			handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, nil, nil, nil, nil, rds)
			handler.RegisterPublic(engine)

			req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"paging":{"mode":"single","offset":0,"limit":2},"need_total":true}`))
			req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantCode != "" {
				var response struct {
					ErrorCode string `json:"error_code"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
				assert.Equal(t, tc.wantCode, response.ErrorCode)
				assert.NotContains(t, w.Body.String(), `"entries"`)
			}
		})
	}

	t.Run("document create uses data_write", func(t *testing.T) {
		engine := gin.New()
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ds := vmock.NewMockDatasetService(ctrl)
		resource := sampleDatasetResource()
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1", interfaces.OPERATION_TYPE_DATA_WRITE).Return(nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), gomock.Nil(), "res-1").Return(resource, nil)
		ds.EXPECT().CreateDocument(gomock.Any(), resource, gomock.Any()).Return("doc-1", nil)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, ds, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"name":"row"}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodPost)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Code)
	})
}

func Test_ResourceDataRestHandler_CreateResourceData(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("creates one document", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		ds.EXPECT().CreateDocument(gomock.Any(), resource, map[string]any{"title": "one"}).Return("doc-1", nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"title":"one"}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodPost)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"doc-1"`)
	})

	t.Run("returns an internal error when materialization fails", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		ds.EXPECT().CreateDocument(gomock.Any(), resource, map[string]any{"title": "one"}).
			Return("", errors.New("model registry unavailable"))

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"title":"one"}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodPost)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
	})

}

func Test_ResourceDataRestHandler_DeleteResourceDataByQuery(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("deletes documents by query", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		ds.EXPECT().DeleteDocumentsByQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.DeleteByQueryResult{Deleted: 3, Failures: []interfaces.DocumentWriteResult{}}, nil)

		body := `{"filter_condition":{"name":"title","operation":"==","value":"old"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data?return_result=true", strings.NewReader(body))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodDelete)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.JSONEq(t, `{"deleted":3,"failures":[],"version_conflicts":0,"timed_out":false}`, w.Body.String())
	})

	t.Run("rejects a selector that does not decode to a filter condition", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"filter_condition":{"kind":"remove"}}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodDelete)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "filter condition with an operation")
		// No delete is allowed when a caller's selector cannot be compiled.
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

func Test_ResourceDataRestHandler_GetResourceDataDoc(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("gets requested documents in request order", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().GetDocuments(gomock.Any(), gomock.Any(), []string{"doc-1", "doc-2"}, false).
			Return([]map[string]any{{"id": "doc-1"}, {"id": "doc-2"}}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/doc-1,doc-2", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.JSONEq(t, `{"entries":[{"id":"doc-1"},{"id":"doc-2"}]}`, w.Body.String())
	})

	t.Run("rejects document read without query_data", func(t *testing.T) {
		// The document endpoint hands back row contents, so view_detail on the
		// table is not enough on its own: it only says the caller may see the
		// structure. Without this the split introduced by #801 decides nothing
		// on this route even though the paged query already honours it.
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().GetDocuments(gomock.Any(), gomock.Any(), []string{"doc-1"}, false).
			Return(nil, httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden).
				WithErrorDetails("Access denied: insufficient permissions for[query_data]"))

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/doc-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusForbidden, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "query_data")
	})

	t.Run("returns not found for nil document", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().GetDocuments(gomock.Any(), gomock.Any(), []string{"missing"}, false).
			Return(nil, httperrors.NewHTTPError(context.Background(), http.StatusNotFound, verrors.VegaBackend_Resource_NotFound).
				WithErrorDetails("document missing not found"))

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/missing", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "document missing not found")
	})

	t.Run("skips missing documents when ignore_missing is true", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().GetDocuments(gomock.Any(), gomock.Any(), []string{"doc-1", "missing"}, true).
			Return([]map[string]any{{"id": "doc-1"}, nil}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/doc-1,missing?ignore_missing=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.JSONEq(t, `{"entries":[{"id":"doc-1"}]}`, w.Body.String())
	})

	t.Run("normalizes empty, whitespace, and duplicate document ids", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().GetDocuments(gomock.Any(), gomock.Any(), []string{"doc-1", "doc-2"}, false).
			Return([]map[string]any{{"id": "doc-1"}, {"id": "doc-2"}}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/%20doc-1%20,,doc-2,doc-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.JSONEq(t, `{"entries":[{"id":"doc-1"},{"id":"doc-2"}]}`, w.Body.String())
	})

	t.Run("rejects document ids that normalize to empty", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/,,", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
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

	t.Run("gives stale lifecycle status precedence over missing discovery status", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		resource.Status = interfaces.ResourceStatusStale
		resource.LastDiscoverStatus = interfaces.DiscoverStatusMissing
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/resources/res-1/data/doc-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.NotQueryable")
	})
}

func Test_ResourceDataRestHandler_PutResourceDataDoc(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("upserts document with path id", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		ds.EXPECT().ReplaceDocument(gomock.Any(), resource, "doc-1", map[string]any{"_id": "doc-1", "title": "one"}).Return(nil)

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data/doc-1", strings.NewReader(`{"title":"one"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), `"id":"doc-1"`)
	})

	t.Run("returns an internal error when replacement materialization fails", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		ds.EXPECT().ReplaceDocument(gomock.Any(), resource, "doc-1", map[string]any{"_id": "doc-1", "title": "one"}).
			Return(errors.New("model registry unavailable"))

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data/doc-1", strings.NewReader(`{"title":"one"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
	})

	t.Run("accepts a comma in a single document id", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		ds.EXPECT().ReplaceDocument(gomock.Any(), resource, "doc-1,doc-2", map[string]any{"_id": "doc-1,doc-2"}).Return(nil)

		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data/doc-1,doc-2", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
	})
}

func Test_ResourceDataRestHandler_DeleteResourceData(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("deletes documents by ids after strict existence preflight", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().DeleteDocuments(gomock.Any(), gomock.Any(), []string{"doc-1", "doc-2"}, false).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1/data/doc-1,doc-2,doc-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("does not delete when a requested document is missing", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().DeleteDocuments(gomock.Any(), gomock.Any(), []string{"doc-1", "missing"}, false).
			Return(httperrors.NewHTTPError(context.Background(), http.StatusNotFound, verrors.VegaBackend_Resource_NotFound).
				WithErrorDetails("document missing not found"))

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1/data/doc-1,missing", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Result().StatusCode)
	})

	t.Run("skips missing documents for tolerant delete", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().DeleteDocuments(gomock.Any(), gomock.Any(), []string{"doc-1", "missing"}, true).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1/data/doc-1,missing?ignore_missing=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("returns service authorization errors", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		ds.EXPECT().DeleteDocuments(gomock.Any(), gomock.Any(), []string{"missing"}, false).
			Return(httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden))

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1/data/missing", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusForbidden, w.Result().StatusCode)
	})
}

func Test_ResourceDataRestHandler_RequireDatasetResource(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("rejects non dataset resource", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").
			Return(&interfaces.Resource{ID: "res-1", Category: interfaces.ResourceCategoryTable}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"id":"doc-1"}`))
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodPost)
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
				ds.EXPECT().DeleteDocuments(gomock.Any(), gomock.Any(), []string{"doc-1"}, false).Return(nil)
			},
		},
		{
			name:       "GET /in/ single document",
			method:     http.MethodGet,
			url:        "/api/vega-backend/in/v1/resources/res-1/data/doc-1",
			expectCode: http.StatusOK,
			expectCall: func(ds *vmock.MockDatasetService) {
				ds.EXPECT().GetDocuments(gomock.Any(), gomock.Any(), []string{"doc-1"}, false).Return([]map[string]any{{"id": "doc-1"}}, nil)
			},
		},
		{
			name:       "PUT /in/ single document",
			method:     http.MethodPut,
			url:        "/api/vega-backend/in/v1/resources/res-1/data/doc-1",
			body:       `{"title":"one"}`,
			expectCode: http.StatusOK,
			expectCall: func(ds *vmock.MockDatasetService) {
				ds.EXPECT().ReplaceDocument(gomock.Any(), gomock.Any(), "doc-1", gomock.Any()).Return(nil)
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
		ds.EXPECT().DeleteDocuments(gomock.Any(), gomock.Any(), []string{"doc-1"}, false).Return(nil)

		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, ds, nil, nil, nil, nil)
		engine.DELETE("/ex/resources/:id/data/:docid", func(c *gin.Context) {
			handler.deleteResourceData(c, hydra.Visitor{ID: "user-1"}, false)
		})

		req := httptest.NewRequest(http.MethodDelete, "/ex/resources/res-1/data/doc-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
		assert.False(t, gotS2S, "external endpoint must keep per-account authorization")
	})
}

func TestResourceDataRestHandlerPutResourceData(t *testing.T) {
	t.Run("collection PUT authorizes once and passes loaded resource while preserving numbers", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ds := vmock.NewMockDatasetService(ctrl)
		engine := gin.New()
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, ds, nil, nil, nil, nil)
		handler.RegisterPublic(engine)
		resource := sampleDatasetResource()
		gomock.InOrder(
			rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1", interfaces.OPERATION_TYPE_DATA_WRITE).Return(nil).Times(1),
			rs.EXPECT().InternalGetByID(gomock.Any(), gomock.Nil(), "res-1").Return(resource, nil).Times(1),
		)
		ds.EXPECT().BulkReplaceDocuments(gomock.Any(), resource, gomock.Any()).DoAndReturn(func(ctx context.Context, got *interfaces.Resource, entries []map[string]any) ([]interfaces.DocumentWriteResult, error) {
			require.Same(t, resource, got)
			require.Len(t, entries, 1)
			assert.Equal(t, "doc", entries[0]["_id"])
			assert.Equal(t, "9007199254740993", fmt.Sprint(entries[0]["value"]))
			account := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
			assert.Equal(t, interfaces.BuiltinAdminID, account.ID)
			return []interfaces.DocumentWriteResult{{ID: "doc", Status: 201}}, nil
		})
		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"entries":[{"_id":"doc","value":9007199254740993}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, interfaces.BuiltinAdminID)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code)
		assert.JSONEq(t, `{"entries":[{"id":"doc","status":201,"retryable":false}]}`, w.Body.String())
	})

	for _, count := range []int{0, 1, interfaces.MaxDatasetBulkDocuments, interfaces.MaxDatasetBulkDocuments + 1} {
		t.Run(fmt.Sprintf("document count %d", count), func(t *testing.T) {
			engine, rs, ds, _ := setupResourceDataHandlerTest(t)
			resource := sampleDatasetResource()
			rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
			valid := count > 0 && count <= interfaces.MaxDatasetBulkDocuments
			if valid {
				ds.EXPECT().BulkReplaceDocuments(gomock.Any(), resource, gomock.Any()).DoAndReturn(func(_ context.Context, _ *interfaces.Resource, entries []map[string]any) ([]interfaces.DocumentWriteResult, error) {
					assert.Len(t, entries, count)
					return []interfaces.DocumentWriteResult{}, nil
				})
			}
			var body strings.Builder
			body.WriteString(`{"entries":[`)
			for i := 0; i < count; i++ {
				if i > 0 {
					body.WriteByte(',')
				}
				_, err := fmt.Fprintf(&body, `{"_id":"doc-%d"}`, i)
				require.NoError(t, err)
			}
			body.WriteString(`]}`)
			req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(body.String()))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if valid {
				assert.Equal(t, 200, w.Code)
			} else {
				assert.Equal(t, 400, w.Code)
				assert.Contains(t, w.Body.String(), verrors.VegaBackend_InvalidParameter_RequestBody)
			}
		})
	}

	t.Run("oversized body is rejected before invoking service", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		body := `{"entries":[{"_id":"doc"}]}` + strings.Repeat(" ", interfaces.MaxDatasetBulkBodyBytes)
		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(body))
		req.ContentLength = -1
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		assert.Equal(t, 400, w.Code)
		assert.Contains(t, w.Body.String(), verrors.VegaBackend_InvalidParameter_RequestBody)
	})
	t.Run("malformed body is rejected", func(t *testing.T) {
		engine, rs, _, _ := setupResourceDataHandlerTest(t)
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(sampleDatasetResource(), nil)
		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"entries":`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		assert.Equal(t, 400, w.Code)
	})
	t.Run("service denial is preserved", func(t *testing.T) {
		engine, rs, ds, _ := setupResourceDataHandlerTest(t)
		resource := sampleDatasetResource()
		rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
		ds.EXPECT().BulkReplaceDocuments(gomock.Any(), resource, gomock.Any()).Return(nil, httperrors.NewHTTPError(context.Background(), 403, httperrors.PublicError_Forbidden))
		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"entries":[{"_id":"doc"}]}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		assert.Equal(t, 403, w.Code)
	})

	t.Run("permission denial prevents resource load and service call", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ds := vmock.NewMockDatasetService(ctrl)
		engine := gin.New()
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, nil, ds, nil, nil, nil, nil)
		handler.RegisterPublic(engine)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1", interfaces.OPERATION_TYPE_DATA_WRITE).Return(httperrors.NewHTTPError(context.Background(), 403, httperrors.PublicError_Forbidden))
		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"entries":`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		assert.Equal(t, 403, w.Code)
	})
	for _, tc := range []struct {
		name     string
		resource *interfaces.Resource
		status   int
	}{
		{"missing resource", nil, 404}, {"wrong resource category", &interfaces.Resource{Category: interfaces.ResourceCategoryTable}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, rs, _, _ := setupResourceDataHandlerTest(t)
			rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(tc.resource, nil)
			req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"entries":`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			assert.Equal(t, tc.status, w.Code)
		})
	}

}

func TestResourceDataRestHandlerDeleteByQueryProgress(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			engine, rs, ds, _ := setupResourceDataHandlerTest(t)
			resource := sampleDatasetResource()
			rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil)
			progress := &interfaces.DeleteByQueryResult{Deleted: 2, TimedOut: partial, Failures: []interfaces.DocumentWriteResult{}}
			var failure error
			if partial {
				failure = httperrors.NewHTTPError(context.Background(), 500, verrors.VegaBackend_Dataset_DeleteIncomplete).WithErrorDetails(progress)
			}
			ds.EXPECT().DeleteDocumentsByQuery(gomock.Any(), resource, gomock.Any()).Return(progress, failure)
			req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/res-1/data", strings.NewReader(`{"filter_condition":{"name":"title","operation":"==","value":"old"}}`))
			req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodDelete)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if partial {
				assert.Equal(t, 500, w.Code)
				assert.Contains(t, w.Body.String(), `"deleted":2`)
				assert.Contains(t, w.Body.String(), verrors.VegaBackend_Dataset_DeleteIncomplete)
			} else {
				assert.Equal(t, 204, w.Code)
				assert.Empty(t, w.Body.String())
			}
		})
	}
}

func TestRestHandlerAPIResourceDataInput(t *testing.T) {
	restore := setGinMode()
	defer restore()
	cases := []struct {
		name, body string
		paging     interfaces.PagingRequest
		needTotal  bool
		invalid    bool
		binaryMode *string
	}{
		{name: "default", body: `{}`},
		{name: "null body", body: `null`},
		{name: "large API limit", body: `{"paging":{"limit":10001}}`, paging: interfaces.PagingRequest{Limit: 10001}},
		{name: "cursor ignores caller total", body: `{"paging":{"cursor":"opaque"},"need_total":true}`, paging: interfaces.PagingRequest{Cursor: "opaque"}, invalid: true},
		{name: "empty paging", body: `{"paging":{}}`},
		{name: "explicit single", body: `{"paging":{"mode":"single"}}`},
		{name: "empty mode", body: `{"paging":{"mode":""}}`},
		{name: "empty cursor", body: `{"paging":{"cursor":""}}`},
		{name: "explicit offset zero", body: `{"paging":{"offset":0}}`},
		{name: "offset and limit", body: `{"paging":{"offset":2,"limit":2}}`, paging: interfaces.PagingRequest{Offset: 2, Limit: 2}},
		{name: "limit only", body: `{"paging":{"limit":2}}`, paging: interfaces.PagingRequest{Limit: 2}},
		{name: "cursor initial default limit", body: `{"paging":{"mode":"cursor"}}`, paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor}, invalid: true},
		{name: "cursor initial", body: `{"paging":{"mode":"cursor","limit":2,"keep_alive_sec":60}}`, paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2, KeepAliveSec: 60}, invalid: true},
		{name: "cursor default keep alive", body: `{"paging":{"mode":"cursor","keep_alive_sec":0}}`, paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor}, invalid: true},
		{name: "single ignores keep alive", body: `{"paging":{"keep_alive_sec":1}}`, paging: interfaces.PagingRequest{KeepAliveSec: 1}},
		{name: "cursor continuation", body: `{"paging":{"cursor":"opaque-cursor"}}`, paging: interfaces.PagingRequest{Cursor: "opaque-cursor"}, invalid: true},
		{name: "need total", body: `{"need_total":true}`, needTotal: true},
		{name: "false need total", body: `{"need_total":false}`},
		{name: "null need total", body: `{"need_total":null}`},
		{name: "string need total", body: `{"need_total":"true"}`, invalid: true},
		{name: "null paging", body: `{"paging":null}`},
		{name: "null mode", body: `{"paging":{"mode":null}}`},
		{name: "null cursor", body: `{"paging":{"cursor":null}}`},
		{name: "null keep alive", body: `{"paging":{"keep_alive_sec":null}}`},
		{name: "null page", body: `{"paging":{"page":null}}`},
		{name: "null offset", body: `{"paging":{"offset":null}}`},
		{name: "null limit", body: `{"paging":{"limit":null}}`},
		{name: "zero limit", body: `{"paging":{"limit":0}}`},
		{name: "negative limit", body: `{"paging":{"limit":-1}}`, invalid: true},
		{name: "unknown page ignored", body: `{"paging":{"page":0}}`},
		{name: "negative offset", body: `{"paging":{"offset":-1}}`, invalid: true},
		{name: "fractional offset", body: `{"paging":{"offset":0.5}}`, invalid: true},
		{name: "fractional limit", body: `{"paging":{"limit":1.5}}`, invalid: true},
		{name: "fractional keep alive", body: `{"paging":{"keep_alive_sec":60.5}}`, invalid: true},
		{name: "overflow offset", body: `{"paging":{"offset":999999999999999999999999}}`, invalid: true},
		{name: "string offset", body: `{"paging":{"offset":"0"}}`, invalid: true},
		{name: "false limit", body: `{"paging":{"limit":false}}`, invalid: true},
		{name: "array paging", body: `{"paging":[]}`, invalid: true},
		{name: "unknown paging field", body: `{"paging":{"unknown":0}}`},
		{name: "unsupported mode", body: `{"paging":{"mode":"offset"}}`, invalid: true},
		{name: "cursor short keep alive", body: `{"paging":{"mode":"cursor","keep_alive_sec":59}}`, invalid: true},
		{name: "cursor long keep alive", body: `{"paging":{"mode":"cursor","keep_alive_sec":3601}}`, invalid: true},
		{name: "cursor with offset zero", body: `{"paging":{"cursor":"opaque","offset":0}}`, paging: interfaces.PagingRequest{Cursor: "opaque"}, invalid: true},
		{name: "cursor with empty mode", body: `{"paging":{"cursor":"opaque","mode":""}}`, paging: interfaces.PagingRequest{Cursor: "opaque"}, invalid: true},
		{name: "cursor with keep alive zero", body: `{"paging":{"cursor":"opaque","keep_alive_sec":0}}`, paging: interfaces.PagingRequest{Cursor: "opaque"}, invalid: true},
		{name: "cursor with false need total", body: `{"paging":{"cursor":"opaque"},"need_total":false}`, paging: interfaces.PagingRequest{Cursor: "opaque"}, invalid: true},
		{name: "unknown top field", body: `{"unknown":0}`},
		{name: "null filter", body: `{"filter_condition":null}`},
		{name: "empty sort", body: `{"sort":[]}`},
		{name: "empty output", body: `{"output_fields":[]}`},
		{name: "null binary mode", body: `{"binary_mode":null}`},
		{name: "metadata binary mode", body: `{"binary_mode":"metadata"}`, binaryMode: new(interfaces.BinaryModeMetadata)},
		{name: "content binary mode", body: `{"binary_mode":"content"}`, binaryMode: new(interfaces.BinaryModeContent)},
		{name: "cursor binary mode", body: `{"paging":{"cursor":"opaque"},"binary_mode":"content","need_total":true}`, paging: interfaces.PagingRequest{Cursor: "opaque"}, binaryMode: new(interfaces.BinaryModeContent), invalid: true},
		{name: "null aggregation", body: `{"aggregation":null,"group_by":null,"having":null}`},
		{name: "null sort item", body: `{"sort":[null]}`, invalid: true},
		{name: "null group item", body: `{"group_by":[null]}`, invalid: true},
		{name: "invalid binary mode", body: `{"binary_mode":"raw"}`, invalid: true},
		{name: "invalid sort direction", body: `{"sort":[{"field":"id","direction":"wrong"}]}`, invalid: true},
		{name: "invalid aggregation", body: `{"aggregation":{"property":"id","aggr":"wrong"}}`, invalid: true},
		{name: "safe bind error", body: `{"paging":{"limit":"fixture-private-input"}}`, invalid: true},
	}
	for _, proxy := range []bool{false, true} {
		for _, test := range cases {
			t.Run(fmt.Sprintf("proxy_%t/%s", proxy, test.name), func(t *testing.T) {
				var engine *gin.Engine
				var rs *vmock.MockResourceService
				var rds *vmock.MockResourceDataService
				resource := &interfaces.Resource{ID: "resource-1", Category: interfaces.ResourceCategoryAPI, APIEffectType: interfaces.APIEffectRead, Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id", OriginalName: "id", Type: interfaces.DataType_Integer}}}
				url := "/api/vega-backend/in/v1/resources/resource-1/data"
				if proxy {
					engine, _, rs, rds, _, _ = setupProxyReadHandlerTest(t, nil)
					rs.EXPECT().InternalGetByID(gomock.Any(), gomock.Nil(), "resource-1").Return(resource, nil)
					url = "/api/vega-backend/in/v1/proxy/resources/resource-1/data"
				} else {
					engine, rs, _, rds = setupResourceDataHandlerTest(t)
					rs.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil).AnyTimes()
				}
				if test.invalid {
					rds.EXPECT().QueryWithPaging(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				} else {
					rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).DoAndReturn(func(ctx context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
						expectedPaging := test.paging
						if expectedPaging.Cursor == "" && expectedPaging.Mode == "" {
							expectedPaging.Mode = interfaces.PagingModeSingle
						}
						assert.Equal(t, expectedPaging, params.Paging)
						assert.Equal(t, test.needTotal, params.NeedTotal)
						expectedBinary := test.binaryMode
						if params.Paging.Cursor == "" {
							assert.Equal(t, interfaces.Format_Original, params.Format)
							if expectedBinary == nil {
								expectedBinary = new(interfaces.BinaryModeMetadata)
							}
						}
						assert.Equal(t, expectedBinary, params.BinaryMode)
						assert.Equal(t, proxy, interfaces.IsTrustedProxyRead(ctx))
						return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{}}, nil
					})
				}
				request := httptest.NewRequest(http.MethodPost, url, strings.NewReader(test.body))
				request.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
				request.Header.Set("Content-Type", "application/json")
				if proxy {
					setProxyReadHeaders(request, interfaces.OPERATION_TYPE_QUERY_DATA, interfaces.ProxyChildTypeMetric)
				}
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)
				status := http.StatusOK
				if test.invalid {
					status = http.StatusBadRequest
				}
				require.Equal(t, status, recorder.Code, recorder.Body.String())
				assert.NotContains(t, recorder.Body.String(), "fixture-private-input")
			})
		}
	}
	t.Run("filters remain precise and capabilities reach the API service", func(t *testing.T) {
		for _, proxy := range []bool{false, true} {
			for _, body := range []string{
				`{"filter_condition":{"field":"id","operation":"==","value":9007199254740993}}`,
				`{"filter_condition":{"field":"id","operation":"==","value":9007199254740993.123456789}}`,
				`{"filter_condition":{"field":"id","operation":"null","value":null}}`,
				`{"sort":[{"field":"id","direction":"asc"}]}`,
				`{"aggregation":{"property":"id","aggr":"sum"},"group_by":[{"property":"id"}]}`,
				`{"output_fields":["id"]}`,
				`{"ignore_local_index":false}`,
			} {
				t.Run(fmt.Sprintf("proxy_%t/%s", proxy, body), func(t *testing.T) {
					resource := &interfaces.Resource{ID: "resource-1", Category: interfaces.ResourceCategoryAPI, APIEffectType: interfaces.APIEffectRead, Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id", OriginalName: "id", Type: interfaces.DataType_Integer}}}
					var engine *gin.Engine
					var rs *vmock.MockResourceService
					var rds *vmock.MockResourceDataService
					url := "/api/vega-backend/in/v1/resources/resource-1/data"
					if proxy {
						engine, _, rs, rds, _, _ = setupProxyReadHandlerTest(t, nil)
						rs.EXPECT().InternalGetByID(gomock.Any(), gomock.Nil(), resource.ID).Return(resource, nil)
						url = "/api/vega-backend/in/v1/proxy/resources/resource-1/data"
					} else {
						engine, rs, _, rds = setupResourceDataHandlerTest(t)
						rs.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)
					}
					rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).DoAndReturn(func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
						switch {
						case strings.Contains(body, "filter_condition"):
							filter, ok := params.FilterCondition.(map[string]any)
							require.True(t, ok)
							assert.Nil(t, params.FilterCondCfg)
							if strings.Contains(body, `"operation":"null"`) {
								assert.Contains(t, filter, "value")
								assert.Nil(t, filter["value"])
								break
							}
							want := json.Number("9007199254740993")
							if strings.Contains(body, ".123456789") {
								want = json.Number("9007199254740993.123456789")
							}
							assert.Equal(t, want, filter["value"])
							return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": want}}}, nil
						case strings.Contains(body, "sort"):
							require.Len(t, params.Sort, 1)
							assert.Equal(t, "ASC", params.Sort[0].Direction)
						case strings.Contains(body, "aggregation"):
							require.NotNil(t, params.Aggregation)
							assert.Equal(t, "sum", params.Aggregation.Aggr)
							require.Len(t, params.GroupBy, 1)
						case strings.Contains(body, "output_fields"):
							assert.Equal(t, []string{"id"}, params.OutputFields)
						case strings.Contains(body, "ignore_local_index"):
							require.NotNil(t, params.IgnoreLocalIndex)
							assert.False(t, *params.IgnoreLocalIndex)
						}
						return nil, httperrors.NewHTTPError(t.Context(), http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).WithErrorDetails("unsupported API query capability")
					})
					request := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
					request.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
					request.Header.Set("Content-Type", "application/json")
					if proxy {
						setProxyReadHeaders(request, interfaces.OPERATION_TYPE_QUERY_DATA, interfaces.ProxyChildTypeMetric)
					}
					recorder := httptest.NewRecorder()
					engine.ServeHTTP(recorder, request)
					if strings.Contains(body, `"operation":"=="`) {
						require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
						assert.Contains(t, recorder.Body.String(), "9007199254740993")
						return
					}
					require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
					assert.Contains(t, recorder.Body.String(), "unsupported API query capability")
				})
			}
		}
	})
	t.Run("resource permission precedes API field validation", func(t *testing.T) {
		engine, rs, _, rds := setupResourceDataHandlerTestWithPermission(t, httperrors.NewHTTPError(t.Context(), http.StatusForbidden, httperrors.PublicError_Forbidden))
		rs.EXPECT().GetByID(gomock.Any(), gomock.Any()).Times(0)
		rds.EXPECT().QueryWithPaging(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		request := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/resource-1/data", strings.NewReader(`{"filter_condition":null}`))
		request.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusForbidden, recorder.Code)
	})
}

func TestRestHandlerAPIResourceDataResponse(t *testing.T) {
	restore := setGinMode()
	defer restore()
	for _, proxy := range []bool{false, true} {
		for _, test := range []struct {
			name, body, expected string
			result               *interfaces.ResourceDataQueryResult
		}{
			{
				name: "single without total", body: `{}`,
				result:   &interfaces.ResourceDataQueryResult{QuerySource: interfaces.ResourceQuerySourceSource, Entries: []map[string]any{{"id": json.Number("9007199254740993")}}, TotalCount: 8, Columns: []string{"id"}, Paging: &interfaces.PagingResponse{}},
				expected: `{"query_source":"source","is_approximate":false,"entries":[{"id":9007199254740993}],"paging":{"next_cursor":null,"expires_at_sec":null}}`,
			},
			{
				name: "requested total single page", body: `{"paging":{"mode":"single","limit":2},"need_total":true}`,
				result:   &interfaces.ResourceDataQueryResult{QuerySource: interfaces.ResourceQuerySourceSource, Entries: []map[string]any{}, TotalCount: 8, Paging: &interfaces.PagingResponse{}},
				expected: `{"query_source":"source","is_approximate":false,"entries":[],"total_count":8,"paging":{"next_cursor":null,"expires_at_sec":null}}`,
			},
		} {
			t.Run(fmt.Sprintf("proxy_%t/%s", proxy, test.name), func(t *testing.T) {
				var engine *gin.Engine
				var rs *vmock.MockResourceService
				var rds *vmock.MockResourceDataService
				resource := &interfaces.Resource{ID: "resource-1", Category: interfaces.ResourceCategoryAPI, APIEffectType: interfaces.APIEffectRead, Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id", OriginalName: "id", Type: interfaces.DataType_Integer}}}
				url := "/api/vega-backend/in/v1/resources/resource-1/data"
				if proxy {
					engine, _, rs, rds, _, _ = setupProxyReadHandlerTest(t, nil)
					rs.EXPECT().InternalGetByID(gomock.Any(), gomock.Nil(), "resource-1").Return(resource, nil)
					url = "/api/vega-backend/in/v1/proxy/resources/resource-1/data"
				} else {
					engine, rs, _, rds = setupResourceDataHandlerTest(t)
					rs.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
				}
				rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).Return(test.result, nil)
				request := httptest.NewRequest(http.MethodPost, url, strings.NewReader(test.body))
				request.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
				request.Header.Set("Content-Type", "application/json")
				if proxy {
					setProxyReadHeaders(request, interfaces.OPERATION_TYPE_QUERY_DATA, interfaces.ProxyChildTypeMetric)
				}
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				assert.JSONEq(t, test.expected, recorder.Body.String())
			})
		}
	}
}

func TestRestHandlerAPIResourceQueryErrorDetails(t *testing.T) {
	engine, rs, _, rds := setupResourceDataHandlerTest(t)
	resource := &interfaces.Resource{
		ID: "resource-1", Category: interfaces.ResourceCategoryAPI, APIEffectType: interfaces.APIEffectRead,
		Enabled: true, Status: interfaces.ResourceStatusActive,
		SchemaDefinition: []*interfaces.Property{{Name: "id", OriginalName: "id", Type: interfaces.DataType_Integer}},
	}
	rs.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)

	const upstreamBody = `{"message":"upstream fixture details"}`
	publicErr := httperrors.NewHTTPError(t.Context(), http.StatusBadGateway, verrors.VegaBackend_Query_ExecuteFailed).
		WithErrorDetails(upstreamBody).
		WithMetadata(map[string]any{"stage": "mapping", "upstream_status": 200})
	rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).Return(nil, publicErr)
	logged, traced := captureResourceFailureTelemetry(t)

	request := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources/resource-1/data", strings.NewReader(`{}`))
	request.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadGateway, recorder.Code, recorder.Body.String())
	var response httperrors.BaseError
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, upstreamBody, response.ErrorDetails)
	assert.Equal(t, float64(200), response.Metadata["upstream_status"])
	require.Len(t, *logged, 1)
	assert.NotContains(t, (*logged)[0], "upstream fixture details")
	assert.Contains(t, (*logged)[0], `"upstream_status":200`)
	require.Len(t, *traced, 1)
	assert.Equal(t, http.StatusBadGateway, (*traced)[0].HTTPCode)
	assert.Equal(t, "mapping", (*traced)[0].Metadata["stage"])
	assert.NotContains(t, (*traced)[0].ErrorDetails, "upstream fixture details")
}

func captureResourceFailureTelemetry(t *testing.T) (*[]string, *[]httperrors.HTTPError) {
	t.Helper()
	var logged []string
	var traced []httperrors.HTTPError
	patches := gomonkey.ApplyFunc(otellog.LogError, func(_ context.Context, _ string, err error, _ ...attribute.KeyValue) {
		logged = append(logged, err.Error())
	}).ApplyFunc(oteltrace.AddHttpAttrs4HttpError, func(_ trace.Span, err *httperrors.HTTPError) {
		traced = append(traced, *err)
	})
	t.Cleanup(patches.Reset)
	return &logged, &traced
}
