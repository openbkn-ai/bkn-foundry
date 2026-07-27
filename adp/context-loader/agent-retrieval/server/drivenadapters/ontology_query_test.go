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

	infraErr "github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/mocks"
)

// TestQueryObjectInstances_Success 测试 QueryObjectInstances 成功场景
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

		// Mock HTTP 成功响应
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, map[string]interface{}{
				"datas":       []interface{}{},
				"object_type": map[string]interface{}{},
			}, nil)

		resp, err := client.QueryObjectInstances(ctx, req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp, convey.ShouldNotBeNil)
	})
}

// TestQueryObjectInstances_HTTPError 测试 QueryObjectInstances HTTP 错误
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

		// Mock HTTP 错误
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("connection refused"))

		_, err := client.QueryObjectInstances(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestQueryLogicProperties_Success 测试 QueryLogicProperties 成功场景
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

		// Mock HTTP 成功响应
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, map[string]interface{}{
				"datas": []interface{}{
					map[string]interface{}{"prop1": "value1"},
				},
			}, nil)

		resp, err := client.QueryLogicProperties(ctx, req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp, convey.ShouldNotBeNil)
		convey.So(len(resp.Datas), convey.ShouldEqual, 1)
	})
}

// TestQueryLogicProperties_HTTPError 测试 QueryLogicProperties HTTP 错误
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

		// Mock HTTP 错误
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("connection refused"))

		_, err := client.QueryLogicProperties(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestQueryActions_Success 测试 QueryActions 成功场景
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

		// Mock HTTP 成功响应
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, map[string]interface{}{
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
			}, nil)

		resp, err := client.QueryActions(ctx, req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp, convey.ShouldNotBeNil)
		convey.So(resp.ActionSource, convey.ShouldNotBeNil)
		convey.So(resp.ActionSource.Type, convey.ShouldEqual, "tool")
	})
}

// TestQueryActions_HTTPError 测试 QueryActions HTTP 错误
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

		// Mock HTTP 错误
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("connection refused"))

		_, err := client.QueryActions(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestQueryInstanceSubgraph_Success 测试 QueryInstanceSubgraph 成功场景
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

		// Mock HTTP 成功响应
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, map[string]interface{}{
				"entries": []interface{}{},
			}, nil)

		resp, err := client.QueryInstanceSubgraph(ctx, req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp, convey.ShouldNotBeNil)
	})
}

// TestQueryInstanceSubgraph_HTTPError 测试 QueryInstanceSubgraph HTTP 错误
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

		// Mock HTTP 错误
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("connection refused"))

		_, err := client.QueryInstanceSubgraph(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestQueryObjectInstances_DownstreamBadRequestRemappedToBadRequest 回归 #235:
// 下游对非 vector 字段做 knn 时返回 4xx（"left field is not a vector field"）,
// 共享 http client 把它拍成 CommonExternalServerError「依赖服务异常」。驱动层须
// 把 4xx 重映射成 BadRequest 并保留下游 detail,让调用方看清是自己参数用错、不是
// 服务故障。
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
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
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

// TestQueryObjectInstances_DownstreamNotFoundKeepsCode 下游 404（如 kn_id/ot_id
// 不存在）须保留 404 语义,不被压成 400「参数错误」。
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
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusNotFound, nil, wrapped)

		_, err := client.QueryObjectInstances(ctx, req)
		var he *infraErr.HTTPError
		convey.So(errors.As(err, &he), convey.ShouldBeTrue)
		convey.So(he.HTTPCode, convey.ShouldEqual, http.StatusNotFound)
		convey.So(strings.Contains(he.Code, "NotFound"), convey.ShouldBeTrue)
		convey.So(strings.Contains(he.Code, "CommonExternalServerError"), convey.ShouldBeFalse)
	})
}

// TestQueryObjectInstances_DownstreamServerErrorUntouched 下游 5xx（真服务故障）
// 与传输错误(无 HTTP 码)保持原样,不误降为 400。
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
		mockHTTPClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusInternalServerError, nil, wrapped)

		_, err := client.QueryObjectInstances(ctx, req)
		var he *infraErr.HTTPError
		convey.So(errors.As(err, &he), convey.ShouldBeTrue)
		convey.So(he.HTTPCode, convey.ShouldEqual, http.StatusInternalServerError)
	})
}
