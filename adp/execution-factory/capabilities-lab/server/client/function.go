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
	"net/http"
	"net/url"
)

type ExecuteFunctionRequest struct {
	Code     string                 `json:"code"`
	Event    map[string]interface{} `json:"event,omitempty"`
	Language string                 `json:"language,omitempty"`
	Timeout  int                    `json:"timeout,omitempty"`
}

type ExecuteFunctionResponse struct {
	Result     json.RawMessage `json:"result"`
	Data       json.RawMessage `json:"data"`
	Stdout     string          `json:"stdout"`
	Stderr     string          `json:"stderr"`
	Error      string          `json:"error"`
	DurationMs int64           `json:"duration_ms"`
}

func (c *OperatorIntegrationClient) ExecuteFunction(
	ctx context.Context,
	businessDomain, userID string,
	req ExecuteFunctionRequest,
) (*ExecuteFunctionResponse, error) {
	if req.Language == "" {
		req.Language = "python"
	}

	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.BaseURL+"/api/agent-operator-integration/v1/function/execute",
		bytes.NewReader(raw),
	)
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-business-domain", businessDomain)
	if userID != "" {
		httpReq.Header.Set("user_id", userID)
	}

	res, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("function execute failed (%d): %s", res.StatusCode, string(payload))
	}

	var resp ExecuteFunctionResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *OperatorIntegrationClient) GetPythonTemplate(ctx context.Context, businessDomain string) (string, error) {
	var resp struct {
		CodeTemplate string `json:"code_template"`
	}
	if err := c.doJSON(
		ctx,
		http.MethodGet,
		"/api/agent-operator-integration/v1/template/python",
		businessDomain,
		nil,
		&resp,
	); err != nil {
		return "", err
	}

	if resp.CodeTemplate == "" {
		return "", fmt.Errorf("python template unavailable")
	}

	return resp.CodeTemplate, nil
}

type FunctionToolPayload struct {
	Name        string
	Description string
	Code        string
	ScriptType  string
	Inputs      []map[string]interface{}
	Outputs     []map[string]interface{}
}

func (c *OperatorIntegrationClient) CreateFunctionTool(
	ctx context.Context,
	businessDomain, boxID string,
	payload FunctionToolPayload,
) (*createToolResponse, error) {
	scriptType := payload.ScriptType
	if scriptType == "" {
		scriptType = "python"
	}

	body := map[string]interface{}{
		"metadata_type": "function",
		"function_input": map[string]interface{}{
			"name":        payload.Name,
			"description": payload.Description,
			"code":        payload.Code,
			"script_type": scriptType,
			"inputs":      payload.Inputs,
			"outputs":     payload.Outputs,
		},
	}

	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/tool-box/%s/tool",
		url.PathEscape(boxID),
	)

	var resp createToolResponse
	if err := c.doJSON(ctx, http.MethodPost, path, businessDomain, body, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func CreateFunctionToolboxPayload(name, description, serviceURL, category string) createToolboxRequest {
	return createToolboxRequest{
		BoxName:      name,
		BoxDesc:      description,
		BoxSvcURL:    serviceURL,
		Category:     category,
		MetadataType: "function",
	}
}
