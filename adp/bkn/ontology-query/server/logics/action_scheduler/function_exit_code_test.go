// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"
)

const noTokenStderr = "Traceback (most recent call last):\n  File \"<string>\", line 56, in calc\n" +
	"bkn_osdk.errors.InputError: No access token.\n"

// useFunctionTarget pins the execution's proxy snapshot to a Function toolbox, as the
// scheduler records it when the action's toolbox is a Function box.
func useFunctionTarget(t *testing.T, execution *interfaces.ActionExecution, actionType *interfaces.ActionType) {
	t.Helper()
	attachTestActionProxySnapshot(t, execution, actionType)
	_, requirement, err := actionProxyBinding(context.Background(), execution.KNID, actionType,
		interfaces.ProxyTargetTypeFunction)
	if err != nil {
		t.Fatalf("actionProxyBinding() error = %v", err)
	}
	execution.ProxyPermissionSnapshot = []interfaces.PermissionRequirement{requirement}
}

func functionProxyContext(targetType string) context.Context {
	return interfaces.WithTrustedProxyContext(context.Background(), &interfaces.TrustedProxyContext{
		Binding: interfaces.TrustedProxyBinding{TargetType: targetType},
	})
}

// The Function runtime answers HTTP 200 whatever the sandbox outcome is, so the exit code in
// the body is the only record of a failed Function.
func Test_ExecuteTool_FunctionExitCode(t *testing.T) {
	Convey("ExecuteTool classifies a Function by its sandbox exit code", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
		actionType := &interfaces.ActionType{
			ActionSource: interfaces.ActionSource{Type: interfaces.ActionSourceTypeTool, BoxID: "box_fn", ToolID: "fn_1"},
		}
		respond := func(body map[string]any) {
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_fn", "fn_1", gomock.Any()).Return(body, nil)
		}

		Convey("a non-zero exit code fails and keeps the sandbox outcome", func() {
			body := map[string]any{"exit_code": json.Number("1"), "stderr": noTokenStderr, "result": nil}
			respond(body)

			result, err := ExecuteTool(functionProxyContext(interfaces.ProxyTargetTypeFunction), aoAccess, actionType, nil)

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldEqual, "function exited with code 1: bkn_osdk.errors.InputError: No access token.")
			So(result, ShouldResemble, body)
		})

		Convey("a zero exit code succeeds", func() {
			respond(map[string]any{"exit_code": json.Number("0"), "result": map[string]any{"ok": true}})

			_, err := ExecuteTool(functionProxyContext(interfaces.ProxyTargetTypeFunction), aoAccess, actionType, nil)

			So(err, ShouldBeNil)
		})

		Convey("an OpenAPI Tool's own exit_code field keeps its meaning", func() {
			respond(map[string]any{"exit_code": 1})

			_, err := ExecuteTool(functionProxyContext(interfaces.ProxyTargetTypeToolBox), aoAccess, actionType, nil)

			So(err, ShouldBeNil)
		})
	})
}

func Test_executeAsync_FunctionExitCodePerInstance(t *testing.T) {
	Convey("A Function that exits non-zero for one instance is counted as failed (#1642)", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
		logsService := omock.NewMockActionLogsService(mockCtrl)
		service := &actionSchedulerService{aoAccess: aoAccess, logsService: logsService, permissions: &actionPermissionStub{}}

		execution := &interfaces.ActionExecution{
			ID: "exec_fn", KNID: "kn_001", Status: interfaces.ExecutionStatusPending,
			TotalCount: 2, StartTime: 1700000000000,
		}
		actionType := &interfaces.ActionType{
			ActionSource: interfaces.ActionSource{Type: interfaces.ActionSourceTypeTool, BoxID: "box_fn", ToolID: "fn_1"},
		}
		useFunctionTarget(t, execution, actionType)
		req := &interfaces.ActionExecutionRequest{
			Instances: []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{"id": "1"}},
				{InstanceIdentity: map[string]any{"id": "2"}},
			},
			ObjDatas: []map[string]any{{"id": "1"}, {"id": "2"}},
		}

		var finalOutcome *interfaces.ExecutionOutcome
		var stored []interfaces.ObjectExecutionResult
		captureAppendedResults(logsService, "exec_fn", &stored)
		logsService.EXPECT().MarkExecutionRunning(gomock.Any(), "kn_001", "exec_fn").Return(nil)
		logsService.EXPECT().UpdateExecutionProgress(gomock.Any(), "kn_001", "exec_fn", gomock.Any()).Return(nil).AnyTimes()
		logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_fn", gomock.Any()).DoAndReturn(
			func(_ context.Context, _, _ string, outcome *interfaces.ExecutionOutcome) error {
				finalOutcome = outcome
				return nil
			})
		logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ExecutionStatusRunning, nil).AnyTimes()
		failedBody := map[string]any{"exit_code": json.Number("1"), "stderr": noTokenStderr, "result": nil}
		gomock.InOrder(
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_fn", "fn_1", gomock.Any()).Return(failedBody, nil),
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_fn", "fn_1", gomock.Any()).
				Return(map[string]any{"exit_code": json.Number("0"), "result": map[string]any{"profit": "1"}}, nil),
		)

		service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{})

		So(finalOutcome, ShouldNotBeNil)
		So(finalOutcome.SuccessCount, ShouldEqual, 1)
		So(finalOutcome.FailedCount, ShouldEqual, 1)
		So(len(stored), ShouldEqual, 2)
		So(stored[0].Status, ShouldEqual, interfaces.ObjectStatusFailed)
		So(stored[0].ErrorMessage, ShouldContainSubstring, "function exited with code 1")
		// The sandbox outcome stays on the failed entry: its stderr is the diagnosis.
		So(stored[0].Result, ShouldResemble, failedBody)
		So(stored[1].Status, ShouldEqual, interfaces.ObjectStatusSuccess)
	})
}

func Test_executeAsync_FunctionExitCodeOnce(t *testing.T) {
	Convey("A once-mode Function that exits non-zero fails the execution (#1642)", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)
		logsService := omock.NewMockActionLogsService(mockCtrl)
		service := &actionSchedulerService{aoAccess: aoAccess, logsService: logsService, permissions: &actionPermissionStub{}}

		execution, actionType, req := aggregatedOnceFixture(t, "exec_fn_once")
		useFunctionTarget(t, execution, actionType)

		var finalOutcome *interfaces.ExecutionOutcome
		var stored []interfaces.ObjectExecutionResult
		captureAppendedResults(logsService, "exec_fn_once", &stored)
		logsService.EXPECT().MarkExecutionRunning(gomock.Any(), "kn_001", "exec_fn_once").Return(nil).AnyTimes()
		logsService.EXPECT().UpdateExecutionProgress(gomock.Any(), "kn_001", "exec_fn_once", gomock.Any()).Return(nil).AnyTimes()
		logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_fn_once", gomock.Any()).DoAndReturn(
			func(_ context.Context, _, _ string, outcome *interfaces.ExecutionOutcome) error {
				finalOutcome = outcome
				return nil
			})
		logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ExecutionStatusRunning, nil).AnyTimes()
		failedBody := map[string]any{"exit_code": json.Number("2"), "stderr": "boom\n"}
		aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_001", "tool_001", gomock.Any()).Return(failedBody, nil)

		service.executeAsync(execution, actionType, req, interfaces.CallerRuntimeCredential{})

		So(finalOutcome, ShouldNotBeNil)
		So(finalOutcome.Status, ShouldEqual, interfaces.ExecutionStatusFailed)
		So(finalOutcome.SuccessCount, ShouldEqual, 0)
		So(finalOutcome.FailedCount, ShouldEqual, 1)
		So(stored[0].Status, ShouldEqual, interfaces.ObjectStatusFailed)
		So(stored[0].ErrorMessage, ShouldEqual, "function exited with code 2: boom")
		So(stored[0].Result, ShouldResemble, failedBody)
	})
}
