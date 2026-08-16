// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knskills"
)

// handleListSkills handles list_skills tool calls without a knowledge-network context.
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

// handleGetSkillContent handles get_skill_content calls for SKILL.md and its file list.
func handleGetSkillContent(svc knskills.KnSkillsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		skillID := getStringArg(req, "skill_id", "")
		if skillID == "" {
			return mcp.NewToolResultError(knskills.SkillIDRequiredError(ctx).Error()), nil
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

// handleReadSkillFile handles read_skill_file calls for one skill package file.
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
			return mcp.NewToolResultError(knskills.SkillIDRequiredError(ctx).Error()), nil
		}
		if readReq.RelPath == "" {
			return mcp.NewToolResultError(knskills.RelPathRequiredError(ctx).Error()), nil
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

// handleExecuteSkill runs a skill entry command in the sandbox.
// Execution Factory enforces either execute or public_access authorization.
func handleExecuteSkill(svc knskills.KnSkillsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		execReq := &knskills.ExecuteSkillReq{}
		if err := bindArguments(req, execReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if execReq.SkillID == "" {
			return mcp.NewToolResultError(knskills.SkillIDRequiredError(ctx).Error()), nil
		}
		if execReq.EntryShell == "" {
			return mcp.NewToolResultError(knskills.EntryShellRequiredError(ctx).Error()), nil
		}

		resp, err := svc.ExecuteSkill(ctx, execReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Match execute_action: execution results are always machine-readable JSON.
		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}
