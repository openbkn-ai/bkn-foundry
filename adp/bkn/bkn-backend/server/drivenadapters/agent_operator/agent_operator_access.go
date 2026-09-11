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
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

// execFactoryNameLookupPageSize bounds a name lookup. A name that matches more entries than this
// is ambiguous anyway, and the import refuses to guess between them.
const execFactoryNameLookupPageSize = 50

var (
	aoAccessOnce sync.Once
	aoAccess     interfaces.AgentOperatorAccess
)

type agentOperatorAccess struct {
	appSetting       *common.AppSetting
	agentOperatorURL string
	httpClient       rest.HTTPClient
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
		err := interfaces.NewDependencyError("execution-factory", "get_tool",
			interfaces.DependencyInvalidBinding, 0)
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
		return executionDependencyTransportError("get_tool", respCode, err)
	}
	if respCode == http.StatusOK {
		var payload struct {
			ToolID string `json:"tool_id"`
		}
		if err := json.Unmarshal(result, &payload); err != nil || strings.TrimSpace(payload.ToolID) != toolID {
			invalidErr := interfaces.NewDependencyError("execution-factory", "get_tool",
				interfaces.DependencyInvalidResponse, respCode)
			if err != nil {
				common.LogSafeError(ctx, "Tool binding response was invalid", err)
			} else {
				common.LogSafeError(ctx, "Tool binding response was invalid", invalidErr)
			}
			return invalidErr
		}
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil
	}
	kind := executionDependencyKindForStatus(respCode)
	oteltrace.AddHttpAttrs4Error(span, respCode, "DependencyError", "Tool binding check failed")
	logger.Debugf("Tool binding check response: %s", common.SafeTextSummary("response", string(result)))
	return interfaces.NewDependencyError("execution-factory", "get_tool", kind, respCode)
}

// CheckMCPToolBinding verifies MCP exposes a tool with toolName (GET .../mcp/proxy/{mcp_id}/tools).
func (aoa *agentOperatorAccess) GetMcpToolByName(ctx context.Context, mcpID, toolName string) error {
	if mcpID == "" || toolName == "" {
		err := interfaces.NewDependencyError("execution-factory", "get_mcp_tool",
			interfaces.DependencyInvalidBinding, 0)
		common.LogSafeError(ctx, "Invalid MCP tool binding parameter", err)
		return err
	}

	// One reader for the MCP tool listing, not two. This answers only "does that name exist",
	// which is all the action type needs; a capability binding also has to know whether the
	// server is published, and reads the same list through ListMCPTools to find out.
	tools, err := aoa.ListMCPTools(ctx, mcpID)
	if err != nil {
		return err
	}
	if tools == nil {
		return interfaces.NewDependencyError("execution-factory", "get_mcp_tool",
			interfaces.DependencyNotFound, http.StatusNotFound)
	}
	want := strings.TrimSpace(toolName)
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == want {
			return nil
		}
	}
	return interfaces.NewDependencyError("execution-factory", "get_mcp_tool",
		interfaces.DependencyNotFound, http.StatusNotFound)
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
		BoxID        string `json:"box_id"`
		BoxName      string `json:"box_name"`
		MetadataType string `json:"metadata_type"`
		Status       string `json:"status"`
		IsInternal   bool   `json:"is_internal"`
		Tools        []struct {
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
			BoxID:           payload.BoxID,
			BoxName:         payload.BoxName,
			BoxMetadataType: payload.MetadataType,
			BoxStatus:       payload.Status,
			BoxInternal:     payload.IsInternal,
			ToolID:          tool.ToolID,
			Name:            tool.Name,
			Description:     tool.Description,
			Status:          tool.Status,
		})
	}
	// make() above keeps this non-nil even for a box with no tools, which matters: nil is
	// reserved for "no such box" and the caller answers the two cases differently.
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return tools, nil
}

// GetSkillNamesByIDs resolves skill names in bulk (POST .../skills/names).
//
// The endpoint's contract is that unknown IDs are skipped rather than returned empty, which is
// what makes the difference between the request and the answer a usable dangling-reference set.
func (aoa *agentOperatorAccess) GetSkillNamesByIDs(ctx context.Context, skillIDs []string) (map[string]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetSkillNamesByIDs")
	defer span.End()

	names := map[string]string{}
	if len(skillIDs) == 0 {
		return names, nil
	}

	url := fmt.Sprintf("%s/skills/names", aoa.agentOperatorURL)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         url,
		HttpMethod:      http.MethodPost,
		HttpContentType: rest.ContentTypeJson,
	})

	respCode, result, err := aoa.httpClient.PostNoUnmarshal(ctx, url, aoa.execFactoryHeaders(ctx),
		map[string]any{"ids": skillIDs})
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http post skill names failed")
		common.LogSafeError(ctx, "Skill name lookup request failed", err)
		return nil, fmt.Errorf("skill name lookup failed: %w", err)
	}
	if respCode != http.StatusOK {
		common.LogSafeError(ctx, "Skill name lookup failed",
			fmt.Errorf("skill name lookup returned HTTP %d", respCode))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Post skill names failed")
		return nil, fmt.Errorf("skill name lookup returned HTTP %d", respCode)
	}

	var payload struct {
		Entries []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err = json.Unmarshal(result, &payload); err != nil {
		common.LogSafeError(ctx, "Unmarshal skill names failed", err)
		return nil, fmt.Errorf("skill name lookup failed: %w", err)
	}
	for _, entry := range payload.Entries {
		names[entry.ID] = entry.Name
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return names, nil
}

// FindSkillsByName resolves a skill name to every skill carrying it (GET .../skills?name=).
//
// The execution factory filters by substring, so the exact matches are picked out here: a model
// asking for "交期评估" must not resolve to "交期评估（旧）" because it happens to contain it.
func (aoa *agentOperatorAccess) FindSkillsByName(ctx context.Context, name string) ([]*interfaces.SkillBrief, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "FindSkillsByName")
	defer span.End()

	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/skills?name=%s&page_size=%d", aoa.agentOperatorURL, neturl.QueryEscape(name), execFactoryNameLookupPageSize)
	respCode, result, err := aoa.httpClient.GetNoUnmarshal(ctx, url, nil, aoa.execFactoryHeaders(ctx))
	if err != nil {
		common.LogSafeError(ctx, "Skill name lookup request failed", err)
		return nil, fmt.Errorf("skill name lookup failed: %w", err)
	}
	if respCode != http.StatusOK {
		return nil, fmt.Errorf("skill name lookup returned HTTP %d", respCode)
	}

	var payload struct {
		Data []struct {
			SkillID     string `json:"skill_id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Status      string `json:"status"`
		} `json:"data"`
	}
	if err = json.Unmarshal(result, &payload); err != nil {
		common.LogSafeError(ctx, "Unmarshal skill list failed", err)
		return nil, fmt.Errorf("skill name lookup failed: %w", err)
	}
	matches := make([]*interfaces.SkillBrief, 0, 1)
	for _, skill := range payload.Data {
		if skill.Name != name {
			continue
		}
		matches = append(matches, &interfaces.SkillBrief{
			SkillID:     skill.SkillID,
			Name:        skill.Name,
			Description: skill.Description,
			Status:      skill.Status,
		})
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return matches, nil
}

// FindToolBoxesByName resolves a tool box name (GET .../tool-box/list?name=), exact matches only,
// on the same terms as FindSkillsByName.
func (aoa *agentOperatorAccess) FindToolBoxesByName(ctx context.Context, name string) ([]*interfaces.ToolBoxBrief, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "FindToolBoxesByName")
	defer span.End()

	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/tool-box/list?name=%s&page_size=%d", aoa.agentOperatorURL, neturl.QueryEscape(name), execFactoryNameLookupPageSize)
	respCode, result, err := aoa.httpClient.GetNoUnmarshal(ctx, url, nil, aoa.execFactoryHeaders(ctx))
	if err != nil {
		common.LogSafeError(ctx, "Tool box name lookup request failed", err)
		return nil, fmt.Errorf("tool box name lookup failed: %w", err)
	}
	if respCode != http.StatusOK {
		return nil, fmt.Errorf("tool box name lookup returned HTTP %d", respCode)
	}

	var payload struct {
		Data []struct {
			BoxID   string `json:"box_id"`
			BoxName string `json:"box_name"`
			Status  string `json:"status"`
		} `json:"data"`
	}
	if err = json.Unmarshal(result, &payload); err != nil {
		common.LogSafeError(ctx, "Unmarshal tool box list failed", err)
		return nil, fmt.Errorf("tool box name lookup failed: %w", err)
	}
	matches := make([]*interfaces.ToolBoxBrief, 0, 1)
	for _, box := range payload.Data {
		if box.BoxName != name {
			continue
		}
		matches = append(matches, &interfaces.ToolBoxBrief{BoxID: box.BoxID, Name: box.BoxName, Status: box.Status})
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return matches, nil
}

// ListMCPTools reads the tools an MCP Server exposes, together with the server's own status.
//
// Two calls, not one: the proxy tool listing carries the tools but not the server's status, and a
// caller deciding whether a tool may be bound needs both — the same pair boxIsUsable and
// tool.Status answer for a tool box. A server that does not exist comes back as nil, nil.
func (aoa *agentOperatorAccess) ListMCPTools(ctx context.Context, mcpID string) ([]*interfaces.MCPToolBrief, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListMCPTools")
	defer span.End()

	if mcpID == "" {
		return nil, interfaces.NewDependencyError("execution-factory", "list_mcp_tools",
			interfaces.DependencyInvalidBinding, 0)
	}

	detailURL := fmt.Sprintf("%s/mcp/%s", aoa.agentOperatorURL, mcpID)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         detailURL,
		HttpMethod:      http.MethodGet,
		HttpContentType: rest.ContentTypeJson,
	})

	respCode, result, err := aoa.httpClient.GetNoUnmarshal(ctx, detailURL, nil, aoa.execFactoryHeaders(ctx))
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get MCP server failed")
		common.LogSafeError(ctx, "MCP server lookup request failed", err)
		return nil, executionDependencyTransportError("list_mcp_tools", respCode, err)
	}
	if respCode == http.StatusNotFound {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	if respCode != http.StatusOK {
		common.LogSafeError(ctx, "MCP server lookup failed",
			fmt.Errorf("MCP server lookup returned HTTP %d", respCode))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Get MCP server failed")
		return nil, interfaces.NewDependencyError("execution-factory", "list_mcp_tools",
			executionDependencyKindForStatus(respCode), respCode)
	}

	var detail struct {
		BaseInfo struct {
			MCPID  string `json:"mcp_id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"base_info"`
	}
	if err = json.Unmarshal(result, &detail); err != nil {
		common.LogSafeError(ctx, "Unmarshal MCP server detail failed", err)
		return nil, interfaces.NewDependencyError("execution-factory", "list_mcp_tools",
			interfaces.DependencyInvalidResponse, respCode)
	}
	if strings.TrimSpace(detail.BaseInfo.MCPID) != mcpID || strings.TrimSpace(detail.BaseInfo.Status) == "" {
		invalidErr := interfaces.NewDependencyError("execution-factory", "list_mcp_tools",
			interfaces.DependencyInvalidResponse, respCode)
		common.LogSafeError(ctx, "MCP server detail response was invalid", invalidErr)
		return nil, invalidErr
	}

	toolsURL := fmt.Sprintf("%s/mcp/proxy/%s/tools", aoa.agentOperatorURL, mcpID)
	respCode, result, err = aoa.httpClient.GetNoUnmarshal(ctx, toolsURL, nil, aoa.execFactoryHeaders(ctx))
	if err != nil {
		common.LogSafeError(ctx, "MCP tool listing request failed", err)
		return nil, executionDependencyTransportError("list_mcp_tools", respCode, err)
	}
	if respCode == http.StatusNotFound {
		return nil, nil
	}
	if respCode != http.StatusOK {
		common.LogSafeError(ctx, "MCP tool listing failed",
			fmt.Errorf("MCP tool listing returned HTTP %d", respCode))
		return nil, interfaces.NewDependencyError("execution-factory", "list_mcp_tools",
			executionDependencyKindForStatus(respCode), respCode)
	}

	var payload struct {
		Tools *[]struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err = json.Unmarshal(result, &payload); err != nil || payload.Tools == nil {
		invalidErr := interfaces.NewDependencyError("execution-factory", "list_mcp_tools",
			interfaces.DependencyInvalidResponse, respCode)
		if err != nil {
			common.LogSafeError(ctx, "Unmarshal MCP tool listing failed", err)
		} else {
			common.LogSafeError(ctx, "MCP tool listing response was invalid", invalidErr)
		}
		return nil, invalidErr
	}

	tools := make([]*interfaces.MCPToolBrief, 0, len(*payload.Tools))
	for _, tool := range *payload.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			invalidErr := interfaces.NewDependencyError("execution-factory", "list_mcp_tools",
				interfaces.DependencyInvalidResponse, respCode)
			common.LogSafeError(ctx, "MCP tool listing entry was invalid", invalidErr)
			return nil, invalidErr
		}
		tools = append(tools, &interfaces.MCPToolBrief{
			MCPID:       mcpID,
			MCPName:     detail.BaseInfo.Name,
			MCPStatus:   detail.BaseInfo.Status,
			Name:        tool.Name,
			Description: tool.Description,
		})
	}
	return tools, nil
}

func executionDependencyTransportError(operation string, status int, err error) error {
	kind := interfaces.DependencyTransportKind(err)
	return interfaces.NewDependencyError("execution-factory", operation, kind, status)
}

func executionDependencyKindForStatus(status int) interfaces.DependencyErrorKind {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return interfaces.DependencyForbidden
	case http.StatusNotFound:
		return interfaces.DependencyNotFound
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return interfaces.DependencyInvalidBinding
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return interfaces.DependencyTimeout
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable:
		return interfaces.DependencyUnavailable
	default:
		return interfaces.DependencyDownstreamError
	}
}

// FindMCPServersByName resolves an MCP Server name to the ids that carry it exactly.
//
// The listing filters by name server-side, but that filter is a fuzzy one, so the exact match is
// applied here: an import that accepted a prefix would bind a different server than the model
// named.
func (aoa *agentOperatorAccess) FindMCPServersByName(ctx context.Context, name string) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "FindMCPServersByName")
	defer span.End()

	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/mcp/list?name=%s&page_size=%d",
		aoa.agentOperatorURL, neturl.QueryEscape(name), execFactoryNameLookupPageSize)
	respCode, result, err := aoa.httpClient.GetNoUnmarshal(ctx, url, nil, aoa.execFactoryHeaders(ctx))
	if err != nil {
		common.LogSafeError(ctx, "MCP server name lookup request failed", err)
		return nil, fmt.Errorf("mcp server name lookup failed: %w", err)
	}
	if respCode != http.StatusOK {
		common.LogSafeError(ctx, "MCP server name lookup failed",
			fmt.Errorf("mcp server name lookup returned HTTP %d", respCode))
		return nil, fmt.Errorf("mcp server name lookup returned HTTP %d", respCode)
	}

	var payload struct {
		Data []struct {
			MCPID string `json:"mcp_id"`
			Name  string `json:"name"`
		} `json:"data"`
	}
	if err = json.Unmarshal(result, &payload); err != nil {
		common.LogSafeError(ctx, "Unmarshal MCP server listing failed", err)
		return nil, fmt.Errorf("mcp server name lookup failed: %w", err)
	}

	want := strings.TrimSpace(name)
	ids := make([]string, 0, 1)
	for _, server := range payload.Data {
		if strings.TrimSpace(server.Name) == want && server.MCPID != "" {
			ids = append(ids, server.MCPID)
		}
	}
	return ids, nil
}
