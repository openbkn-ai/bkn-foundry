// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestCallMCPToolPreservesStructuredResultThroughServedProxy(t *testing.T) {
	ctrl := gomock.NewController(t)
	upstream := server.NewMCPServer("structured-fixture", "1.0.0", server.WithToolCapabilities(true))
	upstream.AddTool(mcp.NewTool("qa_structured"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result := mcp.NewToolResultText("first")
		result.Content = append(result.Content, mcp.TextContent{Type: "text", Text: "second"})
		result.StructuredContent = map[string]any{"answer": 7, "label": "fixed"}
		return result, nil
	})
	configs := mocks.NewMockDBMCPServerConfig(ctrl)
	configs.EXPECT().SelectByID(gomock.Any(), gomock.Nil(), "mcp-1").Return(&model.MCPServerConfigDB{
		MCPID: "mcp-1", Status: string(interfaces.BizStatusPublished), Version: 1,
		CreationType: interfaces.MCPCreationTypeToolImported.String(),
	}, nil)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	auth.EXPECT().GetAccessor(gomock.Any(), "reviewer").Return(&interfaces.AuthAccessor{ID: "reviewer"}, nil)
	auth.EXPECT().CheckExecutePermission(gomock.Any(), gomock.Any(), "mcp-1", interfaces.AuthResourceTypeMCP).Return(nil)
	svc := &mcpServiceImpl{
		logger: logger.DefaultLogger(), AuthService: auth, DBMCPServerConfig: configs,
		MCPInstanceService: &releasedInstances{servers: map[int]*server.MCPServer{1: upstream}},
	}
	response, err := svc.CallMCPTool(context.Background(), &interfaces.MCPProxyCallToolRequest{
		MCPID: "mcp-1", ToolName: "qa_structured", UserID: "reviewer", Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := sonic.MarshalString(response)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			Answer int    `json:"answer"`
			Label  string `json:"label"`
		} `json:"structuredContent"`
		IsError bool `json:"is_error"`
	}
	if err := json.Unmarshal([]byte(wire), &got); err != nil || len(got.Content) != 2 ||
		got.Content[0].Text != "first" || got.Content[1].Text != "second" ||
		got.StructuredContent.Answer != 7 || got.StructuredContent.Label != "fixed" || got.IsError {
		t.Fatalf("proxy response lost upstream result: %s, %v", wire, err)
	}
}

func TestProxyCallToolResponsePreservesStructuredAndMultiBlockContent(t *testing.T) {
	var upstream mcp.CallToolResult
	upstreamJSON := []byte(`{"content":[{"type":"text","text":"first"},{"type":"text","text":"second"}],"structuredContent":{"answer":9007199254740993,"label":"fixed"},"isError":false}`)
	if err := json.Unmarshal(upstreamJSON, &upstream); err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(proxyCallToolResponse(&upstream))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Content           []json.RawMessage `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
		IsError           bool              `json:"is_error"`
	}
	if err := json.Unmarshal(response, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 2 || got.IsError || string(got.StructuredContent) != `{"answer":9007199254740993,"label":"fixed"}` {
		t.Fatalf("proxy response lost upstream content: %s", response)
	}
	for i, want := range []string{"first", "second"} {
		var block struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(got.Content[i], &block); err != nil || block.Text != want {
			t.Fatalf("content block %d = %s, %v", i, got.Content[i], err)
		}
	}
	generated, err := json.Marshal(proxyCallToolResponse(&mcp.CallToolResult{StructuredContent: map[string]any{"answer": 7}}))
	if err != nil {
		t.Fatal(err)
	}
	var generatedFields map[string]json.RawMessage
	if err := json.Unmarshal(generated, &generatedFields); err != nil || string(generatedFields["structuredContent"]) != `{"answer":7}` {
		t.Fatalf("generated structured result lost: %s, %v", generated, err)
	}
	plain, err := json.Marshal(proxyCallToolResponse(&mcp.CallToolResult{IsError: true}))
	if err != nil {
		t.Fatal(err)
	}
	var plainFields map[string]json.RawMessage
	if err := json.Unmarshal(plain, &plainFields); err != nil || plainFields["structuredContent"] != nil || string(plainFields["is_error"]) != "true" {
		t.Fatalf("plain error response changed: %s, %v", plain, err)
	}
}

type denyingMCPProxyAuthorizer struct {
	calls int
	err   error
}

func (a *denyingMCPProxyAuthorizer) Authorize(context.Context, interfaces.ProxyExecutionContext) error {
	a.calls++
	return a.err
}

type mcpProxyExecutionAudit struct {
	events []interfaces.ProxyExecutionAuditEvent
}

func (a *mcpProxyExecutionAudit) RecordProxyExecution(_ context.Context, event interfaces.ProxyExecutionAuditEvent) {
	a.events = append(a.events, event)
}

func TestCallMCPToolRechecksManagedProxyBeforeCreatingOutboundClient(t *testing.T) {
	tests := map[string]error{
		"grant revoked":        interfaces.ErrProxyExecutionDenied,
		"bkn-safe unavailable": errors.New("connection refused"),
	}
	for name, authorizationErr := range tests {
		t.Run(name, func(t *testing.T) {
			authorizer := &denyingMCPProxyAuthorizer{err: authorizationErr}
			audit := &mcpProxyExecutionAudit{}
			service := &mcpServiceImpl{
				ProxyAuthorizer: authorizer,
				ProxyAudit:      audit,
			}
			ctx := interfaces.WithProxyExecutionContext(context.Background(), interfaces.ProxyExecutionContext{
				CallerID:     "caller-1",
				CallerType:   "user",
				KnowledgeID:  "kn-1",
				ChildType:    interfaces.ProxyChildTypeAction,
				ChildID:      "action-1",
				ProxyID:      "proxy-1",
				ProxyType:    interfaces.ProxyAccountTypeApp,
				ProxyVersion: 3,
				TargetType:   interfaces.ProxyTargetTypeMCP,
				TargetID:     "mcp-1",
				Operation:    interfaces.ProxyOperationExecute,
				ExecutionID:  "execution-1",
			})

			response, err := service.callTool(ctx, &CallToolRequest{})

			if err == nil || response != nil {
				t.Fatalf("callTool() = %+v, %v; want final authorization failure", response, err)
			}
			if authorizer.calls != 1 {
				t.Fatalf("authorizer calls = %d, want 1", authorizer.calls)
			}
			if len(audit.events) != 1 || audit.events[0].Decision != "deny" ||
				audit.events[0].ExecutionID != "execution-1" {
				t.Fatalf("audit events = %+v", audit.events)
			}
		})
	}
}
