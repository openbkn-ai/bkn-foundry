// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// TestQueryObjectInstances_Success test QueryObjectInstances success scenario.
func TestQueryObjectInstances_Success(t *testing.T) {
	convey.Convey("TestQueryObjectInstances_Success", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://localhost:8080/api/ontology-query",
			httpClient: mockHTTPClient,
		}

		ctx := context.Background()
		req := &interfaces.QueryObjectInstancesReq{
			KnID:  "kn-001",
			OtID:  "ot-001",
			Limit: 10,
		}

		// Mock a successful HTTP response.
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, jsonBytes(map[string]interface{}{
				"datas":       []interface{}{},
				"object_type": map[string]interface{}{},
			}), nil)

		resp, err := client.QueryObjectInstances(ctx, req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp, convey.ShouldNotBeNil)
	})
}

func TestObjectQueryIdentityIsStablePerConcreteQuery(t *testing.T) {
	first := &interfaces.QueryObjectInstancesReq{KnID: "kn-1", OtID: "customer", Limit: 10}
	replay := &interfaces.QueryObjectInstancesReq{KnID: "kn-1", OtID: "customer", Limit: 10}
	different := &interfaces.QueryObjectInstancesReq{KnID: "kn-1", OtID: "order", Limit: 10}
	const target = "/api/bkn-backend/v1/knowledge-networks/kn-1/object-types/customer/objects?include_type_info=false"
	if objectQueryIdentity(target, first) != objectQueryIdentity(target, replay) {
		t.Fatal("equivalent queries must have a stable identity")
	}
	if objectQueryIdentity(target, first) == objectQueryIdentity(target, different) {
		t.Fatal("different queries must not share an identity")
	}
	if objectQueryIdentity(target, first) == objectQueryIdentity(target+"&ignoring_store_cache=true", first) {
		t.Fatal("different query parameters must not share an identity")
	}
}

// TestQueryObjectInstances_HTTPError test QueryObjectInstances HTTP error.
func TestQueryObjectInstances_HTTPError(t *testing.T) {
	convey.Convey("TestQueryObjectInstances_HTTPError", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://localhost:8080/api/ontology-query",
			httpClient: mockHTTPClient,
		}

		ctx := context.Background()
		req := &interfaces.QueryObjectInstancesReq{
			KnID:  "kn-001",
			OtID:  "ot-001",
			Limit: 10,
		}

		// Mock an HTTP error.
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("connection refused"))

		_, err := client.QueryObjectInstances(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestQueryLogicProperties_Success test QueryLogicProperties success scenario.
func TestQueryLogicProperties_Success(t *testing.T) {
	convey.Convey("TestQueryLogicProperties_Success", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://localhost:8080/api/ontology-query",
			httpClient: mockHTTPClient,
		}

		ctx := context.Background()
		req := &interfaces.QueryLogicPropertiesReq{
			KnID:               "kn-001",
			OtID:               "ot-001",
			InstanceIdentities: []map[string]interface{}{{"id": "obj-001"}},
			Properties:         []string{"prop1"},
		}

		// Mock a successful HTTP response.
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, jsonBytes(map[string]interface{}{
				"datas": []interface{}{
					map[string]interface{}{"prop1": "value1"},
				},
			}), nil)

		resp, err := client.QueryLogicProperties(ctx, req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp, convey.ShouldNotBeNil)
		convey.So(len(resp.Datas), convey.ShouldEqual, 1)
	})
}

// TestQueryLogicProperties_HTTPError test QueryLogicProperties HTTP error.
func TestQueryLogicProperties_HTTPError(t *testing.T) {
	convey.Convey("TestQueryLogicProperties_HTTPError", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://localhost:8080/api/ontology-query",
			httpClient: mockHTTPClient,
		}

		ctx := context.Background()
		req := &interfaces.QueryLogicPropertiesReq{
			KnID: "kn-001",
			OtID: "ot-001",
		}

		// Mock an HTTP error.
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("connection refused"))

		_, err := client.QueryLogicProperties(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestQueryActions_Success test QueryActions success scenario.
func TestQueryActions_Success(t *testing.T) {
	convey.Convey("TestQueryActions_Success", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://localhost:8080/api/ontology-query",
			httpClient: mockHTTPClient,
		}

		ctx := context.Background()
		req := &interfaces.QueryActionsRequest{
			KnID:               "kn-001",
			AtID:               "at-001",
			InstanceIdentities: []map[string]interface{}{{"id": "obj-001"}},
		}

		// Mock a successful HTTP response.
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, jsonBytes(map[string]interface{}{
				"action_source": map[string]interface{}{
					"type":    "tool",
					"box_id":  "box-001",
					"tool_id": "tool-001",
				},
				"actions": []interface{}{
					map[string]interface{}{
						"parameters": map[string]interface{}{"key": "value"},
					},
				},
				"total_count": 1,
				"overall_ms":  100,
			}), nil)

		resp, err := client.QueryActions(ctx, req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp, convey.ShouldNotBeNil)
		convey.So(resp.ActionSource, convey.ShouldNotBeNil)
		convey.So(resp.ActionSource.Type, convey.ShouldEqual, "tool")
	})
}

// TestQueryActions_HTTPError test QueryActions HTTP error.
func TestQueryActions_HTTPError(t *testing.T) {
	convey.Convey("TestQueryActions_HTTPError", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://localhost:8080/api/ontology-query",
			httpClient: mockHTTPClient,
		}

		ctx := context.Background()
		req := &interfaces.QueryActionsRequest{
			KnID: "kn-001",
			AtID: "at-001",
		}

		// Mock an HTTP error.
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("connection refused"))

		_, err := client.QueryActions(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestQueryInstanceSubgraph_Success test QueryInstanceSubgraph success scenario.
func TestQueryInstanceSubgraph_Success(t *testing.T) {
	convey.Convey("TestQueryInstanceSubgraph_Success", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://localhost:8080/api/ontology-query",
			httpClient: mockHTTPClient,
		}

		ctx := context.Background()
		req := &interfaces.QueryInstanceSubgraphReq{
			KnID: "kn-001",
			RelationTypePaths: []interface{}{
				map[string]interface{}{"source": "obj-001"},
			},
		}

		// Mock a successful HTTP response.
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, jsonBytes(map[string]interface{}{
				"entries": []interface{}{},
			}), nil)

		resp, err := client.QueryInstanceSubgraph(ctx, req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp, convey.ShouldNotBeNil)
	})
}

// TestQueryInstanceSubgraph_HTTPError test QueryInstanceSubgraph HTTP error.
func TestQueryInstanceSubgraph_HTTPError(t *testing.T) {
	convey.Convey("TestQueryInstanceSubgraph_HTTPError", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://localhost:8080/api/ontology-query",
			httpClient: mockHTTPClient,
		}

		ctx := context.Background()
		req := &interfaces.QueryInstanceSubgraphReq{
			KnID: "kn-001",
		}

		// Mock an HTTP error.
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("connection refused"))

		_, err := client.QueryInstanceSubgraph(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestQueryObjectInstances_DownstreamBadRequestRemappedToBadRequest regression #235:
// When the downstream performs knn on a non-vector field, it returns 4xx ("left field is not a vector field"),
// Shared HTTP client maps it to CommonExternalServerError"dependencyserviceexception".the driven layer must.
// Remap 4xx to BadRequest and retain the downstream detail, so that the caller can see clearly whether his own parameters are used incorrectly or not.
// Service failure.
func TestQueryObjectInstances_DownstreamBadRequestRemappedToBadRequest(t *testing.T) {
	convey.Convey("downstream 4xx -> BadRequest, detail preserved", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://localhost:8080/api/ontology-query",
			httpClient: mockHTTPClient,
		}
		ctx := context.Background()
		req := &interfaces.QueryObjectInstancesReq{KnID: "kn-001", OtID: "ot-001", Limit: 10}

		// What the shared http client returns for a downstream 400: the raw
		// ontology-query message flattened into CommonExternalServerError.
		detail := "OntologyQuery.InvalidParameter.Condition: condition [knn] left field is not a vector field: child_name:string"
		wrapped := infraErr.NewHTTPError(ctx, http.StatusBadRequest, infraErr.ErrExtCommonExternalServerError, detail)
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusBadRequest, nil, wrapped)

		_, err := client.QueryObjectInstances(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)

		var he *infraErr.HTTPError
		convey.So(errors.As(err, &he), convey.ShouldBeTrue)
		convey.So(he.HTTPCode, convey.ShouldEqual, http.StatusBadRequest)
		// No longer classified as a dependency outage.
		convey.So(strings.Contains(he.Code, "CommonExternalServerError"), convey.ShouldBeFalse)
		convey.So(strings.Contains(he.Code, "BadRequest"), convey.ShouldBeTrue)
		// The actionable downstream cause survives so the caller can fix the query.
		convey.So(strings.Contains(fmt.Sprintf("%v", he.ErrorDetails), "not a vector field"), convey.ShouldBeTrue)
	})
}

// TestQueryObjectInstances_DownstreamNotFoundKeepsCode downstream 404 (such as kn_id/ot_id.
// Does not exist)must preserve 404 semantics,must not be collapsed into 400"parametererror".
func TestQueryObjectInstances_DownstreamNotFoundKeepsCode(t *testing.T) {
	convey.Convey("downstream 404 stays NotFound, not collapsed to 400", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{logger: mockLogger, baseURL: "http://x", httpClient: mockHTTPClient}
		ctx := context.Background()
		req := &interfaces.QueryObjectInstancesReq{KnID: "missing", OtID: "ot-001", Limit: 10}

		wrapped := infraErr.NewHTTPError(ctx, http.StatusNotFound, infraErr.ErrExtCommonExternalServerError, "knowledge network not found")
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusNotFound, nil, wrapped)

		_, err := client.QueryObjectInstances(ctx, req)
		var he *infraErr.HTTPError
		convey.So(errors.As(err, &he), convey.ShouldBeTrue)
		convey.So(he.HTTPCode, convey.ShouldEqual, http.StatusNotFound)
		convey.So(strings.Contains(he.Code, "NotFound"), convey.ShouldBeTrue)
		convey.So(strings.Contains(he.Code, "CommonExternalServerError"), convey.ShouldBeFalse)
	})
}

// TestExecuteActions_PreservesConflictStatus ensures 409 DuplicateExecution is not
// collapsed into a generic bad-gateway, so Agents can distinguish duplicate rejection.
func TestExecuteActions_PreservesConflictStatus(t *testing.T) {
	convey.Convey("ExecuteActions preserves 409 from ontology-query", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{logger: mockLogger, baseURL: "http://x", httpClient: mockHTTPClient}
		ctx := context.Background()
		req := &interfaces.ExecuteActionsRequest{
			KnID:               "kn-001",
			AtID:               "at-001",
			InstanceIdentities: []map[string]any{{"id": "1"}},
		}

		wrapped := infraErr.NewHTTPError(ctx, http.StatusConflict, infraErr.ErrExtCommonExternalServerError,
			`{"error_code":"OntologyQuery.ActionExecution.DuplicateExecution"}`)
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusConflict, nil, wrapped)

		_, err := client.ExecuteActions(ctx, req)
		var he *infraErr.HTTPError
		convey.So(errors.As(err, &he), convey.ShouldBeTrue)
		convey.So(he.HTTPCode, convey.ShouldEqual, http.StatusConflict)
		convey.So(strings.Contains(he.Code, "Conflict"), convey.ShouldBeTrue)
		convey.So(strings.Contains(fmt.Sprintf("%v", he.ErrorDetails), "DuplicateExecution"), convey.ShouldBeTrue)
	})
}

// TestQueryObjectInstances_DownstreamServerErrorUntouched Downstream 5xx (true service failure)
// Leave the transfer error (no HTTP code) as is and not downgrade to 400.
func TestQueryObjectInstances_DownstreamServerErrorUntouched(t *testing.T) {
	convey.Convey("downstream 5xx stays as-is", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		client := &ontologyQueryClient{logger: mockLogger, baseURL: "http://x", httpClient: mockHTTPClient}
		ctx := context.Background()
		req := &interfaces.QueryObjectInstancesReq{KnID: "kn-001", OtID: "ot-001", Limit: 10}

		wrapped := infraErr.NewHTTPError(ctx, http.StatusInternalServerError, infraErr.ErrExtCommonExternalServerError, "boom")
		mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusInternalServerError, nil, wrapped)

		_, err := client.QueryObjectInstances(ctx, req)
		var he *infraErr.HTTPError
		convey.So(errors.As(err, &he), convey.ShouldBeTrue)
		convey.So(he.HTTPCode, convey.ShouldEqual, http.StatusInternalServerError)
	})
}
