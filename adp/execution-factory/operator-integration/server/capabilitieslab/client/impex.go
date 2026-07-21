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

func (c *OperatorIntegrationClient) ExportImpex(
	ctx context.Context,
	businessDomain, userID, componentType, id string,
) (json.RawMessage, error) {
	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/impex/export/%s/%s",
		url.PathEscape(componentType),
		url.PathEscape(id),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-business-domain", businessDomain)
	if userID != "" {
		req.Header.Set("user_id", userID)
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("impex export %s failed (%d): %s", componentType, res.StatusCode, string(payload))
	}

	if !json.Valid(payload) {
		return nil, fmt.Errorf("invalid impex export json")
	}

	return json.RawMessage(payload), nil
}

func (c *OperatorIntegrationClient) ImportImpex(
	ctx context.Context,
	businessDomain, userID, componentType, mode string,
	data []byte,
) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("mode", mode); err != nil {
		return err
	}

	part, err := writer.CreateFormFile("data", "import.adp.json")
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/impex/import/%s",
		url.PathEscape(componentType),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, &body)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("x-business-domain", businessDomain)
	if userID != "" {
		req.Header.Set("user_id", userID)
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("impex import %s failed (%d): %s", componentType, res.StatusCode, string(payload))
	}

	return nil
}

func DetectImpexComponentType(data []byte) (string, error) {
	var probe struct {
		Operator *json.RawMessage `json:"operator"`
		Toolbox  *json.RawMessage `json:"toolbox"`
		MCP      *json.RawMessage `json:"mcp"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", fmt.Errorf("invalid impex json")
	}

	switch {
	case probe.Toolbox != nil:
		return "toolbox", nil
	case probe.MCP != nil:
		return "mcp", nil
	case probe.Operator != nil:
		return "operator", nil
	default:
		return "", fmt.Errorf("unsupported impex payload")
	}
}
