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

func TestExecutionFactoryResourceCallsRequireOriginalCallerToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	client := &operatorIntegrationClient{
		logger: logger, baseURL: "http://operator/api/agent-operator-integration", httpClient: httpClient,
	}

	calls := []struct {
		name string
		call func() error
	}{
		{"tool detail", func() error {
			_, err := client.GetToolDetail(context.Background(), &interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"})
			return err
		}},
		{"mcp tools", func() error {
			_, err := client.GetMCPToolDetail(context.Background(), &interfaces.GetMCPToolDetailRequest{McpID: "mcp-1", ToolName: "tool"})
			return err
		}},
		{"mcp execute", func() error {
			_, err := client.CallMCPTool(context.Background(), &interfaces.CallMCPToolRequest{McpID: "mcp-1", ToolName: "tool"})
			return err
		}},
		{"skill list", func() error {
			_, err := client.ListSkills(context.Background(), &interfaces.ListSkillsRequest{})
			return err
		}},
		{"skill content", func() error {
			_, err := client.GetSkillContent(context.Background(), "skill-1")
			return err
		}},
		{"skill file", func() error {
			_, err := client.ReadSkillFile(context.Background(), &interfaces.ReadSkillFileRequest{SkillID: "skill-1", RelPath: "a.md"})
			return err
		}},
		{"skill execute", func() error {
			_, err := client.ExecuteSkill(context.Background(), &interfaces.ExecuteSkillRequest{SkillID: "skill-1", EntryShell: "run"})
			return err
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			status, ok := infraErr.HTTPStatus(tc.call())
			if !ok || status != http.StatusUnauthorized {
				t.Fatalf("status = %d, %v; want 401", status, ok)
			}
		})
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
			assertAuth(target, headers, "/v1/skills")
			if query["status"][0] != "published" {
				t.Fatalf("status query = %v", query["status"])
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
