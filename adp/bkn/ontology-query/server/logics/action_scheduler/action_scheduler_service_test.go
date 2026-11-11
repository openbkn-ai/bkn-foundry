// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_scheduler

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
)

func Test_buildExecutionParams(t *testing.T) {
	Convey("Test buildExecutionParams", t, func() {
		s := &actionSchedulerService{}

		Convey("should get value from object property (VALUE_FROM_PROP)", func() {
			actionType := &interfaces.ActionType{
				Parameters: []interfaces.Parameter{
					{
						Name:      "target_ip",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP,
						Value:     "pod_ip",
					},
				}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}

			objData := map[string]any{
				"_instance_id": "test-instance",
				"_instance_identity": map[string]any{
					"pod_ip":   "192.168.1.1",
					"pod_name": "test-pod",
				},
				"_display": "test-pod",
				"pod_ip":   "192.168.1.1",
				"pod_name": "test-pod",
			}

			params, err := s.buildExecutionParams(actionType, objData, nil)

			So(err, ShouldBeNil)
			So(params["target_ip"], ShouldEqual, "192.168.1.1")
		})

		Convey("should get value from constant (VALUE_FROM_CONST)", func() {
			actionType := &interfaces.ActionType{
				Parameters: []interfaces.Parameter{
					{
						Name:      "timeout",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST,
						Value:     60,
					},
				}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}

			objData := map[string]any{}

			params, err := s.buildExecutionParams(actionType, objData, nil)

			So(err, ShouldBeNil)
			So(params["timeout"], ShouldEqual, 60)
		})

		Convey("should get value from dynamic params (VALUE_FROM_INPUT)", func() {
			actionType := &interfaces.ActionType{
				Parameters: []interfaces.Parameter{
					{
						Name:      "Authorization",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
					},
				}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}
			objData := map[string]any{}
			dynamicParams := map[string]any{
				"Authorization": "Bearer token123",
			}

			params, err := s.buildExecutionParams(actionType, objData, dynamicParams)

			So(err, ShouldBeNil)
			So(params["Authorization"], ShouldEqual, "Bearer token123")
		})

		Convey("should handle mixed parameter sources", func() {
			actionType := &interfaces.ActionType{
				Parameters: []interfaces.Parameter{
					{
						Name:      "target_ip",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP,
						Value:     "pod_ip",
					},
					{
						Name:      "timeout",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST,
						Value:     30,
					},
					{
						Name:      "token",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
					},
				}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}
			objData := map[string]any{
				"_instance_id": "test-instance",
				"_instance_identity": map[string]any{
					"pod_ip": "10.0.0.1",
				},
				"_display": "test-pod",
				"pod_ip":   "10.0.0.1",
			}
			dynamicParams := map[string]any{
				"token": "abc123",
			}

			params, err := s.buildExecutionParams(actionType, objData, dynamicParams)

			So(err, ShouldBeNil)
			So(params["target_ip"], ShouldEqual, "10.0.0.1")
			So(params["timeout"], ShouldEqual, 30)
			So(params["token"], ShouldEqual, "abc123")
		})

		Convey("should handle missing property in identity", func() {
			actionType := &interfaces.ActionType{
				Parameters: []interfaces.Parameter{
					{
						Name:      "target_ip",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP,
						Value:     "pod_ip",
					},
				}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}
			objData := map[string]any{
				"_instance_id": "test-instance",
				"_instance_identity": map[string]any{
					"pod_name": "test-pod", // pod_ip is missing
				},
				"_display": "test-pod",
				"pod_name": "test-pod", // pod_ip is missing
			}

			params, err := s.buildExecutionParams(actionType, objData, nil)

			So(err, ShouldNotBeNil)
			_, exists := params["target_ip"]
			So(exists, ShouldBeFalse) // Parameter should not be set if property is missing
		})

		Convey("should handle missing dynamic param", func() {
			actionType := &interfaces.ActionType{
				Parameters: []interfaces.Parameter{
					{
						Name:      "token",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
					},
				}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}
			objData := map[string]any{}

			params, err := s.buildExecutionParams(actionType, objData, nil)

			So(err, ShouldNotBeNil)
			_, exists := params["token"]
			So(exists, ShouldBeFalse)
		})

		Convey("should handle empty parameters", func() {
			actionType := &interfaces.ActionType{
				Parameters: []interfaces.Parameter{}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}
			objData := map[string]any{}

			params, err := s.buildExecutionParams(actionType, objData, nil)

			So(err, ShouldBeNil)
			So(len(params), ShouldEqual, 0)
		})

		Convey("should handle nested dynamic param name (dot-separated)", func() {
			actionType := &interfaces.ActionType{
				Parameters: []interfaces.Parameter{
					{
						Name:      "props.headers",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
					},
				}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}
			objData := map[string]any{}
			dynamicParams := map[string]any{
				"props": map[string]any{
					"headers": map[string]any{"Authorization": "Bearer xxx"},
				},
			}

			params, err := s.buildExecutionParams(actionType, objData, dynamicParams)

			So(err, ShouldBeNil)
			propsMap, ok := params["props"].(map[string]any)
			So(ok, ShouldBeTrue)
			So(propsMap["headers"], ShouldResemble, map[string]any{"Authorization": "Bearer xxx"})
		})

		Convey("should handle nested property name (dot-separated)", func() {
			actionType := &interfaces.ActionType{
				Parameters: []interfaces.Parameter{
					{
						Name:      "target.ip",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP,
						Value:     "network.ip",
					},
				}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}
			objData := map[string]any{
				"network": map[string]any{
					"ip": "10.0.0.1",
				},
			}

			params, err := s.buildExecutionParams(actionType, objData, nil)

			So(err, ShouldBeNil)
			targetMap, ok := params["target"].(map[string]any)
			So(ok, ShouldBeTrue)
			So(targetMap["ip"], ShouldEqual, "10.0.0.1")
		})

		Convey("should handle mixed nested and flat params", func() {
			actionType := &interfaces.ActionType{
				Parameters: []interfaces.Parameter{
					{
						Name:      "props.headers",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
					},
					{
						Name:      "query",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST,
						Value:     "test-query",
					},
					{
						Name:      "target_ip",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP,
						Value:     "pod_ip",
					},
				}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}
			objData := map[string]any{
				"pod_ip": "192.168.1.1",
			}
			dynamicParams := map[string]any{
				"props": map[string]any{
					"headers": "Bearer token",
				},
			}

			params, err := s.buildExecutionParams(actionType, objData, dynamicParams)

			So(err, ShouldBeNil)
			propsMap, ok := params["props"].(map[string]any)
			So(ok, ShouldBeTrue)
			So(propsMap["headers"], ShouldEqual, "Bearer token")
			So(params["query"], ShouldEqual, "test-query")
			So(params["target_ip"], ShouldEqual, "192.168.1.1")
		})
	})
}

func Test_ActionExecutionRequest_Validation(t *testing.T) {
	Convey("Test ActionExecutionRequest", t, func() {
		Convey("should have required fields", func() {
			req := &interfaces.ActionExecutionRequest{
				KNID:         "kn_001",
				ActionTypeID: "at_001",
				InstanceIdentities: []map[string]any{
					{"pod_ip": "192.168.1.1"},
				},
			}

			So(req.KNID, ShouldEqual, "kn_001")
			So(req.ActionTypeID, ShouldEqual, "at_001")
			So(len(req.InstanceIdentities), ShouldEqual, 1)
		})

		Convey("should support branch field", func() {
			req := &interfaces.ActionExecutionRequest{
				KNID:         "kn_001",
				Branch:       "feature/test",
				ActionTypeID: "at_001",
				InstanceIdentities: []map[string]any{
					{"pod_ip": "192.168.1.1"},
				},
			}

			So(req.KNID, ShouldEqual, "kn_001")
			So(req.Branch, ShouldEqual, "feature/test")
			So(req.ActionTypeID, ShouldEqual, "at_001")
		})

		Convey("should default branch to empty string when not set", func() {
			req := &interfaces.ActionExecutionRequest{
				KNID:         "kn_001",
				ActionTypeID: "at_001",
			}

			So(req.Branch, ShouldEqual, "")
		})

		Convey("should handle multiple objects", func() {
			req := &interfaces.ActionExecutionRequest{
				InstanceIdentities: []map[string]any{
					{"pod_ip": "192.168.1.1", "id": 1},
					{"pod_ip": "192.168.1.2", "id": 2},
					{"pod_ip": "192.168.1.3", "id": 3},
				},
			}

			So(len(req.InstanceIdentities), ShouldEqual, 3)
		})

		Convey("should handle dynamic params", func() {
			req := &interfaces.ActionExecutionRequest{
				DynamicParams: map[string]any{
					"Authorization": "Bearer xxx",
					"Timeout":       60,
				},
			}

			So(req.DynamicParams["Authorization"], ShouldEqual, "Bearer xxx")
			So(req.DynamicParams["Timeout"], ShouldEqual, 60)
		})
	})
}

func Test_ActionExecutionResponse(t *testing.T) {
	Convey("Test ActionExecutionResponse", t, func() {
		Convey("should have correct structure", func() {
			resp := &interfaces.ActionExecutionResponse{
				ExecutionID: "exec_123",
				Status:      interfaces.ExecutionStatusPending,
				Message:     "Action execution started",
				CreatedAt:   1704067200000,
			}

			So(resp.ExecutionID, ShouldEqual, "exec_123")
			So(resp.Status, ShouldEqual, "pending")
			So(resp.Message, ShouldEqual, "Action execution started")
			So(resp.CreatedAt, ShouldEqual, int64(1704067200000))
		})
	})
}

func Test_ObjectExecutionResult(t *testing.T) {
	Convey("Test ObjectExecutionResult", t, func() {
		Convey("should represent success result", func() {
			result := interfaces.ObjectExecutionResult{
				ObjectSystemInfo: interfaces.ObjectSystemInfo{
					InstanceID:       "",
					InstanceIdentity: map[string]any{"pod_ip": "192.168.1.1"},
					Display:          "",
				},
				Status:     interfaces.ObjectStatusSuccess,
				Parameters: map[string]any{"target_ip": "192.168.1.1"},
				Result:     map[string]any{"message": "OK"},
				DurationMs: 1200,
			}

			So(result.Status, ShouldEqual, "success")
			So(result.ErrorMessage, ShouldEqual, "")
			So(result.DurationMs, ShouldEqual, int64(1200))
		})

		Convey("should represent failed result", func() {
			result := interfaces.ObjectExecutionResult{
				ObjectSystemInfo: interfaces.ObjectSystemInfo{
					InstanceID:       "",
					InstanceIdentity: map[string]any{"pod_ip": "192.168.1.2"},
					Display:          "",
				},
				Status:       interfaces.ObjectStatusFailed,
				Parameters:   map[string]any{"target_ip": "192.168.1.2"},
				ErrorMessage: "Connection timeout",
				DurationMs:   5000,
			}

			So(result.Status, ShouldEqual, "failed")
			So(result.ErrorMessage, ShouldEqual, "Connection timeout")
			So(result.Result, ShouldBeNil)
		})
	})
}

func Test_ActionSource_Types(t *testing.T) {
	Convey("Test ActionSource types", t, func() {
		Convey("should handle Tool source", func() {
			source := interfaces.ActionSource{
				Type:   interfaces.ActionSourceTypeTool,
				BoxID:  "box_001",
				ToolID: "tool_001",
			}

			So(source.Type, ShouldEqual, "tool")
			So(source.BoxID, ShouldEqual, "box_001")
			So(source.ToolID, ShouldEqual, "tool_001")
		})

		Convey("should handle MCP source", func() {
			source := interfaces.ActionSource{
				Type:     interfaces.ActionSourceTypeMCP,
				McpID:    "mcp_001",
				ToolName: "restart_service",
			}

			So(source.Type, ShouldEqual, "mcp")
			So(source.McpID, ShouldEqual, "mcp_001")
			So(source.ToolName, ShouldEqual, "restart_service")
		})
	})
}

func Test_ActionExecution_Snapshot(t *testing.T) {
	Convey("Test ActionExecution with ActionTypeSnapshot", t, func() {
		Convey("should store action type snapshot", func() {
			// Mock the raw action type configuration obtained from manager.
			actionTypeSnapshot := map[string]any{
				"id":             "at_001",
				"name":           "restart_pod",
				"action_type":    "modify",
				"object_type_id": "ot_pod",
				"tags":           []string{"k8s", "pod"},
				"comment":        "重启 Pod",
				"icon":           "restart",
				"color":          "#FF5722",
				"condition": map[string]any{
					"field":    "status",
					"operator": "eq",
					"value":    "Running",
				},
				"parameters": []map[string]any{
					{"name": "timeout", "value_from": "const", "value": 30},
				},
				"schedule": map[string]any{
					"type":       "manual",
					"expression": "",
				},
				"creator":     "user_123",
				"create_time": int64(1704000000000),
				"updater":     "user_456",
				"update_time": int64(1704100000000),
			}

			execution := &interfaces.ActionExecution{
				ID:                 "exec_001",
				KNID:               "kn_001",
				ActionTypeID:       "at_001",
				ActionTypeName:     "restart_pod",
				ActionSourceType:   interfaces.ActionSourceTypeTool,
				Status:             interfaces.ExecutionStatusPending,
				TotalCount:         1,
				ActionTypeSnapshot: actionTypeSnapshot,
			}

			So(execution.ActionTypeSnapshot, ShouldNotBeNil)
			So(execution.ActionTypeSnapshot["id"], ShouldEqual, "at_001")
			So(execution.ActionTypeSnapshot["name"], ShouldEqual, "restart_pod")
			So(execution.ActionTypeSnapshot["tags"], ShouldNotBeNil)
			So(execution.ActionTypeSnapshot["condition"], ShouldNotBeNil)
			So(execution.ActionTypeSnapshot["parameters"], ShouldNotBeNil)
			So(execution.ActionTypeSnapshot["creator"], ShouldEqual, "user_123")
			So(execution.ActionTypeSnapshot["create_time"], ShouldEqual, int64(1704000000000))
		})

		Convey("should allow nil snapshot for backward compatibility", func() {
			execution := &interfaces.ActionExecution{
				ID:                 "exec_002",
				KNID:               "kn_001",
				ActionTypeID:       "at_001",
				ActionTypeName:     "restart_pod",
				Status:             interfaces.ExecutionStatusCompleted,
				ActionTypeSnapshot: nil, // Old data may not have a snapshot.
			}

			So(execution.ActionTypeSnapshot, ShouldBeNil)
			So(execution.ActionTypeID, ShouldEqual, "at_001")
		})
	})
}

func Test_executeAsync_ContextAndProgress(t *testing.T) {
	Convey("executeAsync should restore trace context and flush small-run progress", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
		aoAccess.EXPECT().GetBoxMetadataType(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ProxyTargetTypeToolBox, nil).AnyTimes()
		logsService := omock.NewMockActionLogsService(mockCtrl)
		service := &actionSchedulerService{
			aoAccess:    aoAccess,
			logsService: logsService,
			permissions: &actionPermissionStub{},
		}

		execution := &interfaces.ActionExecution{
			ID:           "exec_001",
			KNID:         "kn_001",
			Status:       interfaces.ExecutionStatusPending,
			TotalCount:   1,
			StartTime:    1700000000000,
			SuccessCount: 0,
			FailedCount:  0,
			Executor: interfaces.AccountInfo{
				ID:   "user_001",
				Type: "user",
			},
		}
		actionType := &interfaces.ActionType{
			ActionSource: interfaces.ActionSource{
				Type:   interfaces.ActionSourceTypeTool,
				BoxID:  "box_001",
				ToolID: "tool_001",
			},
			Parameters: []interfaces.Parameter{}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
		}
		attachTestActionProxySnapshot(t, execution, actionType)
		req := &interfaces.ActionExecutionRequest{
			Instances: []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{"id": "1"}},
			},
			ObjDatas: []map[string]any{
				{"id": "1"},
			},
		}

		var progressUpdates []*interfaces.ExecutionProgress
		var stored []interfaces.ObjectExecutionResult
		captureAppendedResults(logsService, "exec_001", &stored)
		logsService.EXPECT().MarkExecutionRunning(gomock.Any(), "kn_001", "exec_001").Return(nil)
		logsService.EXPECT().UpdateExecutionProgress(gomock.Any(), "kn_001", "exec_001", gomock.Any()).DoAndReturn(
			func(ctx context.Context, knID, execID string, progress *interfaces.ExecutionProgress) error {
				progressUpdates = append(progressUpdates, progress)
				return nil
			}).AnyTimes()
		logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_001", gomock.Any()).Return(nil)
		logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ExecutionStatusRunning, nil).AnyTimes()
		aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_001", "tool_001", gomock.Any()).DoAndReturn(
			func(ctx context.Context, boxID, toolID string, execRequest interfaces.ToolExecutionRequest) (any, error) {
				So(ctx.Value(interfaces.ACCOUNT_INFO_KEY), ShouldResemble, execution.Executor)
				_, ok := ctx.Deadline()
				So(ok, ShouldBeTrue)
				credential, ok := interfaces.CallerRuntimeCredentialFromContext(ctx)
				So(ok, ShouldBeTrue)
				So(credential.Authorization, ShouldEqual, "Bearer caller-token")
				return map[string]any{"checked": true}, nil
			})

		service.executeAsync(execution, actionType, req,
			interfaces.CallerRuntimeCredential{Authorization: "Bearer caller-token"})

		So(len(progressUpdates), ShouldBeGreaterThanOrEqualTo, 1)
		So(progressUpdates[0].SuccessCount, ShouldEqual, 1)
		So(progressUpdates[0].FailedCount, ShouldEqual, 0)
		results := stored
		So(len(results), ShouldEqual, 1)
		So(results[0].Status, ShouldEqual, interfaces.ObjectStatusSuccess)
	})
}

func Test_resolveExecutionMode(t *testing.T) {
	Convey("resolveExecutionMode 按参数来源判定执行粒度", t, func() {
		Convey("含 property 参数时逐实例执行", func() {
			at := &interfaces.ActionType{Parameters: []interfaces.Parameter{
				{Name: "text", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT},
				{Name: "order_no", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "order_no"},
			}, OperationConfig: &interfaces.ActionOperationConfig{Type: "object"},
			}
			So(resolveExecutionMode(at), ShouldEqual, interfaces.ExecutionModePerInstance)
		})

		Convey("参数全部来自 input/const 时只执行一次", func() {
			at := &interfaces.ActionType{Parameters: []interfaces.Parameter{
				{Name: "source", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST, Value: "bkn"},
				{Name: "text", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT},
			}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}
			So(resolveExecutionMode(at), ShouldEqual, interfaces.ExecutionModeOnce)
		})

		Convey("无参数时只执行一次", func() {
			at := &interfaces.ActionType{Parameters: []interfaces.Parameter{}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"}}
			So(resolveExecutionMode(at), ShouldEqual, interfaces.ExecutionModeOnce)
		})
	})
}

func Test_executeAsync_AggregatedInvokesToolOnce(t *testing.T) {
	Convey("聚合参数命中多个实例时只调用一次工具（#724）", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
		aoAccess.EXPECT().GetBoxMetadataType(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ProxyTargetTypeToolBox, nil).AnyTimes()
		logsService := omock.NewMockActionLogsService(mockCtrl)
		service := &actionSchedulerService{aoAccess: aoAccess, logsService: logsService, permissions: &actionPermissionStub{}}

		execution := &interfaces.ActionExecution{
			ID:            "exec_agg",
			KNID:          "kn_001",
			Status:        interfaces.ExecutionStatusPending,
			ExecutionMode: interfaces.ExecutionModeOnce,
			TargetCount:   7,
			TotalCount:    1,
			StartTime:     1700000000000,
		}
		actionType := &interfaces.ActionType{
			ActionSource: interfaces.ActionSource{
				Type:   interfaces.ActionSourceTypeTool,
				BoxID:  "box_001",
				ToolID: "tool_001",
			},
			Parameters: []interfaces.Parameter{
				{Name: "source", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST, Value: "bkn"},
				{Name: "text", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT},
			}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
		}
		attachTestActionProxySnapshot(t, execution, actionType)

		instances := make([]interfaces.ObjectSystemInfo, 0, 7)
		objDatas := make([]map[string]any, 0, 7)
		for i := 0; i < 7; i++ {
			id := fmt.Sprintf("order_%d", i)
			instances = append(instances, interfaces.ObjectSystemInfo{InstanceIdentity: map[string]any{"id": id}})
			objDatas = append(objDatas, map[string]any{"id": id})
		}
		req := &interfaces.ActionExecutionRequest{
			Instances:     instances,
			ObjDatas:      objDatas,
			DynamicParams: map[string]any{"text": "订单编号,订单状态,下单时间\n..."},
		}

		var finalOutcome *interfaces.ExecutionOutcome
		var stored []interfaces.ObjectExecutionResult
		captureAppendedResults(logsService, "exec_agg", &stored)
		logsService.EXPECT().MarkExecutionRunning(gomock.Any(), "kn_001", "exec_agg").Return(nil).AnyTimes()
		logsService.EXPECT().UpdateExecutionProgress(gomock.Any(), "kn_001", "exec_agg", gomock.Any()).Return(nil).AnyTimes()
		logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_agg", gomock.Any()).DoAndReturn(
			func(ctx context.Context, knID, execID string, outcome *interfaces.ExecutionOutcome) error {
				finalOutcome = outcome
				return nil
			})
		logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ExecutionStatusRunning, nil).AnyTimes()

		// Core assertion: seven target instances produce only one tool call.
		aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_001", "tool_001", gomock.Any()).DoAndReturn(
			func(ctx context.Context, boxID, toolID string, execRequest interfaces.ToolExecutionRequest) (any, error) {
				return map[string]any{"message_id": "m_1"}, nil
			}).Times(1)

		service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{})

		So(finalOutcome, ShouldNotBeNil)
		So(finalOutcome.Status, ShouldEqual, interfaces.ExecutionStatusCompleted)
		So(finalOutcome.SuccessCount, ShouldEqual, 1)
		So(finalOutcome.FailedCount, ShouldEqual, 0)

		results := stored
		So(len(results), ShouldEqual, 1)
		So(results[0].Status, ShouldEqual, interfaces.ObjectStatusSuccess)
		// Target instances are not lost.
		So(len(results[0].Targets), ShouldEqual, 7)
		So(results[0].Parameters["text"], ShouldEqual, "订单编号,订单状态,下单时间\n...")
	})
}

// aggregatedOnceFixture builds an ExecutionModeOnce execution whose parameters are all
// instance-independent, so the scheduler collapses the matched instances into one invocation.
func aggregatedOnceFixture(t *testing.T, execID string) (*interfaces.ActionExecution, *interfaces.ActionType, *interfaces.ActionExecutionRequest) {
	execution := &interfaces.ActionExecution{
		ID:            execID,
		KNID:          "kn_001",
		Status:        interfaces.ExecutionStatusRunning,
		ExecutionMode: interfaces.ExecutionModeOnce,
		TargetCount:   3,
		TotalCount:    1,
		StartTime:     1700000000000,
	}
	actionType := &interfaces.ActionType{
		ActionSource: interfaces.ActionSource{
			Type:   interfaces.ActionSourceTypeTool,
			BoxID:  "box_001",
			ToolID: "tool_001",
		},
		Parameters: []interfaces.Parameter{
			{Name: "text", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT},
		}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
	}
	attachTestActionProxySnapshot(t, execution, actionType)
	req := &interfaces.ActionExecutionRequest{
		Instances: []interfaces.ObjectSystemInfo{
			{InstanceIdentity: map[string]any{"id": "1"}},
			{InstanceIdentity: map[string]any{"id": "2"}},
			{InstanceIdentity: map[string]any{"id": "3"}},
		},
		ObjDatas:      []map[string]any{{"id": "1"}, {"id": "2"}, {"id": "3"}},
		DynamicParams: map[string]any{"text": "汇总消息"},
	}
	return execution, actionType, req
}

func Test_executeAsync_AggregatedCancelledBeforeInvocation(t *testing.T) {
	Convey("聚合执行在调用发出前被取消：不得再发出工具调用", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
		aoAccess.EXPECT().GetBoxMetadataType(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ProxyTargetTypeToolBox, nil).AnyTimes()
		logsService := omock.NewMockActionLogsService(mockCtrl)
		service := &actionSchedulerService{aoAccess: aoAccess, logsService: logsService, permissions: &actionPermissionStub{}}

		execution, actionType, req := aggregatedOnceFixture(t, "exec_cancel_before")

		var finalOutcome *interfaces.ExecutionOutcome
		var stored []interfaces.ObjectExecutionResult
		captureAppendedResults(logsService, "exec_cancel_before", &stored)
		logsService.EXPECT().MarkExecutionRunning(gomock.Any(), "kn_001", "exec_cancel_before").Return(nil).AnyTimes()
		logsService.EXPECT().UpdateExecutionProgress(gomock.Any(), "kn_001", "exec_cancel_before", gomock.Any()).Return(nil).AnyTimes()
		logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_cancel_before", gomock.Any()).DoAndReturn(
			func(ctx context.Context, knID, execID string, outcome *interfaces.ExecutionOutcome) error {
				finalOutcome = outcome
				return nil
			})
		logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ExecutionStatusCancelled, nil).AnyTimes()

		// After cancellation, the tool must not be called at all.
		aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

		service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{})

		So(finalOutcome, ShouldNotBeNil)
		So(finalOutcome.Status, ShouldEqual, interfaces.ExecutionStatusCancelled)
		So(finalOutcome.SuccessCount, ShouldEqual, 0)
		So(finalOutcome.FailedCount, ShouldEqual, 0)
		results := stored
		So(results[0].Status, ShouldEqual, interfaces.ObjectStatusCancelled)
	})
}

func Test_executeAsync_AggregatedCancelledDuringInvocation(t *testing.T) {
	Convey("聚合执行在调用途中被取消：终态保持 cancelled，且已发出的调用结果照记", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
		aoAccess.EXPECT().GetBoxMetadataType(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ProxyTargetTypeToolBox, nil).AnyTimes()
		logsService := omock.NewMockActionLogsService(mockCtrl)
		service := &actionSchedulerService{aoAccess: aoAccess, logsService: logsService, permissions: &actionPermissionStub{}}

		execution, actionType, req := aggregatedOnceFixture(t, "exec_cancel_during")

		var finalOutcome *interfaces.ExecutionOutcome
		var stored []interfaces.ObjectExecutionResult
		captureAppendedResults(logsService, "exec_cancel_during", &stored)
		logsService.EXPECT().MarkExecutionRunning(gomock.Any(), "kn_001", "exec_cancel_during").Return(nil).AnyTimes()
		logsService.EXPECT().UpdateExecutionProgress(gomock.Any(), "kn_001", "exec_cancel_during", gomock.Any()).Return(nil).AnyTimes()
		logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_cancel_during", gomock.Any()).DoAndReturn(
			func(ctx context.Context, knID, execID string, outcome *interfaces.ExecutionOutcome) error {
				finalOutcome = outcome
				return nil
			})

		// It is still running before the call is sent and is canceled after the call returns.
		gomock.InOrder(
			logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ExecutionStatusRunning, nil),
			logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ExecutionStatusCancelled, nil),
		)

		aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_001", "tool_001", gomock.Any()).
			Return(map[string]any{"message_id": "m_1"}, nil).Times(1)

		service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{})

		So(finalOutcome, ShouldNotBeNil)
		So(finalOutcome.Status, ShouldEqual, interfaces.ExecutionStatusCancelled)
		// The side effect has already occurred, so the result must be recorded.
		results := stored
		So(results[0].Status, ShouldEqual, interfaces.ObjectStatusSuccess)
		So(finalOutcome.SuccessCount, ShouldEqual, 1)
	})
}

func Test_executeAsync_PerInstanceStillFansOut(t *testing.T) {
	Convey("含 property 参数时仍逐实例执行，计数与改动前一致", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
		aoAccess.EXPECT().GetBoxMetadataType(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ProxyTargetTypeToolBox, nil).AnyTimes()
		logsService := omock.NewMockActionLogsService(mockCtrl)
		service := &actionSchedulerService{aoAccess: aoAccess, logsService: logsService, permissions: &actionPermissionStub{}}

		execution := &interfaces.ActionExecution{
			ID:            "exec_fan",
			KNID:          "kn_001",
			Status:        interfaces.ExecutionStatusPending,
			ExecutionMode: interfaces.ExecutionModePerInstance,
			TargetCount:   3,
			TotalCount:    3,
			StartTime:     1700000000000,
		}
		actionType := &interfaces.ActionType{
			ActionSource: interfaces.ActionSource{
				Type:   interfaces.ActionSourceTypeTool,
				BoxID:  "box_001",
				ToolID: "tool_001",
			},
			Parameters: []interfaces.Parameter{
				{Name: "order_no", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "order_no"},
			}, OperationConfig: &interfaces.ActionOperationConfig{Type: "object"},
		}
		attachTestActionProxySnapshot(t, execution, actionType)
		req := &interfaces.ActionExecutionRequest{
			Instances: []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{"id": "1"}},
				{InstanceIdentity: map[string]any{"id": "2"}},
				{InstanceIdentity: map[string]any{"id": "3"}},
			},
			ObjDatas: []map[string]any{
				{"order_no": "A"}, {"order_no": "B"}, {"order_no": "C"},
			},
		}

		var finalOutcome *interfaces.ExecutionOutcome
		var stored []interfaces.ObjectExecutionResult
		captureAppendedResults(logsService, "exec_fan", &stored)
		logsService.EXPECT().MarkExecutionRunning(gomock.Any(), "kn_001", "exec_fan").Return(nil).AnyTimes()
		logsService.EXPECT().UpdateExecutionProgress(gomock.Any(), "kn_001", "exec_fan", gomock.Any()).Return(nil).AnyTimes()
		logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_fan", gomock.Any()).DoAndReturn(
			func(ctx context.Context, knID, execID string, outcome *interfaces.ExecutionOutcome) error {
				finalOutcome = outcome
				return nil
			})
		logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ExecutionStatusRunning, nil).AnyTimes()

		sentOrderNos := []any{}
		aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_001", "tool_001", gomock.Any()).DoAndReturn(
			func(ctx context.Context, boxID, toolID string, execRequest interfaces.ToolExecutionRequest) (any, error) {
				sentOrderNos = append(sentOrderNos, execRequest.Body["order_no"])
				return map[string]any{"ok": true}, nil
			}).Times(3)

		service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{})

		So(sentOrderNos, ShouldResemble, []any{"A", "B", "C"})
		So(finalOutcome.SuccessCount, ShouldEqual, 3)
		results := stored
		So(len(results), ShouldEqual, 3)
		So(results[0].Targets, ShouldBeNil)
	})
}

func Test_executeAsync_PerInstanceCancelledMidway(t *testing.T) {
	Convey("per_instance execution cancelled midway stops invoking and records the rest as cancelled (#790)", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
		logsService := omock.NewMockActionLogsService(mockCtrl)
		service := &actionSchedulerService{aoAccess: aoAccess, logsService: logsService, permissions: &actionPermissionStub{}}

		execution := &interfaces.ActionExecution{
			ID:            "exec_cancel_mid",
			KNID:          "kn_001",
			Status:        interfaces.ExecutionStatusPending,
			ExecutionMode: interfaces.ExecutionModePerInstance,
			TargetCount:   3,
			TotalCount:    3,
			StartTime:     1700000000000,
		}
		actionType := &interfaces.ActionType{
			ActionSource: interfaces.ActionSource{
				Type:   interfaces.ActionSourceTypeTool,
				BoxID:  "box_001",
				ToolID: "tool_001",
			},
			Parameters: []interfaces.Parameter{
				{Name: "order_no", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "order_no"},
			}, OperationConfig: &interfaces.ActionOperationConfig{Type: "object"},
		}
		attachTestActionProxySnapshot(t, execution, actionType)
		req := &interfaces.ActionExecutionRequest{
			Instances: []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{"id": "1"}},
				{InstanceIdentity: map[string]any{"id": "2"}},
				{InstanceIdentity: map[string]any{"id": "3"}},
			},
			ObjDatas: []map[string]any{
				{"order_no": "A"}, {"order_no": "B"}, {"order_no": "C"},
			},
		}

		var finalOutcome *interfaces.ExecutionOutcome
		var stored []interfaces.ObjectExecutionResult
		captureAppendedResults(logsService, "exec_cancel_mid", &stored)
		logsService.EXPECT().MarkExecutionRunning(gomock.Any(), "kn_001", "exec_cancel_mid").Return(nil)
		// Progress writes only ever carry counters and results; the type has no status field.
		logsService.EXPECT().UpdateExecutionProgress(gomock.Any(), "kn_001", "exec_cancel_mid", gomock.Any()).Return(nil).AnyTimes()
		logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_cancel_mid", gomock.Any()).DoAndReturn(
			func(ctx context.Context, knID, execID string, outcome *interfaces.ExecutionOutcome) error {
				finalOutcome = outcome
				return nil
			})
		// Small runs check before every instance: running for the first, cancelled for the second.
		gomock.InOrder(
			logsService.EXPECT().GetExecutionStatus(gomock.Any(), "kn_001", "exec_cancel_mid").Return(interfaces.ExecutionStatusRunning, nil),
			logsService.EXPECT().GetExecutionStatus(gomock.Any(), "kn_001", "exec_cancel_mid").Return(interfaces.ExecutionStatusCancelled, nil),
		)

		aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_001", "tool_001", gomock.Any()).
			Return(map[string]any{"ok": true}, nil).Times(1)

		service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{})

		So(finalOutcome, ShouldNotBeNil)
		So(finalOutcome.Status, ShouldEqual, interfaces.ExecutionStatusCancelled)
		So(finalOutcome.SuccessCount, ShouldEqual, 1)
		So(len(stored), ShouldEqual, 3)
		So(stored[0].Status, ShouldEqual, interfaces.ObjectStatusSuccess)
		So(stored[1].Status, ShouldEqual, interfaces.ObjectStatusCancelled)
		So(stored[2].Status, ShouldEqual, interfaces.ObjectStatusCancelled)
	})
}

func Test_ExecuteAction_InputDynamicParamsValidation(t *testing.T) {
	Convey("行动执行：行动类含 input 参数时，dynamic_params 未给齐则返回 400", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		omAccess := omock.NewMockOntologyManagerAccess(mockCtrl)

		logics.OMA = omAccess

		service := &actionSchedulerService{
			appSetting: appSetting,
			omAccess:   omAccess,
		}

		ctx := context.Background()
		baseActionType := func(extraParams ...interfaces.Parameter) interfaces.ActionType {
			p := []interfaces.Parameter{{Name: "token", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT}}
			p = append(p, extraParams...)
			return interfaces.ActionType{
				ATID:       "at_001",
				ATName:     "needs_input",
				Parameters: p, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
			}
		}

		Convey("未提供 dynamic_params", func() {
			actionType := baseActionType()
			omAccess.EXPECT().GetActionType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(actionType, map[string]any{}, true, nil)

			req := &interfaces.ActionExecutionRequest{
				KNID:               "kn_001",
				ActionTypeID:       "at_001",
				InstanceIdentities: []map[string]any{{"id": "1"}},
			}

			_, err := service.ExecuteAction(ctx, req)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*httperrors.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.ErrorCode, ShouldEqual, oerrors.OntologyQuery_ActionExecution_InvalidParameter)
		})

		Convey("仅提供部分 input（缺少另一个参数）", func() {
			actionType := baseActionType(interfaces.Parameter{
				Name: "other", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
			})
			omAccess.EXPECT().GetActionType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(actionType, map[string]any{}, true, nil)

			req := &interfaces.ActionExecutionRequest{
				KNID:               "kn_001",
				ActionTypeID:       "at_001",
				InstanceIdentities: []map[string]any{{"id": "1"}},
				DynamicParams: map[string]any{
					"token": "ok",
				},
			}

			_, err := service.ExecuteAction(ctx, req)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*httperrors.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.ErrorCode, ShouldEqual, oerrors.OntologyQuery_ActionExecution_InvalidParameter)
		})

		Convey("dynamic_params 中某 key 为 null，视为未提供", func() {
			actionType := baseActionType()
			omAccess.EXPECT().GetActionType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(actionType, map[string]any{}, true, nil)

			req := &interfaces.ActionExecutionRequest{
				KNID:               "kn_001",
				ActionTypeID:       "at_001",
				InstanceIdentities: []map[string]any{{"id": "1"}},
				DynamicParams: map[string]any{
					"token": nil,
				},
			}

			_, err := service.ExecuteAction(ctx, req)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*httperrors.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.ErrorCode, ShouldEqual, oerrors.OntologyQuery_ActionExecution_InvalidParameter)
		})
	})
}

func Test_executeAsync_AppendsOnlyNewResults(t *testing.T) {
	Convey("each progress batch appends only its new results; a failed batch is retried at the same positions (#790)", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
		logsService := omock.NewMockActionLogsService(mockCtrl)
		service := &actionSchedulerService{aoAccess: aoAccess, logsService: logsService, permissions: &actionPermissionStub{}}

		const total = 250
		execution := &interfaces.ActionExecution{
			ID:            "exec_append",
			KNID:          "kn_001",
			Status:        interfaces.ExecutionStatusPending,
			ExecutionMode: interfaces.ExecutionModePerInstance,
			TargetCount:   total,
			TotalCount:    total,
			StartTime:     1700000000000,
		}
		actionType := &interfaces.ActionType{
			ActionSource: interfaces.ActionSource{
				Type:   interfaces.ActionSourceTypeTool,
				BoxID:  "box_001",
				ToolID: "tool_001",
			},
			Parameters: []interfaces.Parameter{
				{Name: "order_no", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "order_no"},
			}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
		}
		attachTestActionProxySnapshot(t, execution, actionType)
		req := &interfaces.ActionExecutionRequest{}
		for i := 0; i < total; i++ {
			req.Instances = append(req.Instances, interfaces.ObjectSystemInfo{InstanceIdentity: map[string]any{"id": i}})
			req.ObjDatas = append(req.ObjDatas, map[string]any{"order_no": i})
		}

		type appendCall struct{ firstSeq, count int }
		var calls []appendCall
		var progress []*interfaces.ExecutionProgress
		var finalOutcome *interfaces.ExecutionOutcome
		logsService.EXPECT().MarkExecutionRunning(gomock.Any(), "kn_001", "exec_append").Return(nil)
		logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ExecutionStatusRunning, nil).AnyTimes()
		logsService.EXPECT().AppendResults(gomock.Any(), "kn_001", "exec_append", gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _, _ string, firstSeq int, results []interfaces.ObjectExecutionResult) error {
				calls = append(calls, appendCall{firstSeq, len(results)})
				if len(calls) == 1 {
					return fmt.Errorf("opensearch unavailable")
				}
				return nil
			}).Times(3)
		logsService.EXPECT().UpdateExecutionProgress(gomock.Any(), "kn_001", "exec_append", gomock.Any()).DoAndReturn(
			func(_ context.Context, _, _ string, p *interfaces.ExecutionProgress) error {
				progress = append(progress, p)
				return nil
			}).Times(2)
		logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_append", gomock.Any()).DoAndReturn(
			func(_ context.Context, _, _ string, outcome *interfaces.ExecutionOutcome) error {
				finalOutcome = outcome
				return nil
			})
		aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_001", "tool_001", gomock.Any()).
			Return(map[string]any{"ok": true}, nil).Times(total)

		service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{})

		// Batch 1 (positions 0-99) fails and stays pending, so batch 2 rewrites it together
		// with positions 100-199; the last partial batch (200-249) is stored before finishing.
		So(calls, ShouldResemble, []appendCall{{0, 100}, {0, 200}, {200, 50}})
		So(len(progress), ShouldEqual, 2)
		So(progress[0].SuccessCount, ShouldEqual, 100)
		So(progress[1].SuccessCount, ShouldEqual, 200)
		So(finalOutcome.Status, ShouldEqual, interfaces.ExecutionStatusCompleted)
		So(finalOutcome.SuccessCount, ShouldEqual, total)
	})
}

func Test_executeAsync_RetriesFinalResultsFlush(t *testing.T) {
	Convey("the last results flush of an execution is retried with backoff before the terminal write (#790)", t, func() {
		saved := finalFlushBackoff
		finalFlushBackoff = []time.Duration{0, 0, 0}
		defer func() { finalFlushBackoff = saved }()

		run := func(failures int) (appendCalls int, finished *interfaces.ExecutionOutcome) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
			logsService := omock.NewMockActionLogsService(mockCtrl)
			service := &actionSchedulerService{aoAccess: aoAccess, logsService: logsService, permissions: &actionPermissionStub{}}
			execution, actionType, req := aggregatedOnceFixture(t, "exec_final_flush")

			logsService.EXPECT().MarkExecutionRunning(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ExecutionStatusRunning, nil).AnyTimes()
			logsService.EXPECT().AppendResults(gomock.Any(), "kn_001", "exec_final_flush", 0, gomock.Any()).DoAndReturn(
				func(_ context.Context, _, _ string, _ int, _ []interfaces.ObjectExecutionResult) error {
					appendCalls++
					if appendCalls <= failures {
						return fmt.Errorf("opensearch unavailable")
					}
					return nil
				}).AnyTimes()
			logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_final_flush", gomock.Any()).DoAndReturn(
				func(_ context.Context, _, _ string, outcome *interfaces.ExecutionOutcome) error {
					finished = outcome
					return nil
				})
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(map[string]any{"ok": true}, nil)

			service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{})
			return appendCalls, finished
		}

		calls, outcome := run(2)
		So(calls, ShouldEqual, 3) // two failures, stored on the third attempt
		So(outcome.Status, ShouldEqual, interfaces.ExecutionStatusCompleted)

		calls, outcome = run(10)
		So(calls, ShouldEqual, 1+len(finalFlushBackoff)) // gives up after the backoff schedule
		So(outcome, ShouldNotBeNil)                      // the terminal write still records what ran
		So(outcome.SuccessCount, ShouldEqual, 1)
	})
}

func Test_shouldCheckExecutionCancellation(t *testing.T) {
	Convey("large executions check for a cancel at batch boundaries and at least once a second (#790)", t, func() {
		// Small runs check before every instance.
		So(shouldCheckExecutionCancellation(7, 50, 0), ShouldBeTrue)
		// Large runs: the first instance and every batch boundary.
		So(shouldCheckExecutionCancellation(0, 1000, 0), ShouldBeTrue)
		So(shouldCheckExecutionCancellation(200, 1000, 0), ShouldBeTrue)
		// Between boundaries only once the interval has passed, so a cancel landing in
		// the tail of a run is noticed within about a second instead of never.
		So(shouldCheckExecutionCancellation(950, 1000, 200*time.Millisecond), ShouldBeFalse)
		So(shouldCheckExecutionCancellation(950, 1000, cancellationCheckInterval), ShouldBeTrue)
	})
}

// captureAppendedResults records every AppendResults call of an execution and checks that
// the batches arrive at consecutive positions within the execution.
func captureAppendedResults(logsService *omock.MockActionLogsService, execID string, stored *[]interfaces.ObjectExecutionResult) {
	logsService.EXPECT().AppendResults(gomock.Any(), "kn_001", execID, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, firstSeq int, results []interfaces.ObjectExecutionResult) error {
			So(firstSeq, ShouldEqual, len(*stored))
			*stored = append(*stored, results...)
			return nil
		}).AnyTimes()
}
