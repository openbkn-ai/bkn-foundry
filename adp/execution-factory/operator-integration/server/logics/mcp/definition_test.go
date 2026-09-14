// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/mock/gomock"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// inProcessInstances serves one in-process MCP server for every version.
type inProcessInstances struct {
	interfaces.InstanceService
	server *server.MCPServer
	calls  int
}

func (i *inProcessInstances) GetMCPInstance(context.Context, string, int) (*interfaces.MCPServerInstance, error) {
	i.calls++
	return &interfaces.MCPServerInstance{MCPServer: i.server}, nil
}

func definitionTestServer() *server.MCPServer {
	srv := server.NewMCPServer("definition-test", "1.0.0", server.WithToolCapabilities(false))
	handler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	srv.AddTool(mcp.NewTool("send_sms",
		mcp.WithDescription("Send an SMS"),
		mcp.WithString("phone", mcp.Required(), mcp.Description("Recipient")),
	), handler)
	srv.AddTool(mcp.NewTool("delete_everything", mcp.WithDescription("Unrelated tool")), handler)
	return srv
}

func mcpDefinitionReadContext(access string) context.Context {
	return interfaces.WithProxyExecutionContext(context.Background(), interfaces.ProxyExecutionContext{
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
		Access:       access,
	})
}

func definitionHTTPStatus(err error) int {
	var httpErr *oerrors.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.HTTPCode
	}
	return 0
}

// Without a route-validated definition context, or without the proxy's grant,
// neither the config table nor the MCP server is reached.
func TestGetMCPToolDefinitionAsProxyAuthorizesBeforeConnecting(t *testing.T) {
	tests := []struct {
		name       string
		ctx        context.Context
		authErr    error
		wantStatus int
	}{
		{name: "direct internal caller", ctx: context.Background(), wantStatus: http.StatusForbidden},
		{name: "execution context", ctx: mcpDefinitionReadContext(""), wantStatus: http.StatusForbidden},
		{name: "grant revoked", ctx: mcpDefinitionReadContext(interfaces.ProxyAccessDefinitionRead),
			authErr: interfaces.ErrProxyExecutionDenied, wantStatus: http.StatusForbidden},
		{name: "bkn-safe unavailable", ctx: mcpDefinitionReadContext(interfaces.ProxyAccessDefinitionRead),
			authErr: errors.New("connection refused"), wantStatus: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			instances := &inProcessInstances{server: definitionTestServer()}
			svc := &mcpServiceImpl{
				logger:             logger.DefaultLogger(),
				DBMCPServerConfig:  mocks.NewMockDBMCPServerConfig(ctrl),
				DBMCPServerRelease: mocks.NewMockDBMCPServerRelease(ctrl),
				MCPInstanceService: instances,
				ProxyAuthorizer:    &denyingMCPProxyAuthorizer{err: test.authErr},
				ProxyAudit:         &mcpProxyExecutionAudit{},
			}

			resp, err := svc.GetMCPToolDefinitionAsProxy(test.ctx,
				&interfaces.MCPToolDefinitionRequest{MCPID: "mcp-1", ToolName: "send_sms"})

			if resp != nil || definitionHTTPStatus(err) != test.wantStatus {
				t.Fatalf("GetMCPToolDefinitionAsProxy() = %+v, %v; want HTTP %d", resp, err, test.wantStatus)
			}
			if instances.calls != 0 {
				t.Fatalf("MCP server reached %d times before authorization", instances.calls)
			}
		})
	}
}

func TestGetMCPToolDefinitionAsProxyReturnsOnlyTheNamedTool(t *testing.T) {
	ctrl := gomock.NewController(t)
	configs := mocks.NewMockDBMCPServerConfig(ctrl)
	configs.EXPECT().SelectByID(gomock.Any(), gomock.Nil(), "mcp-1").Return(&model.MCPServerConfigDB{
		MCPID:        "mcp-1",
		Status:       string(interfaces.BizStatusPublished),
		Version:      2,
		CreationType: interfaces.MCPCreationTypeToolImported.String(),
	}, nil).Times(3)
	audit := &mcpProxyExecutionAudit{}
	svc := &mcpServiceImpl{
		logger:             logger.DefaultLogger(),
		DBMCPServerConfig:  configs,
		DBMCPServerRelease: mocks.NewMockDBMCPServerRelease(ctrl),
		MCPInstanceService: &inProcessInstances{server: definitionTestServer()},
		ProxyAuthorizer:    &denyingMCPProxyAuthorizer{},
		ProxyAudit:         audit,
	}

	resp, err := svc.GetMCPToolDefinitionAsProxy(mcpDefinitionReadContext(interfaces.ProxyAccessDefinitionRead),
		&interfaces.MCPToolDefinitionRequest{MCPID: "mcp-1", ToolName: "send_sms"})
	if err != nil {
		t.Fatalf("GetMCPToolDefinitionAsProxy() error = %v", err)
	}
	if resp.MCPID != "mcp-1" || resp.Name != "send_sms" || resp.Description != "Send an SMS" {
		t.Fatalf("identity = %+v", resp)
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "delete_everything") || strings.Contains(string(encoded), "annotations") {
		t.Fatalf("definition carries more than the named tool's contract: %s", encoded)
	}

	// The schema is the one the tool listing serves, not a re-derivation of it.
	listed, err := svc.GetMCPTools(context.Background(), &interfaces.MCPProxyToolListRequest{MCPID: "mcp-1"})
	if err != nil {
		t.Fatal(err)
	}
	var want json.RawMessage
	for _, tool := range listed.Tools {
		if tool.Name == "send_sms" {
			raw, _ := json.Marshal(tool)
			var decoded struct {
				InputSchema json.RawMessage `json:"inputSchema"`
			}
			_ = json.Unmarshal(raw, &decoded)
			want = decoded.InputSchema
		}
	}
	if len(want) == 0 || string(resp.InputSchema) != string(want) {
		t.Fatalf("input_schema = %s, listing serves %s", resp.InputSchema, want)
	}
	if len(audit.events) != 1 || audit.events[0].Decision != "allow" ||
		audit.events[0].Access != interfaces.ProxyAccessDefinitionRead {
		t.Fatalf("audit events = %+v", audit.events)
	}

	_, err = svc.GetMCPToolDefinitionAsProxy(mcpDefinitionReadContext(interfaces.ProxyAccessDefinitionRead),
		&interfaces.MCPToolDefinitionRequest{MCPID: "mcp-1", ToolName: "missing"})
	if definitionHTTPStatus(err) != http.StatusNotFound {
		t.Fatalf("unknown tool error = %v, want HTTP 404", err)
	}
}

// The same serving rule as a call: an offline server lists nothing through the
// proxy, and no connection is made to find that out.
func TestGetMCPToolDefinitionAsProxyRefusesAServerThatIsNotServed(t *testing.T) {
	ctrl := gomock.NewController(t)
	configs := mocks.NewMockDBMCPServerConfig(ctrl)
	configs.EXPECT().SelectByID(gomock.Any(), gomock.Nil(), "mcp-1").Return(&model.MCPServerConfigDB{
		MCPID:        "mcp-1",
		Status:       string(interfaces.BizStatusOffline),
		Version:      2,
		CreationType: interfaces.MCPCreationTypeToolImported.String(),
	}, nil)
	instances := &inProcessInstances{server: definitionTestServer()}
	svc := &mcpServiceImpl{
		logger:             logger.DefaultLogger(),
		DBMCPServerConfig:  configs,
		DBMCPServerRelease: mocks.NewMockDBMCPServerRelease(ctrl),
		MCPInstanceService: instances,
		ProxyAuthorizer:    &denyingMCPProxyAuthorizer{},
		ProxyAudit:         &mcpProxyExecutionAudit{},
	}

	_, err := svc.GetMCPToolDefinitionAsProxy(mcpDefinitionReadContext(interfaces.ProxyAccessDefinitionRead),
		&interfaces.MCPToolDefinitionRequest{MCPID: "mcp-1", ToolName: "send_sms"})

	if definitionHTTPStatus(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "MCPServerNotPublished") {
		t.Fatalf("error = %v, want HTTP 400 MCPServerNotPublished", err)
	}
	if instances.calls != 0 {
		t.Fatalf("MCP server reached %d times for a server that is not served", instances.calls)
	}
}
