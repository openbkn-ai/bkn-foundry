// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func TestInternalCapabilityCallsUseCallerScopedAuthorizationFace(t *testing.T) {
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	client := &operatorIntegrationClient{
		logger: logger, baseURL: "http://operator/api/agent-operator-integration", httpClient: httpClient,
	}
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
	seen := map[string]bool{}
	assertCallerScoped := func(target string, headers map[string]string) {
		t.Helper()
		if headers[string(interfaces.HeaderXAccountID)] != "user-1" ||
			headers[string(interfaces.HeaderXAccountType)] != string(interfaces.AccessorTypeUser) {
			t.Fatalf("caller headers = %#v", headers)
		}
		if headers["Authorization"] != "" {
			t.Fatalf("internal caller-scoped request must not carry Authorization: %#v", headers)
		}
		seen[target] = true
	}

	httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, _ url.Values, headers map[string]string) (int, interface{}, error) {
			assertCallerScoped(target, headers)
			switch target {
			case client.baseURL + "/internal-v1/caller/tool-box/box-1/tool/tool-1":
				return http.StatusOK, map[string]any{"tool_id": "tool-1", "name": "tool"}, nil
			case client.baseURL + "/internal-v1/caller/tool-box/list":
				return http.StatusOK, map[string]any{"data": []any{}}, nil
			case client.baseURL + "/internal-v1/caller/tool-box/box-1/tools/list":
				return http.StatusOK, map[string]any{"tools": []any{}}, nil
			case client.baseURL + "/internal-v1/caller/mcp/proxy/mcp-1/tools":
				return http.StatusOK, map[string]any{"tools": []any{map[string]any{"name": "tool"}}}, nil
			case client.baseURL + "/internal-v1/caller/skills/available":
				return http.StatusOK, map[string]any{"data": []any{}}, nil
			case client.baseURL + "/internal-v1/caller/skills/skill-1/content":
				return http.StatusOK, map[string]any{"skill_id": "skill-1", "url": "http://asset/skill.md"}, nil
			default:
				t.Fatalf("unexpected GET target %q", target)
				return 0, nil, nil
			}
		}).Times(6)
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://asset/skill.md", gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte("skill body"), nil)
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://asset/a.md", gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte("file body"), nil)
	httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, headers map[string]string, _ interface{}) (int, interface{}, error) {
			assertCallerScoped(target, headers)
			switch target {
			case client.baseURL + "/internal-v1/caller/tool-box/box-1/proxy/tool-1":
				return http.StatusOK, map[string]any{"ok": true}, nil
			case client.baseURL + "/internal-v1/caller/skills/skill-1/files/read":
				return http.StatusOK, map[string]any{"skill_id": "skill-1", "rel_path": "a.md", "url": "http://asset/a.md"}, nil
			case client.baseURL + "/internal-v1/caller/skills/skill-1/execute":
				return http.StatusOK, map[string]any{"skill_id": "skill-1", "exit_code": 0}, nil
			default:
				t.Fatalf("unexpected POST target %q", target)
				return 0, nil, nil
			}
		}).Times(3)
	httpClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, headers map[string]string, _ interface{}) (int, []byte, error) {
			assertCallerScoped(target, headers)
			if target != client.baseURL+"/internal-v1/caller/mcp/proxy/mcp-1/tool/call" {
				t.Fatalf("unexpected PostBytes target %q", target)
			}
			return http.StatusOK, []byte(`{"ok":true}`), nil
		})

	if _, err := client.GetToolDetail(ctx, &interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListPublishedToolboxes(ctx, &interfaces.ListPublishedToolboxesRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListPublishedTools(ctx, &interfaces.ListPublishedToolsRequest{ToolboxID: "box-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExecutePublishedTool(ctx, &interfaces.ExecutePublishedToolRequest{ToolboxID: "box-1", ToolID: "tool-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetMCPToolDetail(ctx, &interfaces.GetMCPToolDetailRequest{McpID: "mcp-1", ToolName: "tool"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallMCPTool(ctx, &interfaces.CallMCPToolRequest{McpID: "mcp-1", ToolName: "tool"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListSkills(ctx, &interfaces.ListSkillsRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetSkillContent(ctx, "skill-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadSkillFile(ctx, &interfaces.ReadSkillFileRequest{SkillID: "skill-1", RelPath: "a.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExecuteSkill(ctx, &interfaces.ExecuteSkillRequest{SkillID: "skill-1", EntryShell: "run"}); err != nil {
		t.Fatal(err)
	}

	wantTargets := []string{
		"/internal-v1/caller/tool-box/box-1/tool/tool-1",
		"/internal-v1/caller/tool-box/list",
		"/internal-v1/caller/tool-box/box-1/tools/list",
		"/internal-v1/caller/tool-box/box-1/proxy/tool-1",
		"/internal-v1/caller/mcp/proxy/mcp-1/tools",
		"/internal-v1/caller/mcp/proxy/mcp-1/tool/call",
		"/internal-v1/caller/skills/available",
		"/internal-v1/caller/skills/skill-1/content",
		"/internal-v1/caller/skills/skill-1/files/read",
		"/internal-v1/caller/skills/skill-1/execute",
	}
	for _, target := range wantTargets {
		if !seen[client.baseURL+target] {
			t.Errorf("caller-scoped route not called: %s", target)
		}
	}
}

func TestPublicCapabilityCallWithoutCallerTokenIsUnauthorized(t *testing.T) {
	client := &operatorIntegrationClient{}
	ctx := common.SetPublicAPIToCtx(context.Background(), true)
	_, err := client.capabilityAuthorizationHeaderForAuthMode(ctx, "operator.skill.list", true)
	status, ok := infraErr.HTTPStatus(err)
	if !ok || status != http.StatusUnauthorized {
		t.Fatalf("status = %d, %v; want 401", status, ok)
	}
}

func TestPublicCapabilityCallWithoutCallerTokenAllowsDisabledAuth(t *testing.T) {
	client := &operatorIntegrationClient{}
	ctx := common.SetPublicAPIToCtx(context.Background(), true)
	header, err := client.capabilityAuthorizationHeaderForAuthMode(ctx, "operator.skill.list", false)
	if err != nil {
		t.Fatalf("capabilityAuthorizationHeaderForAuthMode: %v", err)
	}
	if authorization := header["Authorization"]; authorization != "" {
		t.Fatalf("Authorization = %q; want empty", authorization)
	}
}

func TestPublicArbitraryFunctionExecutionStillRequiresCallerToken(t *testing.T) {
	client := &operatorIntegrationClient{}
	ctx := common.SetPublicAPIToCtx(context.Background(), true)
	_, err := client.ExecuteFunction(ctx, &interfaces.ExecuteFunctionRequest{Code: "print('ok')"})
	status, ok := infraErr.HTTPStatus(err)
	if !ok || status != http.StatusUnauthorized {
		t.Fatalf("status = %d, %v; want 401", status, ok)
	}
}

func TestInternalArbitraryFunctionExecutionRemainsForbidden(t *testing.T) {
	client := &operatorIntegrationClient{}
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})

	_, err := client.ExecuteFunction(ctx, &interfaces.ExecuteFunctionRequest{Code: "print('ok')"})
	status, ok := infraErr.HTTPStatus(err)
	if !ok || status != http.StatusForbidden {
		t.Fatalf("status = %d, %v; want 403", status, ok)
	}
}

func TestExecutionFactoryResourceCallsUsePublicAuthorizationFace(t *testing.T) {
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	client := &operatorIntegrationClient{
		logger: logger, baseURL: "http://operator/api/agent-operator-integration", httpClient: httpClient,
	}
	ctx := common.SetRawTokenToCtx(context.Background(), "caller-token")
	assertAuth := func(url string, headers map[string]string, wantPath string) {
		t.Helper()
		if url != client.baseURL+wantPath {
			t.Fatalf("url = %q, want %q", url, client.baseURL+wantPath)
		}
		if headers["Authorization"] != "Bearer caller-token" {
			t.Fatalf("Authorization = %q", headers["Authorization"])
		}
	}

	httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, _ url.Values, headers map[string]string) (int, interface{}, error) {
			assertAuth(target, headers, "/v1/tool-box/box-1/tool/tool-1")
			return http.StatusOK, map[string]any{"tool_id": "tool-1", "name": "tool"}, nil
		})
	if _, err := client.GetToolDetail(ctx, &interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"}); err != nil {
		t.Fatal(err)
	}

	httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, _ url.Values, headers map[string]string) (int, interface{}, error) {
			assertAuth(target, headers, "/v1/mcp/proxy/mcp-1/tools")
			return http.StatusOK, map[string]any{"tools": []any{map[string]any{"name": "tool"}}}, nil
		})
	if _, err := client.GetMCPToolDetail(ctx, &interfaces.GetMCPToolDetailRequest{McpID: "mcp-1", ToolName: "tool"}); err != nil {
		t.Fatal(err)
	}

	httpClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, headers map[string]string, _ interface{}) (int, []byte, error) {
			assertAuth(target, headers, "/v1/mcp/proxy/mcp-1/tool/call")
			return http.StatusOK, []byte(`{"ok":true}`), nil
		})
	if _, err := client.CallMCPTool(ctx, &interfaces.CallMCPToolRequest{McpID: "mcp-1", ToolName: "tool"}); err != nil {
		t.Fatal(err)
	}
}

func TestSkillCallsUsePublicAuthorizationFace(t *testing.T) {
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	client := &operatorIntegrationClient{
		logger: logger, baseURL: "http://operator/api/agent-operator-integration", httpClient: httpClient,
	}
	ctx := common.SetRawTokenToCtx(context.Background(), "caller-token")
	assertAuth := func(url string, headers map[string]string, wantPath string) {
		t.Helper()
		if url != client.baseURL+wantPath || headers["Authorization"] != "Bearer caller-token" {
			t.Fatalf("request = %q Authorization=%q", url, headers["Authorization"])
		}
	}

	httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, query url.Values, headers map[string]string) (int, interface{}, error) {
			assertAuth(target, headers, "/v1/skills/available")
			if query.Has("status") {
				t.Fatalf("available release list must not filter mutable repository status: %v", query)
			}
			return http.StatusOK, map[string]any{"data": []any{map[string]any{"skill_id": "skill-1", "name": "skill"}}}, nil
		})
	if _, err := client.ListSkills(ctx, &interfaces.ListSkillsRequest{}); err != nil {
		t.Fatal(err)
	}

	httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, _ url.Values, headers map[string]string) (int, interface{}, error) {
			assertAuth(target, headers, "/v1/skills/skill-1/content")
			return http.StatusOK, map[string]any{"skill_id": "skill-1", "url": "http://asset/skill.md"}, nil
		})
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://asset/skill.md", gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte("skill body"), nil)
	if _, err := client.GetSkillContent(ctx, "skill-1"); err != nil {
		t.Fatal(err)
	}

	httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, headers map[string]string, body interface{}) (int, interface{}, error) {
			assertAuth(target, headers, "/v1/skills/skill-1/files/read")
			if body.(map[string]string)["rel_path"] != "references/api.md" {
				t.Fatalf("file read body = %v", body)
			}
			return http.StatusOK, map[string]any{
				"skill_id": "skill-1", "rel_path": "references/api.md", "url": "http://asset/api.md",
			}, nil
		})
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://asset/api.md", gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte("api reference"), nil)
	if _, err := client.ReadSkillFile(ctx, &interfaces.ReadSkillFileRequest{
		SkillID: "skill-1", RelPath: "references/api.md",
	}); err != nil {
		t.Fatal(err)
	}

	httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, headers map[string]string, _ interface{}) (int, interface{}, error) {
			assertAuth(target, headers, "/v1/skills/skill-1/execute")
			return http.StatusOK, map[string]any{"skill_id": "skill-1", "exit_code": 0}, nil
		})
	if _, err := client.ExecuteSkill(ctx, &interfaces.ExecuteSkillRequest{SkillID: "skill-1", EntryShell: "run"}); err != nil {
		t.Fatal(err)
	}
}
