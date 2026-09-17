// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package agent_operator

import (
	"context"
	"net/http"
	"strings"
	"testing"

	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"go.uber.org/mock/gomock"

	"ontology-query/common"
	"ontology-query/interfaces"
)

func TestExecuteToolUsesProxyAsEffectivePrincipal(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
	ctx := actionProxyContext(interfaces.ProxyTargetTypeToolBox, "box-1")

	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(),
		"http://operator/tool-box/box-1/proxy/tool-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			assertActionProxyHeaders(t, headers, "box-1")
			return http.StatusOK, []byte(`{"status_code":200,"body":{"ok":true}}`), nil
		})

	result, err := access.ExecuteToolAsProxy(ctx, "box-1", "tool-1", interfaces.ToolExecutionRequest{})
	if err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
	if result == nil {
		t.Fatal("expected tool result")
	}
}

func TestFunctionToolUsesFunctionProxyTarget(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
	ctx := actionProxyContext(interfaces.ProxyTargetTypeFunction, "box-fn")
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://operator/tool-box/box-fn/proxy/fn-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			if headers[interfaces.HTTPHeaderBKNTargetType] != interfaces.ProxyTargetTypeFunction { t.Fatalf("proxy headers = %#v", headers) }
			return http.StatusOK, []byte(`{"status_code":200,"body":{"ok":true}}`), nil
		})
	if _, err := access.ExecuteToolAsProxy(ctx, "box-fn", "fn-1", interfaces.ToolExecutionRequest{}); err != nil { t.Fatal(err) }
}

// A Function backing a logic property reads BKN as its caller, so the caller's
// credential travels beside the proxy identity to the Function runtime.
func TestFunctionToolCarriesCallerRuntimeCredential(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
	ctx := interfaces.WithCallerRuntimeCredential(
		actionProxyContext(interfaces.ProxyTargetTypeFunction, "box-fn"),
		interfaces.CallerRuntimeCredential{Authorization: "Bearer caller-token", ConversationID: "conv_1"})
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://operator/tool-box/box-fn/proxy/fn-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			if headers[interfaces.HTTP_HEADER_ACCOUNT_ID] != "proxy-1" || headers["Authorization"] != "Bearer caller-token" {
				t.Fatalf("function proxy headers = %#v", headers)
			}
			// No Interaction in the context: the Conversation must not travel alone.
			if headers[common.HeaderBKNConversationID] != "" || headers[common.HeaderBKNParentOperationID] != "" {
				t.Fatalf("conversation travelled without an interaction: %#v", headers)
			}
			return http.StatusOK, []byte(`{"status_code":200,"body":{"ok":true}}`), nil
		})
	if _, err := access.ExecuteToolAsProxy(ctx, "box-fn", "fn-1", interfaces.ToolExecutionRequest{}); err != nil {
		t.Fatal(err)
	}
}

// Inside an Interaction the Function's reads join it, under the operation the
// caller registered.
func TestFunctionToolCarriesCallerInteraction(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
	ctx := common.SetTraceContextToCtx(actionProxyContext(interfaces.ProxyTargetTypeFunction, "box-fn"),
		common.TraceContext{InteractionID: "int_1", OperationID: "op_parent"})
	ctx = interfaces.WithCallerRuntimeCredential(ctx, interfaces.CallerRuntimeCredential{
		Authorization: "Bearer caller-token", ConversationID: "conv_1", ParentOperationID: "op_registered"})
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://operator/tool-box/box-fn/proxy/fn-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			if headers[common.HeaderBKNConversationID] != "conv_1" || headers[common.HeaderBKNInteractionID] != "int_1" ||
				// The registered caller operation, never ontology-query's own unregistered child.
				headers[common.HeaderBKNParentOperationID] != "op_registered" {
				t.Fatalf("function interaction headers = %#v", headers)
			}
			return http.StatusOK, []byte(`{"status_code":200,"body":{"ok":true}}`), nil
		})
	if _, err := access.ExecuteToolAsProxy(ctx, "box-fn", "fn-1", interfaces.ToolExecutionRequest{}); err != nil {
		t.Fatal(err)
	}
}

// An OpenAPI Tool is a third party: the caller's credential never leaves for it.
func TestOpenAPIToolNeverCarriesCallerCredential(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
	ctx := interfaces.WithCallerRuntimeCredential(
		actionProxyContext(interfaces.ProxyTargetTypeToolBox, "box-1"),
		interfaces.CallerRuntimeCredential{Authorization: "Bearer caller-token"})
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://operator/tool-box/box-1/proxy/tool-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			if _, ok := headers["Authorization"]; ok {
				t.Fatalf("OpenAPI tool received the caller credential: %#v", headers)
			}
			return http.StatusOK, []byte(`{"status_code":200,"body":{"ok":true}}`), nil
		})
	if _, err := access.ExecuteToolAsProxy(ctx, "box-1", "tool-1", interfaces.ToolExecutionRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestBoxMetadataTypeRequiresToolMembership(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://operator/tool-box/box-fn", nil, nil).
		Return(http.StatusOK, []byte(`{"metadata_type":"function","tools":[{"tool_id":"fn-1"}]}`), nil).Times(2)
	if kind, err := access.GetBoxMetadataType(t.Context(), "box-fn", "fn-1"); err != nil || kind != interfaces.ProxyTargetTypeFunction {
		t.Fatalf("box kind = %q, %v", kind, err)
	}
	if _, err := access.GetBoxMetadataType(t.Context(), "box-fn", "other-tool"); err == nil { t.Fatal("tool outside box was accepted") }
}

func TestExecuteLogicPropertyToolUsesProxyAsEffectivePrincipal(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
	ctx := actionProxyContext(interfaces.ProxyTargetTypeToolBox, "box-1")
	trusted, _ := interfaces.TrustedProxyContextFromContext(ctx)
	trusted.Binding.ChildType = interfaces.PermissionResourceTypeLogicProperty
	trusted.Binding.ChildID = "logic-binding-1"
	trusted.ExecutionID = ""

	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(),
		"http://operator/tool-box/box-1/proxy/tool-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			if headers[interfaces.HTTP_HEADER_ACCOUNT_ID] != "proxy-1" ||
				headers[interfaces.HTTPHeaderBKNChildType] != interfaces.PermissionResourceTypeLogicProperty ||
				headers[interfaces.HTTPHeaderBKNChildID] != "logic-binding-1" ||
				headers[interfaces.HTTPHeaderBKNExecutionID] != "" {
				t.Fatalf("unexpected logic-property proxy headers: %#v", headers)
			}
			return http.StatusOK, []byte(`{"status_code":200,"body":{"ok":true}}`), nil
		})

	if _, err := access.ExecuteToolAsProxy(ctx, "box-1", "tool-1", interfaces.ToolExecutionRequest{}); err != nil {
		t.Fatalf("ExecuteToolAsProxy() error = %v", err)
	}
}

func TestExecuteMCPUsesProxyAsEffectivePrincipal(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
	ctx := actionProxyContext(interfaces.ProxyTargetTypeMCP, "mcp-1")

	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(),
		"http://operator/mcp/proxy/mcp-1/tool/call", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			assertActionProxyHeaders(t, headers, "mcp-1")
			return http.StatusOK, []byte(`{"content":[{"type":"text","text":"ok"}],"is_error":false}`), nil
		})

	_, err := access.ExecuteMCPAsProxy(ctx, "mcp-1", "tool", interfaces.MCPExecutionRequest{})
	if err != nil {
		t.Fatalf("ExecuteMCP() error = %v", err)
	}
}

func TestToolAndMCPFailuresDoNotReturnRawResultValues(t *testing.T) {
	const watermark = "raw-property-watermark-1342"
	tests := []struct {
		name     string
		target   string
		response string
		execute  func(*agentOperatorAccess, context.Context) error
	}{
		{
			name: "tool", target: "http://operator/tool-box/box-1/proxy/tool-1",
			response: `{"status_code":500,"body":{"value":"` + watermark + `"}}`,
			execute: func(access *agentOperatorAccess, ctx context.Context) error {
				_, err := access.ExecuteToolAsProxy(ctx, "box-1", "tool-1", interfaces.ToolExecutionRequest{})
				return err
			},
		},
		{
			name: "mcp", target: "http://operator/mcp/proxy/mcp-1/tool/call",
			response: `{"content":[{"type":"text","text":"` + watermark + `"}],"is_error":true}`,
			execute: func(access *agentOperatorAccess, ctx context.Context) error {
				_, err := access.ExecuteMCPAsProxy(ctx, "mcp-1", "tool", interfaces.MCPExecutionRequest{})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			httpClient := rmock.NewMockHTTPClient(ctrl)
			access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
			targetType := interfaces.ProxyTargetTypeToolBox
			targetID := "box-1"
			if test.name == "mcp" {
				targetType = interfaces.ProxyTargetTypeMCP
				targetID = "mcp-1"
			}
			httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), test.target, gomock.Any(), gomock.Any()).
				Return(http.StatusOK, []byte(test.response), nil)

			err := test.execute(access, actionProxyContext(targetType, targetID))
			if err == nil || strings.Contains(err.Error(), watermark) {
				t.Fatalf("failure exposed raw result: %v", err)
			}
		})
	}
}

func TestExecuteToolKeepsDirectCallerSemanticsForNonActionUses(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
	caller := interfaces.AccountInfo{ID: "caller-1", Type: "user"}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, caller)

	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(),
		"http://operator/tool-box/box-1/proxy/tool-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			if headers[interfaces.HTTP_HEADER_ACCOUNT_ID] != caller.ID ||
				headers[interfaces.HTTP_HEADER_ACCOUNT_TYPE] != caller.Type ||
				headers[interfaces.HTTPHeaderBKNProxyVersion] != "" {
				t.Fatalf("unexpected direct-caller headers: %#v", headers)
			}
			return http.StatusOK, []byte(`{"status_code":200,"body":{"ok":true}}`), nil
		})

	if _, err := access.ExecuteTool(ctx, "box-1", "tool-1", interfaces.ToolExecutionRequest{}); err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
}

func TestAgentOperatorRejectsForgedOrMissingProxyContextBeforeIO(t *testing.T) {
	access := &agentOperatorAccess{}
	if _, err := access.ExecuteToolAsProxy(context.Background(), "box-1", "tool-1", interfaces.ToolExecutionRequest{}); err == nil {
		t.Fatal("expected missing proxy context to fail")
	}
	ctx := actionProxyContext(interfaces.ProxyTargetTypeToolBox, "box-2")
	if _, err := access.ExecuteToolAsProxy(ctx, "box-1", "tool-1", interfaces.ToolExecutionRequest{}); err == nil {
		t.Fatal("expected unbound target to fail")
	}
}

var commonSettingForProxyTest = common.AppSetting{
	ToolBoxUrl: "http://operator/tool-box",
	MCPUrl:     "http://operator/mcp",
}

func actionProxyContext(targetType, targetID string) context.Context {
	caller := interfaces.AccountInfo{ID: "caller-1", Type: "user"}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, caller)
	return interfaces.WithTrustedProxyContext(ctx, &interfaces.TrustedProxyContext{
		Caller:                caller,
		Proxy:                 interfaces.AccountInfo{ID: "proxy-1", Type: interfaces.ProxyAccountTypeApp},
		ProxyVersion:          8,
		PublishedModelVersion: "v8",
		ExecutionID:           "execution-1",
		Binding: interfaces.TrustedProxyBinding{
			KNID:       "kn-1",
			ChildType:  interfaces.PermissionResourceTypeActionType,
			ChildID:    "action-1",
			TargetType: targetType,
			TargetID:   targetID,
			Operation:  interfaces.PermissionOperationExecute,
		},
	})
}

func assertActionProxyHeaders(t *testing.T, headers map[string]string, targetID string) {
	t.Helper()
	if headers[interfaces.HTTP_HEADER_ACCOUNT_ID] != "proxy-1" ||
		headers[interfaces.HTTP_HEADER_ACCOUNT_TYPE] != interfaces.ProxyAccountTypeApp ||
		headers[interfaces.HTTPHeaderBKNCallerID] != "caller-1" ||
		headers[interfaces.HTTPHeaderBKNKnowledgeID] != "kn-1" ||
		headers[interfaces.HTTPHeaderBKNChildID] != "action-1" ||
		headers[interfaces.HTTPHeaderBKNProxyVersion] != "8" ||
		headers[interfaces.HTTPHeaderBKNTargetID] != targetID ||
		headers[interfaces.HTTPHeaderBKNOperation] != interfaces.PermissionOperationExecute ||
		headers[interfaces.HTTPHeaderBKNExecutionID] != "execution-1" {
		t.Fatalf("unexpected proxy headers: %#v", headers)
	}
}
