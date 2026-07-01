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

type operatorHistoryEntry struct {
	OperatorID  string `json:"operator_id"`
	Version     string `json:"version"`
	Status      string `json:"status"`
	ReleaseUser string `json:"release_user"`
	ReleaseTime int64  `json:"release_time"`
	UpdateTime  int64  `json:"update_time"`
}

type operatorListResponse struct {
	pageResult
	Data []struct {
		OperatorID string `json:"operator_id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
	} `json:"data"`
}

type OperatorDetailResponse struct {
	OperatorID  string `json:"operator_id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Status      string `json:"status"`
	CreateUser  string `json:"create_user"`
	CreateTime  int64  `json:"create_time"`
	UpdateUser  string `json:"update_user"`
	UpdateTime  int64  `json:"update_time"`
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

func (c *OperatorIntegrationClient) ListMcps(ctx context.Context, businessDomain, keyword string, page, pageSize int) (*mcpListResponse, error) {
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	if keyword != "" {
		query.Set("name", keyword)
	}

	path := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/list?%s", query.Encode())
	var resp mcpListResponse
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) ListSkills(ctx context.Context, businessDomain, keyword string, page, pageSize int) (*skillListResponse, error) {
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	if keyword != "" {
		query.Set("name", keyword)
	}

	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills?%s", query.Encode())
	var resp skillListResponse
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) GetSkill(ctx context.Context, businessDomain, skillID string) (*SkillDetailResponse, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s", url.PathEscape(skillID))
	var resp SkillDetailResponse
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) GetSkillHistory(ctx context.Context, businessDomain, skillID string) ([]skillHistoryEntry, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/history", url.PathEscape(skillID))
	var resp []skillHistoryEntry
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *OperatorIntegrationClient) RepublishSkillHistory(ctx context.Context, businessDomain, skillID, version string) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/history/republish", url.PathEscape(skillID))
	return c.doJSON(ctx, http.MethodPost, path, businessDomain, map[string]string{"version": version}, nil)
}

func (c *OperatorIntegrationClient) PublishSkillHistory(ctx context.Context, businessDomain, skillID, version string) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/history/publish", url.PathEscape(skillID))
	return c.doJSON(ctx, http.MethodPost, path, businessDomain, map[string]string{"version": version}, nil)
}

func (c *OperatorIntegrationClient) UpdateSkillStatus(ctx context.Context, businessDomain, skillID, status string) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/status", url.PathEscape(skillID))
	return c.doJSON(ctx, http.MethodPut, path, businessDomain, map[string]string{"status": status}, nil)
}

func (c *OperatorIntegrationClient) GetOperatorHistory(ctx context.Context, businessDomain, operatorID string) ([]operatorHistoryEntry, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/operator/history/%s", url.PathEscape(operatorID))
	var resp []operatorHistoryEntry
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *OperatorIntegrationClient) ListOperatorsByName(ctx context.Context, businessDomain, name string) (*operatorListResponse, error) {
	query := url.Values{}
	query.Set("page", "1")
	query.Set("page_size", "20")
	if name != "" {
		query.Set("name", name)
	}

	path := fmt.Sprintf("/api/agent-operator-integration/v1/operator/info/list?%s", query.Encode())
	var resp operatorListResponse
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) GetOperator(ctx context.Context, businessDomain, operatorID string) (*OperatorDetailResponse, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/operator/info/%s", url.PathEscape(operatorID))
	var resp OperatorDetailResponse
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) UpdateOperatorStatus(
	ctx context.Context,
	businessDomain, userID, operatorID, status string,
	version ...string,
) error {
	item := map[string]string{"operator_id": operatorID, "status": status}
	if len(version) > 0 && version[0] != "" {
		item["version"] = version[0]
	}
	body := []map[string]string{item}
	return c.doJSONWithUser(ctx, http.MethodPost, "/api/agent-operator-integration/v1/operator/status", businessDomain, userID, body, nil)
}

func (c *OperatorIntegrationClient) UpdateOperatorConfig(
	ctx context.Context,
	businessDomain, userID, operatorID string,
	name, description, metadataType string,
	data interface{},
	operatorInfo map[string]interface{},
	executeControl map[string]interface{},
) error {
	body := map[string]interface{}{
		"operator_id": operatorID,
	}
	if name != "" {
		body["name"] = name
	}
	if description != "" {
		body["description"] = description
	}
	if metadataType != "" {
		body["metadata_type"] = metadataType
	}
	if data != nil {
		body["data"] = data
	}
	if operatorInfo != nil {
		body["operator_info"] = operatorInfo
	}
	if executeControl != nil {
		body["operator_execute_control"] = executeControl
	}

	return c.doJSONWithUser(
		ctx,
		http.MethodPost,
		"/api/agent-operator-integration/v1/operator/info",
		businessDomain,
		userID,
		body,
		nil,
	)
}

func (c *OperatorIntegrationClient) DebugTool(
	ctx context.Context,
	businessDomain, boxID, toolID string,
	req DebugToolRequest,
) (*debugToolResponse, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/tool-box/%s/tool/%s/debug",
		url.PathEscape(boxID), url.PathEscape(toolID))
	var resp debugToolResponse
	if err := c.doJSON(ctx, http.MethodPost, path, businessDomain, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) DebugMcpTool(
	ctx context.Context,
	businessDomain, mcpID, toolName string,
	args map[string]interface{},
) (*debugMcpResponse, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/%s/tool/%s/debug",
		url.PathEscape(mcpID), url.PathEscape(toolName))
	var resp debugMcpResponse
	if err := c.doJSON(ctx, http.MethodPost, path, businessDomain, args, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) UpdateToolboxStatus(ctx context.Context, businessDomain, boxID, status string) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/tool-box/%s/status", url.PathEscape(boxID))
	return c.doJSON(ctx, http.MethodPost, path, businessDomain, map[string]string{"status": status}, nil)
}

func (c *OperatorIntegrationClient) RegisterOperatorOpenAPI(
	ctx context.Context,
	businessDomain string,
	openapiSpec string,
	operatorInfo map[string]interface{},
	executeControl map[string]interface{},
	directPublish bool,
) ([]string, error) {
	if operatorInfo == nil {
		operatorInfo = map[string]interface{}{}
	}
	if operatorInfo["category"] == nil || operatorInfo["category"] == "" {
		operatorInfo["category"] = "other_category"
	}
	if operatorInfo["operator_type"] == nil || operatorInfo["operator_type"] == "" {
		operatorInfo["operator_type"] = "basic"
	}
	if operatorInfo["execution_mode"] == nil || operatorInfo["execution_mode"] == "" {
		operatorInfo["execution_mode"] = "sync"
	}
	if operatorInfo["source"] == nil || operatorInfo["source"] == "" {
		operatorInfo["source"] = "custom"
	}

	body := map[string]interface{}{
		"operator_metadata_type": "openapi",
		"data":                   openapiSpec,
		"direct_publish":         directPublish,
		"operator_info":          operatorInfo,
	}
	if executeControl != nil {
		body["operator_execute_control"] = executeControl
	}

	var resp []struct {
		OperatorID string `json:"operator_id"`
		Status     string `json:"status"`
	}
	if err := c.doJSON(ctx, http.MethodPost,
		"/api/agent-operator-integration/v1/operator/register", businessDomain, body, &resp); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(resp))
	for _, item := range resp {
		if item.OperatorID != "" {
			ids = append(ids, item.OperatorID)
		}
	}
	return ids, nil
}
