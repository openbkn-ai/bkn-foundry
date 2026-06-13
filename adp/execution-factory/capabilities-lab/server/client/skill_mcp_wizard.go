package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

type McpParseSseRequest struct {
	URL     string            `json:"url"`
	Mode    string            `json:"mode"`
	Headers map[string]string `json:"headers,omitempty"`
}

type McpParseSseTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type McpParseSseResponse struct {
	Tools []McpParseSseTool `json:"tools"`
}

type SkillFileSummary struct {
	RelPath  string `json:"rel_path"`
	FileType string `json:"file_type,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

type SkillManagementContentResponse struct {
	Content  string             `json:"content,omitempty"`
	FileType string             `json:"file_type,omitempty"`
	Files    []SkillFileSummary `json:"files,omitempty"`
	URL      string             `json:"url,omitempty"`
}

type ReadSkillFileRequest struct {
	RelPath string `json:"rel_path"`
}

type ReadSkillFileResponse struct {
	SkillID  string `json:"skill_id"`
	RelPath  string `json:"rel_path"`
	URL      string `json:"url,omitempty"`
	Content  string `json:"content,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileType string `json:"file_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

func (c *OperatorIntegrationClient) ParseMcpSse(
	ctx context.Context,
	businessDomain string,
	req McpParseSseRequest,
) (*McpParseSseResponse, error) {
	mode := req.Mode
	if mode == "" {
		mode = "sse"
	}

	body := map[string]interface{}{
		"url":     req.URL,
		"mode":    mode,
		"headers": req.Headers,
	}

	var resp McpParseSseResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/agent-operator-integration/v1/mcp/parse/sse", businessDomain, body, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *OperatorIntegrationClient) GetSkillManagementContent(
	ctx context.Context,
	businessDomain, skillID string,
) (*SkillManagementContentResponse, error) {
	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/skills/%s/management/content?response_mode=content",
		url.PathEscape(skillID),
	)

	var raw struct {
		Content  string `json:"content"`
		FileType string `json:"file_type"`
		URL      string `json:"url"`
		Files    []struct {
			RelPath  string `json:"rel_path"`
			FileType string `json:"file_type"`
			MimeType string `json:"mime_type"`
			Size     int64  `json:"size"`
		} `json:"files"`
	}

	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &raw); err != nil {
		return nil, err
	}

	files := make([]SkillFileSummary, 0, len(raw.Files))
	for _, file := range raw.Files {
		if file.RelPath == "" {
			continue
		}
		files = append(files, SkillFileSummary{
			RelPath:  file.RelPath,
			FileType: file.FileType,
			MimeType: file.MimeType,
			Size:     file.Size,
		})
	}

	return &SkillManagementContentResponse{
		Content:  raw.Content,
		FileType: raw.FileType,
		Files:    files,
		URL:      raw.URL,
	}, nil
}

func (c *OperatorIntegrationClient) ReadSkillManagementFile(
	ctx context.Context,
	businessDomain, skillID, relPath, responseMode string,
) (*ReadSkillFileResponse, error) {
	if responseMode == "" {
		responseMode = "content"
	}

	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/skills/%s/management/files/read?response_mode=%s",
		url.PathEscape(skillID),
		url.QueryEscape(responseMode),
	)

	var resp ReadSkillFileResponse
	if err := c.doJSON(ctx, http.MethodPost, path, businessDomain, ReadSkillFileRequest{RelPath: relPath}, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}
