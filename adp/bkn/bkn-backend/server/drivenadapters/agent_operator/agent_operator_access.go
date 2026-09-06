// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package agent_operator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

var (
	aoAccessOnce sync.Once
	aoAccess     interfaces.AgentOperatorAccess
)

type agentOperatorAccess struct {
	appSetting       *common.AppSetting
	agentOperatorURL string
	httpClient       rest.HTTPClient
}

type OperatorError struct {
	Code        string      `json:"code"`        // Error code
	Description string      `json:"description"` // Error description
	Detail      interface{} `json:"detail"`      // Error details
	Solution    interface{} `json:"solution"`    // Suggested resolution
	Link        interface{} `json:"link"`        // Error link
}

// NewAgentOperatorAccess returns a singleton ToolBox and MCP access client.
func NewAgentOperatorAccess(appSetting *common.AppSetting) interfaces.AgentOperatorAccess {
	aoAccessOnce.Do(func() {
		aoAccess = &agentOperatorAccess{
			appSetting:       appSetting,
			agentOperatorURL: appSetting.AgentOperatorUrl,
			httpClient:       common.NewHTTPClient(),
		}
	})
	return aoAccess
}

// GetToolByID verifies tool-box tool exists (GET .../tool-box/{box_id}/tool/{tool_id}).
func (aoa *agentOperatorAccess) GetToolByID(ctx context.Context, boxID, toolID string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetToolByID")
	defer span.End()

	if boxID == "" || toolID == "" {
		err := fmt.Errorf("box_id and tool_id are required for tool binding check")
		common.LogSafeError(ctx, "Invalid tool binding parameter", err)
		return err
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	url := fmt.Sprintf("%s/tool-box/%s/tool/%s", aoa.agentOperatorURL, boxID, toolID)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         url,
		HttpMethod:      http.MethodGet,
		HttpContentType: rest.ContentTypeJson,
	})

	start := time.Now().UnixMilli()
	respCode, result, err := aoa.httpClient.GetNoUnmarshal(ctx, url, nil, headers)
	logger.Debugf("tool binding check response code [%d], took %dms, %s",
		respCode, time.Now().UnixMilli()-start, common.SafeErrorSummary(err))

	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get tool failed")
		common.LogSafeError(ctx, "Tool binding check request failed", err)
		return fmt.Errorf("tool binding check failed: %w", err)
	}
	if respCode == http.StatusOK {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil
	}
	if respCode == http.StatusNotFound {
		err := fmt.Errorf("tool not found: box_id=%s tool_id=%s", boxID, toolID)
		oteltrace.AddHttpAttrs4Error(span, respCode, "NotFound", "Tool not found")
		common.LogSafeError(ctx, "Tool not found", err)
		return err
	}
	if respCode != http.StatusOK {
		var opError OperatorError
		if err = json.Unmarshal(result, &opError); err != nil {
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal OperatorError failed")
			common.LogSafeError(ctx, "Unmarshal OperatorError failed", err)
			return fmt.Errorf("tool binding check failed: %w", err)
		}
		httpErr := &rest.HTTPError{HTTPCode: respCode,
			BaseError: rest.BaseError{
				ErrorCode:    opError.Code,
				Description:  opError.Description,
				ErrorDetails: opError.Detail,
			}}
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		common.LogSafeError(ctx, "Tool binding check failed", httpErr)
		return fmt.Errorf("tool binding check returned HTTP %d", respCode)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

// CheckMCPToolBinding verifies MCP exposes a tool with toolName (GET .../mcp/proxy/{mcp_id}/tools).
func (aoa *agentOperatorAccess) GetMcpToolByName(ctx context.Context, mcpID, toolName string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetMcpToolByName")
	defer span.End()

	if mcpID == "" || toolName == "" {
		err := fmt.Errorf("mcp_id and tool_name are required for MCP tool binding check")
		common.LogSafeError(ctx, "Invalid MCP tool binding parameter", err)
		return err
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	url := fmt.Sprintf("%s/mcp/proxy/%s/tools", aoa.agentOperatorURL, mcpID)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         url,
		HttpMethod:      http.MethodGet,
		HttpContentType: rest.ContentTypeJson,
	})

	start := time.Now().UnixMilli()
	respCode, result, err := aoa.httpClient.GetNoUnmarshal(ctx, url, nil, headers)
	logger.Debugf("MCP tool binding check response code [%d], took %dms, %s",
		respCode, time.Now().UnixMilli()-start, common.SafeErrorSummary(err))

	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get MCP tools failed")
		common.LogSafeError(ctx, "MCP tools list request failed", err)
		return fmt.Errorf("MCP tool binding check failed: %w", err)
	}
	if respCode != http.StatusOK {
		if respCode == http.StatusNotFound {
			err := fmt.Errorf("MCP server not found: mcp_id=%s", mcpID)
			oteltrace.AddHttpAttrs4Error(span, respCode, "NotFound", "MCP server not found")
			common.LogSafeError(ctx, "MCP server not found", err)
			return err
		}

		var opError OperatorError
		if len(result) > 0 && json.Unmarshal(result, &opError) == nil && opError.Description != "" {
			err := fmt.Errorf("MCP tool binding check failed (status %d): %s", respCode, opError.Description)
			oteltrace.AddHttpAttrs4Error(span, respCode, opError.Code, opError.Description)
			common.LogSafeError(ctx, "MCP tool binding check failed", err)
			return err
		}
		err := fmt.Errorf("MCP tool binding check failed: unexpected status %d", respCode)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unexpected MCP tool binding response status")
		common.LogSafeError(ctx, "MCP tool binding check failed", err)
		return err
	}
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &list); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Parse MCP tools response failed")
		common.LogSafeError(ctx, "Parse MCP tools response failed", err)
		return fmt.Errorf("parse MCP tools response: %w", err)
	}
	want := strings.TrimSpace(toolName)
	for _, t := range list.Tools {
		if strings.TrimSpace(t.Name) == want {
			oteltrace.AddHttpAttrs4Ok(span, respCode)
			return nil
		}
	}
	err = fmt.Errorf("MCP tool not found: mcp_id=%s tool_name=%s", mcpID, toolName)
	common.LogSafeError(ctx, "MCP tool not found", err)
	return err
}

// execFactoryHeaders carries the caller's account to the execution factory. The internal face
// authenticates by header, not by token.
func (aoa *agentOperatorAccess) execFactoryHeaders(ctx context.Context) map[string]string {
	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	return map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}
}

// GetSkillByID reads a skill from the execution factory (GET .../skills/{skill_id}).
//
// A missing skill is (nil, nil) rather than an error: "no such skill" and "exists but
// unpublished" are different answers to a mount request and the caller has to tell them apart.
func (aoa *agentOperatorAccess) GetSkillByID(ctx context.Context, skillID string) (*interfaces.SkillBrief, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetSkillByID")
	defer span.End()

	if skillID == "" {
		return nil, fmt.Errorf("skill_id is required for skill binding check")
	}

	url := fmt.Sprintf("%s/skills/%s", aoa.agentOperatorURL, skillID)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         url,
		HttpMethod:      http.MethodGet,
		HttpContentType: rest.ContentTypeJson,
	})

	respCode, result, err := aoa.httpClient.GetNoUnmarshal(ctx, url, nil, aoa.execFactoryHeaders(ctx))
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get skill failed")
		common.LogSafeError(ctx, "Skill binding check request failed", err)
		return nil, fmt.Errorf("skill binding check failed: %w", err)
	}
	if respCode == http.StatusNotFound {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	if respCode != http.StatusOK {
		common.LogSafeError(ctx, "Skill binding check failed",
			fmt.Errorf("skill binding check returned HTTP %d", respCode))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Get skill failed")
		return nil, fmt.Errorf("skill binding check returned HTTP %d", respCode)
	}

	var payload struct {
		SkillID     string `json:"skill_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Status      string `json:"status"`
	}
	if err = json.Unmarshal(result, &payload); err != nil {
		common.LogSafeError(ctx, "Unmarshal skill detail failed", err)
		return nil, fmt.Errorf("skill binding check failed: %w", err)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &interfaces.SkillBrief{
		SkillID:     payload.SkillID,
		Name:        payload.Name,
		Description: payload.Description,
		Status:      payload.Status,
	}, nil
}

// ListBoxTools reads a tool box and its inlined tools (GET .../tool-box/{box_id}).
//
// A missing box is (nil, nil), on the same terms as GetSkillByID.
func (aoa *agentOperatorAccess) ListBoxTools(ctx context.Context, boxID string) ([]*interfaces.ToolBrief, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListBoxTools")
	defer span.End()

	if boxID == "" {
		return nil, fmt.Errorf("box_id is required for tool box lookup")
	}

	url := fmt.Sprintf("%s/tool-box/%s", aoa.agentOperatorURL, boxID)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         url,
		HttpMethod:      http.MethodGet,
		HttpContentType: rest.ContentTypeJson,
	})

	respCode, result, err := aoa.httpClient.GetNoUnmarshal(ctx, url, nil, aoa.execFactoryHeaders(ctx))
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get tool box failed")
		common.LogSafeError(ctx, "Tool box lookup request failed", err)
		return nil, fmt.Errorf("tool box lookup failed: %w", err)
	}
	if respCode == http.StatusNotFound {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	if respCode != http.StatusOK {
		common.LogSafeError(ctx, "Tool box lookup failed",
			fmt.Errorf("tool box lookup returned HTTP %d", respCode))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Get tool box failed")
		return nil, fmt.Errorf("tool box lookup returned HTTP %d", respCode)
	}

	var payload struct {
		BoxID      string `json:"box_id"`
		BoxName    string `json:"box_name"`
		Status     string `json:"status"`
		IsInternal bool   `json:"is_internal"`
		Tools      []struct {
			ToolID      string `json:"tool_id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Status      string `json:"status"`
		} `json:"tools"`
	}
	if err = json.Unmarshal(result, &payload); err != nil {
		common.LogSafeError(ctx, "Unmarshal tool box detail failed", err)
		return nil, fmt.Errorf("tool box lookup failed: %w", err)
	}

	tools := make([]*interfaces.ToolBrief, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		tools = append(tools, &interfaces.ToolBrief{
			BoxID:       payload.BoxID,
			BoxName:     payload.BoxName,
			BoxStatus:   payload.Status,
			BoxInternal: payload.IsInternal,
			ToolID:      tool.ToolID,
			Name:        tool.Name,
			Description: tool.Description,
			Status:      tool.Status,
		})
	}
	// make() above keeps this non-nil even for a box with no tools, which matters: nil is
	// reserved for "no such box" and the caller answers the two cases differently.
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return tools, nil
}
