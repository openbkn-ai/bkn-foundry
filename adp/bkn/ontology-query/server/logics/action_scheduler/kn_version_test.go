// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

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
