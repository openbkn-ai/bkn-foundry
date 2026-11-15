// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

// An action on the draft or a history version runs with the caller's own identity; on
// the running version it still goes through the proxy.
func TestExecuteToolRunsAsCallerOnlyWhenMarked(t *testing.T) {
	actionType := &interfaces.ActionType{ActionSource: interfaces.ActionSource{
		Type: interfaces.ActionSourceTypeTool, BoxID: "box_001", ToolID: "tool_001",
	}}

	t.Run("marked execution calls the tool as the caller", func(t *testing.T) {
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))
		aoAccess.EXPECT().ExecuteTool(gomock.Any(), "box_001", "tool_001", gomock.Any()).Return("ok", nil)
		ctx := interfaces.WithCallerIdentityExecution(context.Background())
		if _, err := ExecuteTool(ctx, aoAccess, actionType, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unmarked execution still goes through the proxy", func(t *testing.T) {
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))
		aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box_001", "tool_001", gomock.Any()).Return("ok", nil)
		if _, err := ExecuteTool(context.Background(), aoAccess, actionType, nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestExecuteMCPRunsAsCallerOnlyWhenMarked(t *testing.T) {
	actionType := &interfaces.ActionType{ActionSource: interfaces.ActionSource{
		Type: interfaces.ActionSourceTypeMCP, McpID: "mcp_001", ToolName: "tool",
	}}

	t.Run("marked execution calls the MCP tool as the caller", func(t *testing.T) {
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))
		aoAccess.EXPECT().ExecuteMCP(gomock.Any(), "mcp_001", "tool", gomock.Any()).Return("ok", nil)
		ctx := interfaces.WithCallerIdentityExecution(context.Background())
		if _, err := ExecuteMCP(ctx, aoAccess, actionType, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unmarked execution still goes through the proxy", func(t *testing.T) {
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))
		aoAccess.EXPECT().ExecuteMCPAsProxy(gomock.Any(), "mcp_001", "tool", gomock.Any()).Return("ok", nil)
		if _, err := ExecuteMCP(context.Background(), aoAccess, actionType, nil); err != nil {
			t.Fatal(err)
		}
	})
}

// runOnceAsCaller runs a once-mode execution recorded the way the scheduler records one on the
// draft or a history version (no proxy, caller-marked model version) and returns its outcome.
func runOnceAsCaller(t *testing.T, aoAccess *omock.MockAgentOperatorAccess, proxyModelVersion string,
	credential ...interfaces.CallerRuntimeCredential) (
	*interfaces.ExecutionOutcome, []interfaces.ObjectExecutionResult) {
	t.Helper()
	logsService := omock.NewMockActionLogsService(gomock.NewController(t))
	service := &actionSchedulerService{aoAccess: aoAccess, logsService: logsService, permissions: &actionPermissionStub{}}
	execution, actionType, req := aggregatedOnceFixture(t, "exec_caller")
	execution.Proxy = nil
	execution.ProxyVersion = 0
	execution.ProxyModelVersion = proxyModelVersion
	execution.ProxyPermissionSnapshot = nil

	var outcome *interfaces.ExecutionOutcome
	var stored []interfaces.ObjectExecutionResult
	logsService.EXPECT().AppendResults(gomock.Any(), "kn_001", "exec_caller", gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, _ int, results []interfaces.ObjectExecutionResult) error {
			stored = append(stored, results...)
			return nil
		}).AnyTimes()
	logsService.EXPECT().MarkExecutionRunning(gomock.Any(), "kn_001", "exec_caller").Return(nil).AnyTimes()
	logsService.EXPECT().UpdateExecutionProgress(gomock.Any(), "kn_001", "exec_caller", gomock.Any()).Return(nil).AnyTimes()
	logsService.EXPECT().GetExecutionStatus(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(interfaces.ExecutionStatusRunning, nil).AnyTimes()
	logsService.EXPECT().FinishExecution(gomock.Any(), "kn_001", "exec_caller", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, o *interfaces.ExecutionOutcome) error {
			outcome = o
			return nil
		})

	callerCredential := interfaces.CallerRuntimeCredential{}
	if len(credential) > 0 {
		callerCredential = credential[0]
	}
	service.executeAsync(execution, actionType, req, callerCredential)
	if outcome == nil || len(stored) != 1 {
		t.Fatalf("execution did not finish with one result: outcome=%v results=%d", outcome, len(stored))
	}
	return outcome, stored
}

// boxKind answers the toolbox kind lookup a caller-identity tool execution makes.
func boxKind(aoAccess *omock.MockAgentOperatorAccess, kind string, err error) {
	aoAccess.EXPECT().GetBoxMetadataType(gomock.Any(), "box_001", "tool_001").Return(kind, err)
}

// A draft or history-version execution has no proxy snapshot by design; it must reach the
// tool as the caller instead of failing on the missing snapshot (#3159).
func TestExecuteAsyncOnCallerIdentityDoesNotRequireProxySnapshot(t *testing.T) {
	t.Run("the caller's call succeeds", func(t *testing.T) {
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))
		boxKind(aoAccess, interfaces.ProxyTargetTypeToolBox, nil)
		aoAccess.EXPECT().ExecuteTool(gomock.Any(), "box_001", "tool_001", gomock.Any()).Return("ok", nil)

		outcome, results := runOnceAsCaller(t, aoAccess, interfaces.CallerIdentityModelPrefix+"draft@r1")

		if outcome.Status != interfaces.ExecutionStatusCompleted || results[0].Status != interfaces.ObjectStatusSuccess {
			t.Fatalf("status = %s/%s, error = %q", outcome.Status, results[0].Status, results[0].ErrorMessage)
		}
	})

	t.Run("the caller's denial is reported as the downstream denial", func(t *testing.T) {
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))
		boxKind(aoAccess, interfaces.ProxyTargetTypeFunction, nil)
		aoAccess.EXPECT().ExecuteTool(gomock.Any(), "box_001", "tool_001", gomock.Any()).
			Return(nil, errors.New("403 forbidden: no execute permission on function"))

		outcome, results := runOnceAsCaller(t, aoAccess, interfaces.CallerIdentityModelPrefix+"v1")

		if outcome.Status != interfaces.ExecutionStatusFailed {
			t.Fatalf("status = %s, want failed", outcome.Status)
		}
		if msg := results[0].ErrorMessage; !strings.Contains(msg, "no execute permission") ||
			strings.Contains(msg, "proxy snapshot is missing") {
			t.Fatalf("error = %q, want the caller's denial", msg)
		}
	})

	t.Run("an execution with neither a proxy nor the caller mark still fails closed", func(t *testing.T) {
		// No tool call is expected: the strict mock fails the test if one is made.
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))

		outcome, results := runOnceAsCaller(t, aoAccess, "model-v2")

		if outcome.Status != interfaces.ExecutionStatusFailed ||
			!strings.Contains(results[0].ErrorMessage, "proxy snapshot is missing") {
			t.Fatalf("status = %s, error = %q, want a missing proxy snapshot", outcome.Status, results[0].ErrorMessage)
		}
	})
	t.Run("a Function that exits non-zero fails the execution", func(t *testing.T) {
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))
		boxKind(aoAccess, interfaces.ProxyTargetTypeFunction, nil)
		aoAccess.EXPECT().ExecuteTool(gomock.Any(), "box_001", "tool_001", gomock.Any()).
			Return(map[string]any{"exit_code": json.Number("1"), "stderr": "boom\n"}, nil)

		outcome, results := runOnceAsCaller(t, aoAccess, interfaces.CallerIdentityModelPrefix+"draft@r1")

		if outcome.Status != interfaces.ExecutionStatusFailed ||
			!strings.Contains(results[0].ErrorMessage, "function exited with code 1") {
			t.Fatalf("status = %s, error = %q, want the Function's exit code", outcome.Status, results[0].ErrorMessage)
		}
	})

	t.Run("an OpenAPI Tool's own exit_code field keeps its meaning", func(t *testing.T) {
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))
		boxKind(aoAccess, interfaces.ProxyTargetTypeToolBox, nil)
		aoAccess.EXPECT().ExecuteTool(gomock.Any(), "box_001", "tool_001", gomock.Any()).
			Return(map[string]any{"exit_code": 1}, nil)

		outcome, _ := runOnceAsCaller(t, aoAccess, interfaces.CallerIdentityModelPrefix+"draft@r1")

		if outcome.Status != interfaces.ExecutionStatusCompleted {
			t.Fatalf("status = %s, want completed", outcome.Status)
		}
	})

	t.Run("an unresolvable toolbox kind fails before the tool is called", func(t *testing.T) {
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))
		boxKind(aoAccess, "", errors.New("tool tool_001 is not in toolbox box_001"))

		outcome, results := runOnceAsCaller(t, aoAccess, interfaces.CallerIdentityModelPrefix+"draft@r1")

		if outcome.Status != interfaces.ExecutionStatusFailed ||
			!strings.Contains(results[0].ErrorMessage, "resolve toolbox kind") {
			t.Fatalf("status = %s, error = %q", outcome.Status, results[0].ErrorMessage)
		}
	})
	// Without Trace Core an asynchronous Function cannot reuse the caller's Interaction, as on
	// the proxy path; a caller-identity Function must drop those IDs too.
	t.Run("a Function runs without the caller's managed trace IDs", func(t *testing.T) {
		aoAccess := omock.NewMockAgentOperatorAccess(gomock.NewController(t))
		boxKind(aoAccess, interfaces.ProxyTargetTypeFunction, nil)
		aoAccess.EXPECT().ExecuteTool(gomock.Any(), "box_001", "tool_001", gomock.Any()).DoAndReturn(
			func(ctx context.Context, _, _ string, _ interfaces.ToolExecutionRequest) (any, error) {
				traceContext, _ := common.GetTraceContextFromCtx(ctx)
				credential, _ := interfaces.CallerRuntimeCredentialFromContext(ctx)
				if traceContext.InteractionID != "" || credential.InteractionID != "" {
					t.Errorf("Function saw interaction %q / %q, want none", traceContext.InteractionID, credential.InteractionID)
				}
				return map[string]any{"exit_code": json.Number("0")}, nil
			})

		outcome, _ := runOnceAsCaller(t, aoAccess, interfaces.CallerIdentityModelPrefix+"draft@r1",
			interfaces.CallerRuntimeCredential{ConversationID: "conv_1", InteractionID: "int_1", ParentOperationID: "op_1"})

		if outcome.Status != interfaces.ExecutionStatusCompleted {
			t.Fatalf("status = %s, want completed", outcome.Status)
		}
	})
}
