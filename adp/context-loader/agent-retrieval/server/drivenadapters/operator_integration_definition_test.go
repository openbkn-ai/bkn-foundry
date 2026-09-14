// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// executionFactoryServer answers every request with status and body and records the last one.
type executionFactoryServer struct {
	mu      sync.Mutex
	request *http.Request
}

func (s *executionFactoryServer) last() *http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.request
}

func newExecutionFactoryServer(t *testing.T, status int, body string) (*operatorIntegrationClient, *executionFactoryServer) {
	t.Helper()
	recorded := &executionFactoryServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.mu.Lock()
		recorded.request = r.Clone(context.Background())
		recorded.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	return &operatorIntegrationClient{
		logger:     logger,
		baseURL:    srv.URL + "/api/agent-operator-integration",
		httpClient: rest.NewHTTPClientWithOptions(rest.HTTPClientOptions{TimeOut: 5}),
	}, recorded
}

const executionFactoryForbidden = `{"code":"AgentOperatorIntegration.Forbidden.CommonOperationForbidden",` +
	`"description":"You have no permission for this operation","solution":"Ask an administrator","link":"none"}`

// The direct tool reads used to turn every Execution Factory refusal into 502 "dependency
// unavailable". A refusal is the caller's answer and keeps its status and downstream code; only a
// 5xx is a dependency failure (#1548).
func TestToolDetailReadsKeepExecutionFactoryStatus(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantStatus  int
		wantDetails string
	}{
		{name: "forbidden", status: http.StatusForbidden, body: executionFactoryForbidden,
			wantStatus: http.StatusForbidden, wantDetails: "AgentOperatorIntegration.Forbidden.CommonOperationForbidden: "},
		{name: "unauthorized", status: http.StatusUnauthorized,
			body:       `{"code":"AgentOperatorIntegration.Unauthorized.TokenInvalid","description":"Token expired"}`,
			wantStatus: http.StatusUnauthorized, wantDetails: "AgentOperatorIntegration.Unauthorized.TokenInvalid: "},
		{name: "not found", status: http.StatusNotFound,
			body:       `{"code":"AgentOperatorIntegration.NotFound.ToolNotFound","description":"Tool not found"}`,
			wantStatus: http.StatusNotFound, wantDetails: "AgentOperatorIntegration.NotFound.ToolNotFound: "},
		{name: "refusal without envelope", status: http.StatusForbidden, body: `<html>forbidden</html>`,
			wantStatus: http.StatusForbidden},
		{name: "dependency failure", status: http.StatusInternalServerError,
			body:       `{"code":"AgentOperatorIntegration.InternalServerError","description":"db down"}`,
			wantStatus: http.StatusBadGateway},
		{name: "authorization outage", status: http.StatusServiceUnavailable,
			body:       `{"code":"AgentOperatorIntegration.ServiceUnavailable","description":"bkn-safe down"}`,
			wantStatus: http.StatusBadGateway},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newExecutionFactoryServer(t, test.status, test.body)
			ctx := common.SetLanguageToCtx(
				common.SetRawTokenToCtx(context.Background(), "caller-token"), sharedrest.AmericanEnglish)

			_, toolErr := client.GetToolDetail(ctx, &interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"})
			_, mcpErr := client.GetMCPToolDetail(ctx, &interfaces.GetMCPToolDetailRequest{McpID: "mcp-1", ToolName: "lookup"})

			for name, err := range map[string]error{"tool": toolErr, "mcp": mcpErr} {
				he := asHTTPError(t, err)
				if he.HTTPCode != test.wantStatus {
					t.Fatalf("%s: status = %d, want %d (%v)", name, he.HTTPCode, test.wantStatus, err)
				}
				details, _ := he.ErrorDetails.(string)
				if test.wantDetails != "" && !strings.HasPrefix(details, test.wantDetails) {
					t.Errorf("%s: details = %q, want the downstream code first", name, details)
				}
				if strings.Contains(he.Error(), "db down") || strings.Contains(he.Error(), "127.0.0.1") {
					t.Errorf("%s: error exposes downstream internals: %s", name, he.Error())
				}
			}
		})
	}
}

func actionDefinitionProxy(targetType, targetID string) *interfaces.KNProxyExecution {
	return &interfaces.KNProxyExecution{
		Mapping: &interfaces.KNProxyAccount{
			KNID: "kn-1", ProxyAccountID: "proxy-1", ProxyAccountType: "app", Version: 7,
		},
		Binding: interfaces.KNProxyBinding{
			KNID: "kn-1", ChildType: interfaces.KNProxyChildTypeActionType, ChildID: "at-1",
			TargetType: targetType, TargetID: targetID, Operation: interfaces.KNProxyOperationExecute,
		},
	}
}

func callerContextWithToken() context.Context {
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
	return common.SetRawTokenToCtx(ctx, "caller-token")
}

func assertActionDefinitionHeaders(t *testing.T, request *http.Request, targetType, targetID string) {
	t.Helper()
	want := map[string]string{
		string(interfaces.HeaderXAccountID):   "proxy-1",
		string(interfaces.HeaderXAccountType): "app",
		headerBKNCallerID:                     "user-1",
		headerBKNCallerType:                   "user",
		headerBKNKnowledgeID:                  "kn-1",
		headerBKNChildType:                    "action_type",
		headerBKNChildID:                      "at-1",
		headerBKNProxyVersion:                 "7",
		headerBKNTargetType:                   targetType,
		headerBKNTargetID:                     targetID,
		headerBKNOperation:                    "execute",
	}
	for header, value := range want {
		if got := request.Header.Get(header); got != value {
			t.Errorf("header %s = %q, want %q", header, got, value)
		}
	}
	// A read names no execution, and the caller's own credential never rides along with the proxy.
	for _, header := range []string{"Authorization", "X-Bkn-Execution-Id"} {
		if got := request.Header.Get(header); got != "" {
			t.Errorf("header %s = %q, want absent", header, got)
		}
	}
}

func TestToolDefinitionReadCarriesTheActionTypeProxyContext(t *testing.T) {
	client, server := newExecutionFactoryServer(t, http.StatusOK,
		`{"box_id":"box-1","tool_id":"tool-1","name":"send_sms","description":"Send an SMS",`+
			`"api_spec":{"parameters":[{"name":"phone","in":"query","required":true}],"request_body":null}}`)

	resp, err := client.GetToolDefinitionAsProxy(callerContextWithToken(),
		&interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"},
		actionDefinitionProxy(interfaces.KNProxyTargetTypeToolBox, "box-1"))
	if err != nil {
		t.Fatal(err)
	}
	request := server.last()
	if request.Method != http.MethodGet ||
		request.URL.Path != "/api/agent-operator-integration/internal-v1/tool-box/box-1/tool/tool-1/definition" {
		t.Fatalf("request = %s %s", request.Method, request.URL.Path)
	}
	assertActionDefinitionHeaders(t, request, "tool_box", "box-1")
	params, _ := resp.Metadata.APISpec["parameters"].([]any)
	if resp.ToolID != "tool-1" || resp.Name != "send_sms" || resp.Description != "Send an SMS" || len(params) != 1 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestMCPToolDefinitionReadCarriesTheActionTypeProxyContext(t *testing.T) {
	client, server := newExecutionFactoryServer(t, http.StatusOK,
		`{"mcp_id":"mcp-1","name":"send sms/v2","description":"Send an SMS",`+
			`"input_schema":{"type":"object","properties":{"phone":{"type":"string"}},"required":["phone"]}}`)

	resp, err := client.GetMCPToolDefinitionAsProxy(callerContextWithToken(),
		&interfaces.GetMCPToolDetailRequest{McpID: "mcp-1", ToolName: "send sms/v2"},
		actionDefinitionProxy(interfaces.KNProxyTargetTypeMCP, "mcp-1"))
	if err != nil {
		t.Fatal(err)
	}
	request := server.last()
	if request.Method != http.MethodGet ||
		request.URL.Path != "/api/agent-operator-integration/internal-v1/mcp/proxy/mcp-1/tool/definition" ||
		request.URL.Query().Get("tool_name") != "send sms/v2" {
		t.Fatalf("request = %s %s", request.Method, request.URL.String())
	}
	assertActionDefinitionHeaders(t, request, "mcp", "mcp-1")
	if resp.Name != "send sms/v2" || resp.Description != "Send an SMS" || resp.InputSchema["type"] != "object" {
		t.Fatalf("response = %+v", resp)
	}
}

// The definition reads accept only an action-type binding for exactly the target they address;
// anything else is refused before a request is made.
func TestDefinitionReadsRefuseAProxyOutsideTheActionBinding(t *testing.T) {
	client, server := newExecutionFactoryServer(t, http.StatusOK, `{}`)
	ctx := callerContextWithToken()

	capability := actionDefinitionProxy(interfaces.KNProxyTargetTypeToolBox, "box-1")
	capability.Binding.ChildType = interfaces.KNProxyChildTypeCapability
	tests := map[string]func() error{
		"other box": func() error {
			_, err := client.GetToolDefinitionAsProxy(ctx, &interfaces.GetToolDetailRequest{BoxID: "box-2", ToolID: "tool-1"},
				actionDefinitionProxy(interfaces.KNProxyTargetTypeToolBox, "box-1"))
			return err
		},
		"MCP binding on the Tool read": func() error {
			_, err := client.GetToolDefinitionAsProxy(ctx, &interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"},
				actionDefinitionProxy(interfaces.KNProxyTargetTypeMCP, "box-1"))
			return err
		},
		"other MCP server": func() error {
			_, err := client.GetMCPToolDefinitionAsProxy(ctx, &interfaces.GetMCPToolDetailRequest{McpID: "mcp-2", ToolName: "lookup"},
				actionDefinitionProxy(interfaces.KNProxyTargetTypeMCP, "mcp-1"))
			return err
		},
		"mounted capability binding": func() error {
			_, err := client.GetToolDefinitionAsProxy(ctx, &interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"},
				capability)
			return err
		},
		"no tool": func() error {
			_, err := client.GetToolDefinitionAsProxy(ctx, &interfaces.GetToolDetailRequest{BoxID: "box-1"},
				actionDefinitionProxy(interfaces.KNProxyTargetTypeToolBox, "box-1"))
			return err
		},
	}
	for name, read := range tests {
		t.Run(name, func(t *testing.T) {
			if err := read(); err == nil {
				t.Fatal("definition read outside the action binding succeeded")
			}
		})
	}
	if server.last() != nil {
		t.Fatalf("a refused definition read still reached Execution Factory: %s", server.last().URL)
	}
}

// The execute helpers stay capability-only: an action-type binding cannot run a tool through
// Context Loader, even though the same proxy may read its definition.
func TestActionTypeBindingCannotExecuteThroughContextLoader(t *testing.T) {
	client, server := newExecutionFactoryServer(t, http.StatusOK, `{"ok":true}`)
	ctx := callerContextWithToken()

	if _, err := client.ExecutePublishedToolAsProxy(ctx, &interfaces.ExecutePublishedToolRequest{
		ToolboxID: "box-1", ToolID: "tool-1",
	}, actionDefinitionProxy(interfaces.KNProxyTargetTypeToolBox, "box-1")); err == nil {
		t.Fatal("action-type binding executed a tool")
	}
	if _, err := client.CallMCPToolAsProxy(ctx, &interfaces.CallMCPToolRequest{
		McpID: "mcp-1", ToolName: "lookup",
	}, actionDefinitionProxy(interfaces.KNProxyTargetTypeMCP, "mcp-1")); err == nil {
		t.Fatal("action-type binding called an MCP tool")
	}
	if server.last() != nil {
		t.Fatalf("a refused execution still reached Execution Factory: %s", server.last().URL)
	}
}

// A response for another tool than the one asked for is not accepted as its definition.
func TestToolDefinitionReadRejectsAnotherToolsAnswer(t *testing.T) {
	client, _ := newExecutionFactoryServer(t, http.StatusOK,
		`{"box_id":"box-1","tool_id":"tool-2","name":"other","api_spec":{}}`)

	_, err := client.GetToolDefinitionAsProxy(callerContextWithToken(),
		&interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"},
		actionDefinitionProxy(interfaces.KNProxyTargetTypeToolBox, "box-1"))

	if he := asHTTPError(t, err); he.HTTPCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", he.HTTPCode)
	}
}

// The proxy route's own refusal keeps its status too.
func TestDefinitionReadKeepsTheProxyRefusal(t *testing.T) {
	client, _ := newExecutionFactoryServer(t, http.StatusForbidden,
		`{"code":"AgentOperatorIntegration.Forbidden","description":"proxy definition read is forbidden"}`)

	_, err := client.GetMCPToolDefinitionAsProxy(callerContextWithToken(),
		&interfaces.GetMCPToolDetailRequest{McpID: "mcp-1", ToolName: "lookup"},
		actionDefinitionProxy(interfaces.KNProxyTargetTypeMCP, "mcp-1"))

	if he := asHTTPError(t, err); he.HTTPCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", he.HTTPCode)
	}
}
