// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knactionrecall

import (
	"context"
	"errors"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// TestExecuteAction_Success 透传 ontology-query 的执行响应
func TestExecuteAction_Success(t *testing.T) {
	convey.Convey("TestExecuteAction_Success", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockOntologyQuery := mocks.NewMockDrivenOntologyQuery(ctrl)
		mockOperatorIntegration := mocks.NewMockDrivenOperatorIntegration(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knActionRecallServiceImpl{
			logger:              mockLogger,
			config:              &config.Config{},
			ontologyQuery:       mockOntologyQuery,
			operatorIntegration: mockOperatorIntegration,
		}

		ctx := context.Background()
		req := &interfaces.KnActionExecuteRequest{
			KnID:               "kn-001",
			AtID:               "at-001",
			InstanceIdentities: []map[string]any{{"key_id": "14"}},
			DynamicParams:      map[string]any{"message": "hi", "name": "zhangsan"},
		}

		// 断言透传的 execReq 携带了动态参数与实例标识
		mockOntologyQuery.EXPECT().ExecuteActions(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, r *interfaces.ExecuteActionsRequest) (*interfaces.ExecuteActionsResponse, error) {
				convey.So(r.KnID, convey.ShouldEqual, "kn-001")
				convey.So(r.AtID, convey.ShouldEqual, "at-001")
				convey.So(r.DynamicParams["message"], convey.ShouldEqual, "hi")
				convey.So(r.DynamicParams["name"], convey.ShouldEqual, "zhangsan")
				convey.So(len(r.InstanceIdentities), convey.ShouldEqual, 1)
				return &interfaces.ExecuteActionsResponse{
					ExecutionID: "exec-xyz",
					Status:      "pending",
					Message:     "Action execution started",
					CreatedAt:   123,
				}, nil
			})

		resp, err := service.ExecuteAction(ctx, req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.ExecutionID, convey.ShouldEqual, "exec-xyz")
		convey.So(resp.Status, convey.ShouldEqual, "pending")
		convey.So(resp.CreatedAt, convey.ShouldEqual, 123)
	})
}

// TestExecuteAction_Error ExecuteActions 失败时向上返回错误
func TestExecuteAction_Error(t *testing.T) {
	convey.Convey("TestExecuteAction_Error", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockOntologyQuery := mocks.NewMockDrivenOntologyQuery(ctrl)
		mockOperatorIntegration := mocks.NewMockDrivenOperatorIntegration(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knActionRecallServiceImpl{
			logger:              mockLogger,
			config:              &config.Config{},
			ontologyQuery:       mockOntologyQuery,
			operatorIntegration: mockOperatorIntegration,
		}

		ctx := context.Background()
		req := &interfaces.KnActionExecuteRequest{KnID: "kn-001", AtID: "at-001"}

		mockOntologyQuery.EXPECT().ExecuteActions(gomock.Any(), gomock.Any()).
			Return(nil, errors.New("execute actions failed"))

		_, err := service.ExecuteAction(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestGetActionExecution_Success 透传单次执行查询结果
func TestGetActionExecution_Success(t *testing.T) {
	convey.Convey("TestGetActionExecution_Success", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockOntologyQuery := mocks.NewMockDrivenOntologyQuery(ctrl)
		mockOperatorIntegration := mocks.NewMockDrivenOperatorIntegration(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knActionRecallServiceImpl{
			logger:              mockLogger,
			config:              &config.Config{},
			ontologyQuery:       mockOntologyQuery,
			operatorIntegration: mockOperatorIntegration,
		}

		mockOntologyQuery.EXPECT().GetActionExecution(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, r *interfaces.GetActionExecutionRequest) (map[string]any, error) {
				convey.So(r.KnID, convey.ShouldEqual, "kn-001")
				convey.So(r.ExecutionID, convey.ShouldEqual, "exec-001")
				return map[string]any{
					"id": "exec-001", "status": "completed",
					"execution_mode": "once", "target_count": 30, "total_count": 1,
					"results": []any{map[string]any{
						"status":   "success",
						"_display": "30 个目标实例合并为 1 次调用",
						"targets":  []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}},
					}},
				}, nil
			})

		resp, err := service.GetActionExecution(context.Background(), &interfaces.KnGetActionExecutionRequest{
			KnID: "kn-001", ExecutionID: "exec-001",
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp["status"], convey.ShouldEqual, "completed")
		// once 模式的执行必须把粒度与覆盖范围透给 Agent，否则 total_count=1 会被
		// 误读成「只处理了 1 个对象」
		convey.So(resp["execution_mode"], convey.ShouldEqual, "once")
		convey.So(resp["target_count"], convey.ShouldEqual, 30)
		r0 := resp["results"].([]any)[0].(map[string]any)
		convey.So(len(r0["targets"].([]any)), convey.ShouldEqual, 2)
	})
}

// TestGetActionExecution_TargetsCapped once 模式命中上万实例时,投影层必须截断 targets,
// 否则一次查询就能把 MB 级实例明细灌进 Agent 上下文
func TestGetActionExecution_TargetsCapped(t *testing.T) {
	convey.Convey("TestGetActionExecution_TargetsCapped", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockOntologyQuery := mocks.NewMockDrivenOntologyQuery(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knActionRecallServiceImpl{
			logger:        mockLogger,
			config:        &config.Config{},
			ontologyQuery: mockOntologyQuery,
		}

		targets := make([]any, 0, 5000)
		for i := 0; i < 5000; i++ {
			targets = append(targets, map[string]any{"_instance_identity": map[string]any{"id": i}})
		}
		mockOntologyQuery.EXPECT().GetActionExecution(gomock.Any(), gomock.Any()).
			Return(map[string]any{
				"id": "exec-agg", "status": "completed",
				"execution_mode": "once", "target_count": 5000, "total_count": 1,
				"results": []any{map[string]any{"status": "success", "targets": targets}},
			}, nil)

		resp, err := service.GetActionExecution(context.Background(), &interfaces.KnGetActionExecutionRequest{
			KnID: "kn-001", ExecutionID: "exec-agg",
		})
		convey.So(err, convey.ShouldBeNil)
		r0 := resp["results"].([]any)[0].(map[string]any)
		convey.So(len(r0["targets"].([]any)), convey.ShouldEqual, maxSlimTargets)
		convey.So(r0["targets_total"], convey.ShouldEqual, 5000)
		convey.So(r0["targets_truncated"], convey.ShouldBeTrue)
		// 覆盖总数仍然拿得到
		convey.So(resp["target_count"], convey.ShouldEqual, 5000)
	})
}

// TestListActionExecutions_Success 透传执行历史查询,过滤参数正确传递
func TestListActionExecutions_Success(t *testing.T) {
	convey.Convey("TestListActionExecutions_Success", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockOntologyQuery := mocks.NewMockDrivenOntologyQuery(ctrl)
		mockOperatorIntegration := mocks.NewMockDrivenOperatorIntegration(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knActionRecallServiceImpl{
			logger:              mockLogger,
			config:              &config.Config{},
			ontologyQuery:       mockOntologyQuery,
			operatorIntegration: mockOperatorIntegration,
		}

		// ontology-query 真实响应键名为 total_count / entries / search_after
		mockOntologyQuery.EXPECT().ListActionExecutions(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, r *interfaces.ListActionExecutionsRequest) (map[string]any, error) {
				convey.So(r.KnID, convey.ShouldEqual, "kn-001")
				convey.So(r.Status, convey.ShouldEqual, "completed")
				convey.So(r.Limit, convey.ShouldEqual, 10)
				// 游标透传：上一页 search_after 原样传到转发结构
				convey.So(len(r.SearchAfter), convey.ShouldEqual, 2)
				convey.So(r.SearchAfter[0], convey.ShouldEqual, int64(1784703617885))
				convey.So(r.SearchAfter[1], convey.ShouldEqual, "d9g6l08acb8s73c6l470")
				return map[string]any{
					"total_count": 1,
					"entries": []any{
						map[string]any{
							"id":                   "exec-001",
							"status":               "completed",
							"action_type_snapshot": map[string]any{"parameters": []any{"x"}},   // 重货，list 精简后应剔除
							"results":              []any{map[string]any{"status": "success"}}, // 逐对象明细只在单查里给
						},
					},
				}, nil
			})

		resp, err := service.ListActionExecutions(context.Background(), &interfaces.KnListActionExecutionsRequest{
			KnID: "kn-001", Status: "completed", Limit: 10,
			SearchAfter: []any{int64(1784703617885), "d9g6l08acb8s73c6l470"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp["total_count"], convey.ShouldEqual, 1)
		// 列表每条也应精简：保留 id/status，剔除 action_type_snapshot
		entries := resp["entries"].([]any)
		convey.So(len(entries), convey.ShouldEqual, 1)
		e0 := entries[0].(map[string]any)
		convey.So(e0["id"], convey.ShouldEqual, "exec-001")
		convey.So(e0["status"], convey.ShouldEqual, "completed")
		convey.So(e0["action_type_snapshot"], convey.ShouldBeNil)
		// 列表不带逐对象明细：键本身也不应存在，而不是留一个 null
		_, hasResults := e0["results"]
		convey.So(hasResults, convey.ShouldBeFalse)
	})
}

// TestSlimActionExecution 精简投影剔除重货、保留核心字段
func TestSlimActionExecution(t *testing.T) {
	convey.Convey("TestSlimActionExecution", t, func() {
		full := map[string]any{
			"id":                   "exec-1",
			"status":               "failed",
			"total_count":          1,
			"success_count":        0,
			"failed_count":         1,
			"dynamic_params":       map[string]any{"message": "hi"},
			"action_type_snapshot": map[string]any{"parameters": []any{"a", "b"}}, // 重货，应剔除
			"executor":             map[string]any{"id": "u1"},                    // 冗余，应剔除
			"executor_id":          "u1",                                          // 冗余，应剔除
			"action_source":        map[string]any{"tool_id": "t1"},               // 冗余，应剔除
			"results_limit":        1000,                                          // 分页元数据，应剔除
			"results": []any{
				map[string]any{
					"_instance_id":  "obj-14",
					"_display":      "1990 World Cup",
					"status":        "failed",
					"parameters":    map[string]any{"message": "hi", "name": "张三"},
					"error_message": "503",
					"duration_ms":   1374,
					"end_time":      123, // 逐对象里未列入保留集，应剔除
				},
			},
		}

		slim := slimActionExecution(full)

		convey.Convey("保留核心字段", func() {
			convey.So(slim["id"], convey.ShouldEqual, "exec-1")
			convey.So(slim["status"], convey.ShouldEqual, "failed")
			convey.So(slim["failed_count"], convey.ShouldEqual, 1)
			convey.So(slim["dynamic_params"], convey.ShouldNotBeNil)
		})
		convey.Convey("剔除重货字段", func() {
			convey.So(slim["action_type_snapshot"], convey.ShouldBeNil)
			convey.So(slim["executor"], convey.ShouldBeNil)
			convey.So(slim["executor_id"], convey.ShouldBeNil)
			convey.So(slim["action_source"], convey.ShouldBeNil)
			convey.So(slim["results_limit"], convey.ShouldBeNil)
		})
		convey.Convey("逐对象结果精简", func() {
			results := slim["results"].([]any)
			convey.So(len(results), convey.ShouldEqual, 1)
			r := results[0].(map[string]any)
			convey.So(r["_instance_id"], convey.ShouldEqual, "obj-14")
			convey.So(r["status"], convey.ShouldEqual, "failed")
			convey.So(r["error_message"], convey.ShouldEqual, "503")
			convey.So(r["parameters"], convey.ShouldNotBeNil)
			// 未列入保留集的字段被剔除
			convey.So(r["end_time"], convey.ShouldBeNil)
		})
	})
}
