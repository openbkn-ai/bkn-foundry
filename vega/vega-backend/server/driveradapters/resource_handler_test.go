// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
)

func TestRestHandlerAPIResourceSaveErrorDetails(t *testing.T) {
	const body = `{"catalog_id":"catalog-1","name":"orders","category":"api","api_effect_type":"read","api_definition":{},"schema_definition":[{"name":"id","type":"integer","filterable":false}],"expected_update_time":1}`
	const upstreamBody = `{"message":"upstream fixture details"}`
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			engine, _, rs := setupResourceHandlerTest(t)
			publicErr := httperrors.NewHTTPError(t.Context(), http.StatusBadGateway, verrors.VegaBackend_Query_ExecuteFailed).
				WithErrorDetails(upstreamBody).
				WithMetadata(map[string]any{"stage": "response", "upstream_status": 503})
			url := "/api/vega-backend/in/v1/resources"
			if method == http.MethodPost {
				rs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, publicErr)
			} else {
				url += "/res-1"
				rs.EXPECT().Update(gomock.Any(), gomock.Any()).Return(publicErr)
			}
			_, traced := captureResourceFailureTelemetry(t)
			request := httptest.NewRequest(method, url, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadGateway, recorder.Code, recorder.Body.String())
			var response httperrors.BaseError
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, upstreamBody, response.ErrorDetails)
			require.Len(t, *traced, 1)
			assert.Equal(t, http.StatusBadGateway, (*traced)[0].HTTPCode)
			assert.Equal(t, "response", (*traced)[0].Metadata["stage"])
			assert.NotContains(t, (*traced)[0].ErrorDetails, "upstream fixture details")
		})
	}
}

func TestRestHandlerAPIResourceInvalidBody(t *testing.T) {
	const fixtureValue = "fixture-only"
	const body = `{"category":"api","api_definition":{"variables":{"key":{"sensitive":true,"value":"` + fixtureValue + `" ?}}}}`
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			engine, _, _ := setupResourceHandlerTest(t)
			_, traced := captureResourceFailureTelemetry(t)
			url := "/api/vega-backend/in/v1/resources"
			if method == http.MethodPut {
				url += "/res-1"
			}
			request := httptest.NewRequest(method, url, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			var response httperrors.BaseError
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, verrors.VegaBackend_InvalidParameter_RequestBody, response.ErrorCode)
			assert.NotContains(t, recorder.Body.String(), fixtureValue)
			assert.Equal(t, "invalid resource request body", response.ErrorDetails)
			require.Len(t, *traced, 1)
			assert.Equal(t, "invalid resource request body", (*traced)[0].ErrorDetails)
		})
	}
}

func TestRestHandlerAPIResourceValidationInput(t *testing.T) {
	const body = `{"catalog_id":"catalog-1","name":"orders","category":"api","api_effect_type":"read","api_definition":{},"schema_definition":[{"name":"id","type":"integer","filterable":true}],"expected_update_time":1,"validation":{"filter_condition":{"field":"id","operation":"eq","value":9007199254740993},"paging":{"offset":2,"limit":1}}}`
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			engine, _, rs := setupResourceHandlerTest(t)
			check := func(req *interfaces.ResourceRequest) {
				require.NotNil(t, req.Validation)
				filter, ok := req.Validation.FilterCondition.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, json.Number("9007199254740993"), filter["value"])
				assert.Equal(t, interfaces.PagingRequest{Offset: 2, Limit: 1}, req.Validation.Paging)
			}
			url := "/api/vega-backend/in/v1/resources"
			status := http.StatusCreated
			if method == http.MethodPost {
				rs.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
					check(req)
					return &interfaces.Resource{ID: "res-1"}, nil
				})
			} else {
				url += "/res-1"
				status = http.StatusNoContent
				rs.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) error {
					check(req)
					return nil
				})
			}
			request := httptest.NewRequest(method, url, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			require.Equal(t, status, recorder.Code, recorder.Body.String())

			const datasetBody = `{"catalog_id":"catalog-1","name":"orders","category":"dataset","schema_definition":[{"name":"id","type":"integer","filterable":true}],"expected_update_time":1,"validation":{}}`
			request = httptest.NewRequest(method, url, strings.NewReader(datasetBody))
			request.Header.Set("Content-Type", "application/json")
			recorder = httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			var response httperrors.BaseError
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, response.ErrorCode)
		})
	}
}

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

	t.Run("invalid enabled", func(t *testing.T) {
		engine, _ := setup(t)
		req := httptest.NewRequest(http.MethodGet, url+"?enabled=abc", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.InvalidParameter")
		assert.Contains(t, w.Body.String(), "invalid enabled: abc")
	})

	t.Run("invalid last discover status", func(t *testing.T) {
		engine, _ := setup(t)
		req := httptest.NewRequest(http.MethodGet, url+"?last_discover_status=unknown", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.InvalidParameter")
		assert.Contains(t, w.Body.String(), "invalid last_discover_status: unknown")
	})

	t.Run("success list resources with schema", func(t *testing.T) {
		engine, rs := setup(t)
		rs.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.ResourcesQueryParams) ([]*interfaces.Resource, int64, error) {
				assert.Equal(t, "orders", params.Name)
				assert.Equal(t, interfaces.ResourceCategoryDataset, params.Category)
				assert.Equal(t, interfaces.ResourceStatusActive, params.Status)
				assert.Equal(t, "external_data", params.Schema)
				require.NotNil(t, params.Enabled)
				assert.True(t, *params.Enabled)
				assert.Equal(t, interfaces.DiscoverStatusUpdated, params.LastDiscoverStatus)
				assert.Equal(t, "update_time", params.Sort)
				assert.Equal(t, "DESC", params.Direction)
				return []*interfaces.Resource{}, int64(0), nil
			})

		req := httptest.NewRequest(http.MethodGet, url+"?name=orders&category=dataset&status=active&enabled=true&last_discover_status=updated&schema=external_data", nil)
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
	t.Run("creates a read-only POST API resource", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
			assert.Equal(t, interfaces.ResourceCategoryAPI, req.Category)
			assert.Equal(t, interfaces.APIEffectRead, req.APIEffectType)
			require.NotNil(t, req.APIDefinition)
			assert.Equal(t, "POST", req.APIDefinition.Request.Method)
			assert.Equal(t, "application/json", req.APIDefinition.Response.ContentType)
			assert.Equal(t, false, req.APIDefinition.Response.Statuses["default"].Schema)
			require.NotNil(t, req.APIDefinition.Response.Statuses["200"].ResultRows)
			assert.Equal(t, json.Number("9007199254740993"), req.APIDefinition.Request.Parameters[0]["value"])
			value, exists := req.APIDefinition.SuccessRule.BodyFilterCondition["value"]
			assert.True(t, exists)
			assert.Nil(t, value)
			return &interfaces.Resource{ID: "api-resource"}, nil
		})
		body := `{"catalog_id":"catalog-1","name":"orders","category":"api","api_effect_type":"read","schema_definition":[{"name":"id","original_name":"id","type":"integer"}],"api_definition":{"request":{"method":"POST","path":"orders","parameters":[{"name":"id","value":9007199254740993}]},"response":{"content_type":"application/json","statuses":{"200":{"description":"orders","schema":{"type":"array","items":{"type":"object"}},"result_rows":""},"default":{"description":"rejected","schema":false}}},"success_rule":{"http_status":[200],"body_filter_condition":{"field":"data","operation":"eq","value":null}}}}`
		request := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		assert.Equal(t, http.StatusCreated, response.Code)
	})

	restoreGinMode := setGinMode()
	defer restoreGinMode()

	const url = "/api/vega-backend/in/v1/resources"
	body := `{"id":"res-1","catalog_id":"catalog-1","name":"dataset","category":"dataset","schema_definition":[{"name":"title","type":"string"}]}`

	t.Run("preserves precise numbers in dynamic definitions", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
				assert.Equal(t, json.Number("9007199254740993"), req.SchemaDefinition[0].Attributes["value"])
				return &interfaces.Resource{ID: "res-1", Name: req.Name}, nil
			})
		preciseBody := `{"id":"res-1","catalog_id":"catalog-1","name":"dataset","category":"dataset","schema_definition":[{"name":"title","type":"string","attributes":{"value":9007199254740993}}]}`
		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(preciseBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code)
	})

	t.Run("rejects manual physical table creation", func(t *testing.T) {
		engine, _, _ := setupResourceHandlerTest(t)
		body := `{"catalog_id":"catalog-1","name":"orders","category":"table","schema":"app","source_identifier":"app.orders"}`
		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "VegaBackend.Resource.CategoryNotCreatable")
	})

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
			httperrors.NewHTTPError(context.Background(), http.StatusNotFound, verrors.VegaBackend_Resource_CatalogNotFound))

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

	t.Run("preserves precise numbers in updated dynamic definitions", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *interfaces.ResourceRequest) error {
				assert.Equal(t, json.Number("9007199254740993"), req.SchemaDefinition[0].Attributes["value"])
				return nil
			})
		preciseBody := `{"id":"res-1","catalog_id":"catalog-1","name":"dataset-new","category":"dataset","schema_definition":[{"name":"title","type":"string","attributes":{"value":9007199254740993}}],"expected_update_time":1}`
		req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(preciseBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		require.Equal(t, http.StatusNoContent, w.Code)
	})

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
			httperrors.NewHTTPError(context.Background(), http.StatusConflict,
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
			httperrors.NewHTTPError(context.Background(), http.StatusBadRequest,
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
		rs.EXPECT().DeleteByIDs(gomock.Any(), []string{"res-1", "res-2"}, false, false).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1,res-2,res-1", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("ignores missing resources when requested", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().DeleteByIDs(gomock.Any(), []string{"res-1", "missing"}, true, false).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1,missing?ignore_missing=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("requires missing state for a single resource", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().DeleteByIDs(gomock.Any(), []string{"res-1"}, false, true).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1?only_if_stale=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("applies stale-state guard to a batch", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().DeleteByIDs(gomock.Any(), []string{"res-1", "res-2"}, false, true).Return(nil)
		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1,res-2,res-1?only_if_stale=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})

	t.Run("rejects incompatible missing-state options", func(t *testing.T) {
		engine, _, _ := setupResourceHandlerTest(t)
		req := httptest.NewRequest(http.MethodDelete, "/api/vega-backend/in/v1/resources/res-1?only_if_stale=true&ignore_missing=true", nil)
		w := httptest.NewRecorder()

		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})
}

func TestReadResourceDiscoverStrategy(t *testing.T) {
	for _, tt := range []struct {
		body, want string
		fail       bool
	}{
		{"", "full_sync", false}, {`{}`, "full_sync", false}, {`{"strategy":"count_only"}`, "count_only", false},
		{`{"strategy":"create_only"}`, "", true}, {`{"strategy":"invalid"}`, "", true}, {`{`, "", true},
	} {
		t.Run(tt.body, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/resources/id/discover", strings.NewReader(tt.body))
			got, err := readResourceDiscoverStrategy(c)
			if tt.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestRestHandlerDiscoverResource(t *testing.T) {
	for _, tt := range []struct {
		category string
		status   int
	}{
		{interfaces.ResourceCategoryDataset, http.StatusBadRequest},
		{interfaces.ResourceCategoryTable, http.StatusOK},
	} {
		t.Run(tt.category, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rs := vmock.NewMockResourceService(ctrl)
			dts := vmock.NewMockDiscoverTaskService(ctrl)
			handler := &restHandler{rs: rs, dts: dts}
			engine := gin.New()
			engine.POST("/resources/:id/discover", handler.DiscoverResourceByIn)
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, "res").Return(&interfaces.Resource{
				ID: "res", CatalogID: "cat", Category: tt.category,
			}, nil)
			dts.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *interfaces.CreateDiscoverTaskRequest) (string, error) {
				assert.Equal(t, "res", req.ResourceID)
				assert.Equal(t, "cat", req.CatalogID)
				assert.Equal(t, interfaces.DiscoverStrategyCountOnly, req.Strategy)
				if tt.status == http.StatusBadRequest {
					return "", httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
						WithErrorDetails("resource category dataset does not support count_only")
				}
				return "task", nil
			})
			req := httptest.NewRequest(http.MethodPost, "/resources/res/discover", strings.NewReader(`{"strategy":"count_only"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			assert.Equal(t, tt.status, w.Code)
			if tt.status == http.StatusBadRequest {
				assert.Contains(t, w.Body.String(), "does not support count_only")
				assert.NotContains(t, w.Body.String(), `"id"`)
			} else {
				assert.JSONEq(t, `{"id":"task"}`, w.Body.String())
			}
		})
	}
}

func TestResourceRequestHTTPContract(t *testing.T) {
	restore := setGinMode()
	defer restore()
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, fields := range []string{
			`"name":false`, `"api_effect_type":123`, `"schema_definition":{}`,
			`"logical_type":"derived"`, `"logical_view_definition":{"sql":"SELECT 1"}`,
			`"api_definition":{"input_schema":{}}`,
			`"api_definition":{"request":{"method":"GET","path":"read"},"response":{"content_type":"application/json","statuses":{"200":{"description":"rows","schema":{"type":"array"},"result":{"rows":""}}}},"success_rule":{"http_status":[200]}}`,
			`"api_definition":{"request":{"method":"GET","path":"read"},"response":{"content_type":"application/json","statuses":{"200":{"description":"rows","schema":{"type":"array"},"result":{"rows":"","columns":{"value":[{"name":"id","type":"integer"}]}}}}},"success_rule":{"http_status":[200]}}`,
			`"api_definition":{"request":{"method":"GET","path":"items","url":"https://override.example.invalid"}}`,
		} {
			t.Run(method+"/invalid/"+fields, func(t *testing.T) {
				engine, _, rs := setupResourceHandlerTest(t)
				rs.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)
				rs.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
				url := "/api/vega-backend/in/v1/resources"
				if method == http.MethodPut {
					url += "/res-1"
				}
				body := `{"catalog_id":"catalog-1","name":"resource","category":"api","api_effect_type":"read","api_definition":{},"schema_definition":[{"name":"id","type":"integer","filterable":false}],"expected_update_time":1,` + fields + `}`
				request := httptest.NewRequest(method, url, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)
				require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			})
		}
		for _, fields := range []string{
			`"unknown":null`, `"logic_type":"ignored"`, `"logic_definition":{"ignored":true}`, `"condition_constraints":{}`,
			`"schema_definition":[{"name":"id","type":"integer","filterable":false,"condition_constraints":{"operations":["eq"]}}]`,
			`"schema_definition":[{"name":"id","type":"integer","filterable":false,"foreign_property":0}]`,
		} {
			t.Run(method+"/unknown is ignored/"+fields, func(t *testing.T) {
				engine, _, rs := setupResourceHandlerTest(t)
				check := func(req *interfaces.ResourceRequest) {
					assert.Equal(t, "resource", req.Name)
					assert.Equal(t, "catalog-1", req.CatalogID)
					assert.Equal(t, interfaces.ResourceCategoryAPI, req.Category)
					assert.Equal(t, interfaces.APIEffectRead, req.APIEffectType)
					assert.Equal(t, &interfaces.APIDefinition{}, req.APIDefinition)
					require.Len(t, req.SchemaDefinition, 1)
					assert.Equal(t, "id", req.SchemaDefinition[0].Name)
					assert.Equal(t, interfaces.DataType_Integer, req.SchemaDefinition[0].Type)
					assert.Empty(t, req.LogicalType)
					assert.Nil(t, req.LogicalViewDefinition)
				}
				url := "/api/vega-backend/in/v1/resources"
				status := http.StatusCreated
				if method == http.MethodPost {
					rs.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
						check(req)
						return &interfaces.Resource{ID: "res-1"}, nil
					})
				} else {
					url += "/res-1"
					status = http.StatusNoContent
					rs.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) error {
						check(req)
						assert.Equal(t, "res-1", req.ID)
						return nil
					})
				}
				body := `{"catalog_id":"catalog-1","name":"resource","category":"api","api_effect_type":"read","api_definition":{},"schema_definition":[{"name":"id","type":"integer","filterable":false}],"expected_update_time":1,` + fields + `}`
				request := httptest.NewRequest(method, url, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)
				require.Equal(t, status, recorder.Code, recorder.Body.String())
			})
		}
	}
	t.Run("API update null definition mapping and effect are omitted for service preservation", func(t *testing.T) {
		engine, _, rs := setupResourceHandlerTest(t)
		rs.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request *interfaces.ResourceRequest) error {
			assert.Equal(t, "res-1", request.ID)
			assert.Equal(t, "renamed", request.Name)
			assert.Equal(t, interfaces.ResourceCategoryAPI, request.Category)
			assert.Nil(t, request.APIDefinition)
			assert.Nil(t, request.SchemaDefinition)
			assert.Empty(t, request.APIEffectType)
			return nil
		})
		body := `{"name":"renamed","category":"api","api_definition":null,"schema_definition":null,"api_effect_type":null,"logical_type":null,"logical_view_definition":null,"expected_update_time":1}`
		request := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/resources/res-1", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	})
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, test := range []struct {
			name, fields string
			valid        bool
		}{
			{"foreign null and empty values", `"api_definition":null,"api_effect_type":"","logical_view_definition":null,"logical_type":""`, true},
			{"foreign API effect", `"api_effect_type":"read"`, false},
			{"foreign API definition", `"api_definition":{}`, false},
			{"foreign logical type", `"logical_type":"derived"`, false},
			{"foreign logical definition", `"logical_view_definition":{"sql":"SELECT 1"}`, false},
		} {
			t.Run(method+"/dataset/"+test.name, func(t *testing.T) {
				engine, _, rs := setupResourceHandlerTest(t)
				url := "/api/vega-backend/in/v1/resources"
				if method == http.MethodPut {
					url += "/res-1"
				}
				if test.valid {
					if method == http.MethodPost {
						rs.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
							assert.Nil(t, req.APIDefinition)
							assert.Empty(t, req.APIEffectType)
							assert.Nil(t, req.LogicalViewDefinition)
							assert.Empty(t, req.LogicalType)
							return &interfaces.Resource{ID: "res-1"}, nil
						})
					} else {
						rs.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) error {
							assert.Nil(t, req.APIDefinition)
							assert.Empty(t, req.APIEffectType)
							assert.Nil(t, req.LogicalViewDefinition)
							assert.Empty(t, req.LogicalType)
							return nil
						})
					}
				} else {
					rs.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)
					rs.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
				}
				body := `{"name":"dataset","category":"dataset","schema_definition":[{"name":"id","type":"integer"}],"expected_update_time":1,` + test.fields + `}`
				request := httptest.NewRequest(method, url, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)
				status := http.StatusBadRequest
				if test.valid {
					status = http.StatusCreated
					if method == http.MethodPut {
						status = http.StatusNoContent
					}
				}
				require.Equal(t, status, recorder.Code, recorder.Body.String())
			})
		}
	}
	for _, fields := range []string{
		`"api_effect_type":null,"api_definition":null,"schema_definition":null`,
		`"api_effect_type":"read","api_definition":null,"schema_definition":[{"name":"id","type":"integer"}]`,
		`"api_effect_type":"read","api_definition":{},"schema_definition":null`,
		`"api_effect_type":null,"api_definition":{},"schema_definition":[{"name":"id","type":"integer"}]`,
	} {
		t.Run("API creation requires meaningful values/"+fields, func(t *testing.T) {
			engine, _, rs := setupResourceHandlerTest(t)
			rs.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)
			request := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/resources", strings.NewReader(`{"name":"resource","category":"api",`+fields+`}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}
