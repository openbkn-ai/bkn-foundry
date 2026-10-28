// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type McpSummary struct {
	McpID       string `json:"mcp_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreateUser  string `json:"create_user"`
	CreateTime  int64  `json:"create_time"`
	UpdateUser  string `json:"update_user"`
	UpdateTime  int64  `json:"update_time"`
	ReleaseUser string `json:"release_user"`
	ReleaseTime int64  `json:"release_time"`
}

type SkillSummary struct {
	SkillID     string `json:"skill_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Version     string `json:"version"`
	CreateUser  string `json:"create_user"`
	CreateTime  int64  `json:"create_time"`
	UpdateUser  string `json:"update_user"`
	UpdateTime  int64  `json:"update_time"`
	ReleaseUser string `json:"release_user"`
	ReleaseTime int64  `json:"release_time"`
}

type mcpListResponse struct {
	pageResult
	Data []McpSummary `json:"data"`
}

type skillListResponse struct {
	pageResult
	Data []SkillSummary `json:"data"`
}

type SkillDetailResponse struct {
	SkillID     string `json:"skill_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Version     string `json:"version"`
	CreateUser  string `json:"create_user"`
	CreateTime  int64  `json:"create_time"`
	UpdateUser  string `json:"update_user"`
	UpdateTime  int64  `json:"update_time"`
	ReleaseUser string `json:"release_user"`
	ReleaseTime int64  `json:"release_time"`
}

type skillHistoryEntry struct {
	SkillID     string `json:"skill_id"`
	Version     string `json:"version"`
	Status      string `json:"status"`
	ReleaseUser string `json:"release_user"`
	ReleaseTime int64  `json:"release_time"`
}

type DebugToolRequest struct {
	Body    map[string]interface{} `json:"body,omitempty"`
	Query   map[string]interface{} `json:"query,omitempty"`
	Path    map[string]interface{} `json:"path,omitempty"`
	Header  map[string]interface{} `json:"header,omitempty"`
	Timeout int                    `json:"timeout,omitempty"`
}

type debugToolResponse struct {
	StatusCode int             `json:"status_code"`
	Body       json.RawMessage `json:"body"`
	DurationMs int64           `json:"duration_ms"`
	Error      string          `json:"error"`
	Headers    json.RawMessage `json:"headers"`
}

type debugMcpResponse struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

func (c *OperatorIntegrationClient) ListMcps(ctx context.Context, keyword string, page, pageSize int) (*mcpListResponse, error) {
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	if keyword != "" {
		query.Set("name", keyword)
	}

	path := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/list?%s", query.Encode())
	var resp mcpListResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) ListSkills(ctx context.Context, keyword string, page, pageSize int) (*skillListResponse, error) {
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	if keyword != "" {
		query.Set("name", keyword)
	}

	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills?%s", query.Encode())
	var resp skillListResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) GetSkill(ctx context.Context, skillID string) (*SkillDetailResponse, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s", url.PathEscape(skillID))
	var resp SkillDetailResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) GetSkillHistory(ctx context.Context, skillID string) ([]skillHistoryEntry, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/history", url.PathEscape(skillID))
	var resp []skillHistoryEntry
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *OperatorIntegrationClient) RepublishSkillHistory(ctx context.Context, skillID, version string) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/history/republish", url.PathEscape(skillID))
	return c.doJSON(ctx, http.MethodPost, path, map[string]string{"version": version}, nil)
}

func (c *OperatorIntegrationClient) PublishSkillHistory(ctx context.Context, skillID, version string) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/history/publish", url.PathEscape(skillID))
	return c.doJSON(ctx, http.MethodPost, path, map[string]string{"version": version}, nil)
}

func (c *OperatorIntegrationClient) UpdateSkillStatus(ctx context.Context, skillID, status string) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/status", url.PathEscape(skillID))
	return c.doJSON(ctx, http.MethodPut, path, map[string]string{"status": status}, nil)
}

func (c *OperatorIntegrationClient) DebugTool(
	ctx context.Context,
	boxID, toolID string,
	req DebugToolRequest,
) (*debugToolResponse, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/tool-box/%s/tool/%s/debug",
		url.PathEscape(boxID), url.PathEscape(toolID))
	var resp debugToolResponse
	if err := c.doJSON(ctx, http.MethodPost, path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) DebugMcpTool(
	ctx context.Context,
	mcpID, toolName string,
	args map[string]interface{},
) (*debugMcpResponse, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/%s/tool/%s/debug",
		url.PathEscape(mcpID), url.PathEscape(toolName))
	var resp debugMcpResponse
	if err := c.doJSON(ctx, http.MethodPost, path, args, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) UpdateToolboxStatus(ctx context.Context, boxID, status string) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/tool-box/%s/status", url.PathEscape(boxID))
	return c.doJSON(ctx, http.MethodPost, path, map[string]string{"status": status}, nil)
}
