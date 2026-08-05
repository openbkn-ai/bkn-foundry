// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knskills"
)

// executeSkillEnabledEnv 控制 execute_skill 是否出现在工具面。
//
// 这条工具把模型生成的 shell 命令送进沙箱执行，是整个工具面唯一的命令执行通道，
// 所以默认不装配：未开启时它既不在 tools/list 也不在 /mcp/info，与从未编译进来无异。
const executeSkillEnabledEnv = "MCP_EXECUTE_SKILL_ENABLED"

// executeSkillEnabled 判断是否装配 execute_skill。默认关。
func executeSkillEnabled() bool {
	value := strings.TrimSpace(os.Getenv(executeSkillEnabledEnv))
	if value == "" {
		return false
	}
	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled
}

// handleListSkills handles list_skills tool calls: 浏览已发布技能，不需要知识网络上下文。
func handleListSkills(svc knskills.KnSkillsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		listReq := &knskills.ListSkillsReq{}
		if err := bindArguments(req, listReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := svc.ListSkills(ctx, listReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleGetSkillContent handles get_skill_content tool calls: SKILL.md 正文 + 包内文件清单。
func handleGetSkillContent(svc knskills.KnSkillsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		skillID := getStringArg(req, "skill_id", "")
		if skillID == "" {
			return mcp.NewToolResultError(knskills.ErrSkillIDRequired.Error()), nil
		}

		resp, err := svc.GetSkillContent(ctx, skillID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleReadSkillFile handles read_skill_file tool calls: 按 rel_path 取技能包内单个文件正文。
func handleReadSkillFile(svc knskills.KnSkillsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		readReq := &knskills.ReadSkillFileReq{}
		if err := bindArguments(req, readReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if readReq.SkillID == "" {
			return mcp.NewToolResultError(knskills.ErrSkillIDRequired.Error()), nil
		}
		if readReq.RelPath == "" {
			return mcp.NewToolResultError(knskills.ErrRelPathRequired.Error()), nil
		}

		resp, err := svc.ReadSkillFile(ctx, readReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleExecuteSkill handles execute_skill tool calls: 在沙箱内执行技能入口命令。
// 授权由执行工厂按账户强制（execute / public_access 二者之一）。
func handleExecuteSkill(svc knskills.KnSkillsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		execReq := &knskills.ExecuteSkillReq{}
		if err := bindArguments(req, execReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if execReq.SkillID == "" {
			return mcp.NewToolResultError(knskills.ErrSkillIDRequired.Error()), nil
		}
		if execReq.EntryShell == "" {
			return mcp.NewToolResultError(knskills.ErrEntryShellRequired.Error()), nil
		}

		resp, err := svc.ExecuteSkill(ctx, execReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// 与 execute_action 一致：执行结果始终返回 JSON，exit_code / stdout 需机器可消费。
		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}
