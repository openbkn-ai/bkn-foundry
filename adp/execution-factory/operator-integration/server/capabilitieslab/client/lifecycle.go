// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
)

type UpdateToolPayload struct {
	Name                string
	Description         string
	FallbackName        string
	FallbackDescription string
	OpenAPISpec         string
	FunctionInput       map[string]interface{}
}

type RegisterMcpPayload struct {
	Name         string
	Description  string
	Mode         string
	URL          string
	Headers      map[string]string
	Category     string
	CreationType string
}

type RegisterSkillPayload struct {
	FileType string
	Category string
	Source   string
	Filename string
	Content  []byte
	MimeType string
}

type registerSkillResponse struct {
	SkillID string `json:"skill_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (c *OperatorIntegrationClient) UpdateTool(
	ctx context.Context,
	businessDomain, boxID, toolID string,
	payload UpdateToolPayload,
) error {
	body := map[string]interface{}{
		"metadata_type": "openapi",
	}
	if payload.FunctionInput != nil {
		body["metadata_type"] = "function"
		body["function_input"] = payload.FunctionInput
	}
	name := payload.Name
	if name == "" {
		name = payload.FallbackName
	}
	desc := payload.Description
	if desc == "" {
		desc = payload.FallbackDescription
	}
	if name == "" || desc == "" {
		return fmt.Errorf("tool name and description are required for update")
	}
	body["name"] = name
	body["description"] = desc
	if payload.OpenAPISpec != "" {
		if !json.Valid([]byte(payload.OpenAPISpec)) {
			return fmt.Errorf("invalid openapi json")
		}
		body["data"] = json.RawMessage(payload.OpenAPISpec)
	}

	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/tool-box/%s/tool/%s",
		url.PathEscape(boxID),
		url.PathEscape(toolID),
	)
	return c.doJSON(ctx, http.MethodPost, path, businessDomain, body, nil)
}

func (c *OperatorIntegrationClient) DeleteTools(
	ctx context.Context,
	businessDomain, boxID string,
	toolIDs []string,
) error {
	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/tool-box/%s/tools/batch-delete",
		url.PathEscape(boxID),
	)
	return c.doJSON(ctx, http.MethodPost, path, businessDomain, map[string]interface{}{
		"tool_ids": toolIDs,
	}, nil)
}

func (c *OperatorIntegrationClient) DeleteMcp(ctx context.Context, businessDomain, mcpID string) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/%s", url.PathEscape(mcpID))
	return c.doJSON(ctx, http.MethodDelete, path, businessDomain, nil, nil)
}

func (c *OperatorIntegrationClient) DeleteSkill(ctx context.Context, businessDomain, skillID string) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s", url.PathEscape(skillID))
	return c.doJSON(ctx, http.MethodDelete, path, businessDomain, nil, nil)
}

func (c *OperatorIntegrationClient) UpdateMcpStatus(
	ctx context.Context,
	businessDomain, mcpID, status string,
) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/%s/status", url.PathEscape(mcpID))
	return c.doJSON(ctx, http.MethodPost, path, businessDomain, map[string]string{"status": status}, nil)
}

func (c *OperatorIntegrationClient) RegisterMcp(
	ctx context.Context,
	businessDomain string,
	payload RegisterMcpPayload,
) (string, error) {
	mode := payload.Mode
	if mode == "" {
		mode = "sse"
	}
	category := payload.Category
	if category == "" {
		category = "other_category"
	}
	creationType := payload.CreationType
	if creationType == "" {
		creationType = "custom"
	}

	body := map[string]interface{}{
		"name":          payload.Name,
		"description":   payload.Description,
		"mode":          mode,
		"url":           payload.URL,
		"headers":       payload.Headers,
		"category":      category,
		"creation_type": creationType,
		"source":        "custom",
	}

	var resp struct {
		McpID interface{} `json:"mcp_id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/agent-operator-integration/v1/mcp", businessDomain, body, &resp); err != nil {
		return "", err
	}
	mcpID := fmt.Sprint(resp.McpID)
	if mcpID == "" || mcpID == "<nil>" {
		return "", fmt.Errorf("mcp registration failed")
	}
	return mcpID, nil
}

func (c *OperatorIntegrationClient) RegisterSkill(
	ctx context.Context,
	businessDomain string,
	payload RegisterSkillPayload,
) (*registerSkillResponse, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	fileType := payload.FileType
	if fileType == "" {
		fileType = "zip"
	}
	_ = writer.WriteField("file_type", fileType)

	category := payload.Category
	if category == "" {
		category = "other_category"
	}
	_ = writer.WriteField("category", category)

	source := payload.Source
	if source == "" {
		source = "custom"
	}
	_ = writer.WriteField("source", source)

	filename := payload.Filename
	if filename == "" {
		filename = "skill.zip"
	}
	mimeType := payload.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(payload.Content); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.BaseURL+"/api/agent-operator-integration/v1/skills",
		&body,
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-business-domain", businessDomain)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	payloadBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("operator-integration POST /skills failed (%d): %s", res.StatusCode, string(payloadBytes))
	}

	var resp registerSkillResponse
	if err := json.Unmarshal(payloadBytes, &resp); err != nil {
		return nil, fmt.Errorf("decode skill response: %w", err)
	}
	if resp.SkillID == "" {
		return nil, fmt.Errorf("skill registration failed")
	}
	return &resp, nil
}
