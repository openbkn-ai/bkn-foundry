package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
)

type CategoryEntry struct {
	CategoryType string `json:"category_type"`
	Name         string `json:"name"`
}

var filenamePattern = regexp.MustCompile(`filename="?([^";]+)"?`)

func (c *OperatorIntegrationClient) ListCategories(ctx context.Context, businessDomain string) ([]CategoryEntry, error) {
	var resp []CategoryEntry
	if err := c.doJSON(
		ctx,
		http.MethodGet,
		"/api/agent-operator-integration/v1/operator/category",
		businessDomain,
		nil,
		&resp,
	); err != nil {
		return nil, err
	}
	if len(resp) == 0 {
		return []CategoryEntry{{CategoryType: "other_category", Name: "Other"}}, nil
	}
	return resp, nil
}

func (c *OperatorIntegrationClient) UpdateMcp(
	ctx context.Context,
	businessDomain, mcpID string,
	body map[string]interface{},
) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/%s", mcpID)
	return c.doJSON(ctx, http.MethodPut, path, businessDomain, body, nil)
}

func (c *OperatorIntegrationClient) UpdateSkillMetadata(
	ctx context.Context,
	businessDomain, skillID string,
	body map[string]interface{},
) error {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/metadata", skillID)
	return c.doJSON(ctx, http.MethodPut, path, businessDomain, body, nil)
}

func (c *OperatorIntegrationClient) DownloadSkillPackage(
	ctx context.Context,
	businessDomain, skillID string,
) ([]byte, string, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/management/download", skillID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("x-business-domain", businessDomain)

	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download skill failed (%d): %s", res.StatusCode, string(payload))
	}

	filename := "skill.zip"
	if match := filenamePattern.FindStringSubmatch(res.Header.Get("Content-Disposition")); len(match) > 1 {
		filename = strings.TrimSpace(match[1])
	}

	return payload, filename, nil
}

func (c *OperatorIntegrationClient) ListMcpTools(
	ctx context.Context,
	businessDomain, mcpID string,
) ([]map[string]interface{}, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/proxy/%s/tools", mcpID)
	var resp struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		// Fallback: some deployments expose tools under the legacy path.
		legacyPath := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/%s/tools", mcpID)
		var legacy []map[string]interface{}
		if legacyErr := c.doJSON(ctx, http.MethodGet, legacyPath, businessDomain, nil, &legacy); legacyErr != nil {
			return nil, err
		}
		return legacy, nil
	}
	if len(resp.Tools) > 0 {
		return resp.Tools, nil
	}
	return resp.Tools, nil
}

func (c *OperatorIntegrationClient) UpdateSkillPackage(
	ctx context.Context,
	businessDomain, skillID string,
	payload RegisterSkillPayload,
) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	fileType := payload.FileType
	if fileType == "" {
		fileType = "zip"
	}
	_ = writer.WriteField("file_type", fileType)

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
		return err
	}
	if _, err := part.Write(payload.Content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/%s/package", skillID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-business-domain", businessDomain)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("update skill package failed (%d): %s", res.StatusCode, string(responseBody))
	}
	return nil
}
