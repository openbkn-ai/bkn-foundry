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
	"strings"
	"time"
)

type contextKey string

const (
	contextKeyAuthorization contextKey = "authorization"
	contextKeyCookie        contextKey = "cookie"
)

type OperatorIntegrationClient struct {
	BaseURL string
	HTTP    *http.Client
}

func WithForwardedAuth(ctx context.Context, authorization string, cookie string) context.Context {
	if authorization != "" {
		ctx = context.WithValue(ctx, contextKeyAuthorization, authorization)
	}
	if cookie != "" {
		ctx = context.WithValue(ctx, contextKeyCookie, cookie)
	}
	return ctx
}

func NewOperatorIntegrationClient(baseURL string) *OperatorIntegrationClient {
	return &OperatorIntegrationClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *OperatorIntegrationClient) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health/alive", nil)
	if err != nil {
		return err
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("upstream health status %d", resp.StatusCode)
	}

	return nil
}

type pageResult struct {
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

type ToolboxInfo struct {
	BoxID        string   `json:"box_id"`
	BoxName      string   `json:"box_name"`
	BoxDesc      string   `json:"box_desc"`
	BoxSvcURL    string   `json:"box_svc_url"`
	BoxCategory  string   `json:"box_category"`
	Status       string   `json:"status"`
	MetadataType string   `json:"metadata_type"`
	CreateUser   string   `json:"create_user"`
	CreateTime   int64    `json:"create_time"`
	UpdateUser   string   `json:"update_user"`
	UpdateTime   int64    `json:"update_time"`
	ReleaseUser  string   `json:"release_user"`
	ReleaseTime  int64    `json:"release_time"`
	Tools        []string `json:"tools"`
}

type toolboxListResponse struct {
	pageResult
	Data []ToolboxInfo `json:"data"`
}

type ToolInfo struct {
	ToolID         string `json:"tool_id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Status         string `json:"status"`
	CreateUser     string `json:"create_user"`
	CreateTime     int64  `json:"create_time"`
	UpdateUser     string `json:"update_user"`
	UpdateTime     int64  `json:"update_time"`
	ReleaseUser    string `json:"release_user"`
	ReleaseTime    int64  `json:"release_time"`
	SourceID       string `json:"source_id"`
	SourceType     string `json:"source_type"`
	ResourceObject string `json:"resource_object"`
	Metadata       *struct {
		MetadataType string `json:"metadata_type"`
	} `json:"metadata"`
}

type ToolListResponse struct {
	pageResult
	Tools []ToolInfo `json:"tools"`
}

type ToolMetadataInfo struct {
	Version         string          `json:"version"`
	Summary         string          `json:"summary"`
	Description     string          `json:"description"`
	ServerURL       string          `json:"server_url"`
	Path            string          `json:"path"`
	Method          string          `json:"method"`
	APISpec         json.RawMessage `json:"api_spec"`
	FunctionContent *struct {
		ScriptType      string   `json:"script_type"`
		Code            string   `json:"code"`
		Dependencies    []string `json:"dependencies"`
		DependenciesURL string   `json:"dependencies_url"`
	} `json:"function_content"`
}

type ToolDetail struct {
	ToolID         string            `json:"tool_id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Status         string            `json:"status"`
	CreateUser     string            `json:"create_user"`
	CreateTime     int64             `json:"create_time"`
	UpdateUser     string            `json:"update_user"`
	UpdateTime     int64             `json:"update_time"`
	ReleaseUser    string            `json:"release_user"`
	ReleaseTime    int64             `json:"release_time"`
	MetadataType   string            `json:"metadata_type"`
	SourceID       string            `json:"source_id"`
	SourceType     string            `json:"source_type"`
	ResourceObject string            `json:"resource_object"`
	Metadata       *ToolMetadataInfo `json:"metadata"`
}

type createToolboxRequest struct {
	BoxName      string `json:"box_name"`
	BoxDesc      string `json:"box_desc"`
	BoxSvcURL    string `json:"box_svc_url"`
	Category     string `json:"box_category"`
	MetadataType string `json:"metadata_type"`
}

type createToolboxResponse struct {
	BoxID string `json:"box_id"`
}

type createToolRequest struct {
	MetadataType string          `json:"metadata_type"`
	Data         json.RawMessage `json:"data"`
}

type createToolResponse struct {
	SuccessIDs   []string            `json:"success_ids"`
	FailureCount int64               `json:"failure_count"`
	Failures     []createToolFailure `json:"failures"`
}

type createToolFailure struct {
	ToolName string          `json:"tool_name"`
	Error    string          `json:"error"`
	ErrorMsg json.RawMessage `json:"error_msg"`
}

func (f createToolFailure) Message() string {
	if strings.TrimSpace(f.Error) != "" {
		return f.Error
	}

	if len(f.ErrorMsg) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(f.ErrorMsg, &text); err == nil {
		return strings.TrimSpace(text)
	}

	var detail struct {
		Description string `json:"description"`
		Details     string `json:"details"`
		Message     string `json:"message"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(f.ErrorMsg, &detail); err != nil {
		return ""
	}

	for _, candidate := range []string{
		detail.Description,
		detail.Details,
		detail.Message,
		detail.Error,
	} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}

	return ""
}

type bundleRequest struct {
	BoxID         string                 `json:"box_id,omitempty"`
	BoxName       string                 `json:"box_name,omitempty"`
	BoxDesc       string                 `json:"box_desc,omitempty"`
	BoxSvcURL     string                 `json:"box_svc_url"`
	BoxCategory   string                 `json:"box_category"`
	Data          string                 `json:"data"`
	DirectPublish bool                   `json:"direct_publish"`
	OperatorInfo  map[string]interface{} `json:"operator_info"`
}

type bundleResponse struct {
	BoxID       string   `json:"box_id"`
	ToolIDs     []string `json:"tool_ids"`
	OperatorIDs []string `json:"operator_ids"`
	Links       []struct {
		OperatorID string `json:"operator_id"`
		ToolID     string `json:"tool_id"`
	} `json:"links"`
	FailureCount int64    `json:"failure_count"`
	Failures     []string `json:"failures"`
}

func (c *OperatorIntegrationClient) ListToolboxes(
	ctx context.Context,
	businessDomain, name string,
	page, pageSize int,
	all bool,
) (*toolboxListResponse, error) {
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	if all {
		query.Set("all", "true")
	}
	if name != "" {
		query.Set("name", name)
	}

	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/tool-box/list?%s",
		query.Encode(),
	)

	var resp toolboxListResponse
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *OperatorIntegrationClient) ListTools(
	ctx context.Context,
	businessDomain, boxID string,
) ([]ToolInfo, error) {
	resp, err := c.ListToolsPaged(ctx, businessDomain, boxID, "", 1, 100)
	if err != nil {
		return nil, err
	}

	tools := resp.Tools
	if resp.Total > len(tools) {
		for page := 2; len(tools) < resp.Total; page++ {
			next, pageErr := c.ListToolsPaged(ctx, businessDomain, boxID, "", page, 100)
			if pageErr != nil {
				return nil, pageErr
			}
			if len(next.Tools) == 0 {
				break
			}
			tools = append(tools, next.Tools...)
		}
	}

	return tools, nil
}

func (c *OperatorIntegrationClient) ListToolsPaged(
	ctx context.Context,
	businessDomain, boxID, keyword string,
	page, pageSize int,
) (*ToolListResponse, error) {
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	if keyword != "" {
		query.Set("name", keyword)
	}

	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/tool-box/%s/tools/list?%s",
		url.PathEscape(boxID),
		query.Encode(),
	)

	var resp ToolListResponse
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func CreateToolboxPayload(name, description, serviceURL, category string) createToolboxRequest {
	return createToolboxRequest{
		BoxName:      name,
		BoxDesc:      description,
		BoxSvcURL:    serviceURL,
		Category:     category,
		MetadataType: "openapi",
	}
}

func CreateToolPayload(openapiSpec string) (createToolRequest, error) {
	if !json.Valid([]byte(openapiSpec)) {
		return createToolRequest{}, fmt.Errorf("invalid openapi json")
	}

	return createToolRequest{
		MetadataType: "openapi",
		Data:         json.RawMessage(openapiSpec),
	}, nil
}

func BundleRequestFromModel(
	boxID, boxName string,
	req struct {
		OpenAPISpec string
		ServiceURL  string
		Description string
		Category    string
	},
	category string,
) bundleRequest {
	payload := bundleRequest{
		BoxSvcURL:     req.ServiceURL,
		BoxCategory:   category,
		Data:          req.OpenAPISpec,
		DirectPublish: false,
		OperatorInfo: map[string]interface{}{
			"category":       category,
			"execution_mode": "sync",
			"operator_type":  "basic",
			"source":         "custom",
		},
	}
	if boxID != "" {
		payload.BoxID = boxID
	} else {
		payload.BoxName = boxName
		payload.BoxDesc = req.Description
	}
	return payload
}

func (c *OperatorIntegrationClient) GetTool(
	ctx context.Context,
	businessDomain, boxID, toolID string,
) (*ToolDetail, error) {
	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/tool-box/%s/tool/%s",
		url.PathEscape(boxID),
		url.PathEscape(toolID),
	)

	var resp ToolDetail
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *OperatorIntegrationClient) CreateToolbox(
	ctx context.Context,
	businessDomain string,
	req createToolboxRequest,
) (*createToolboxResponse, error) {
	var resp createToolboxResponse
	if err := c.doJSON(
		ctx,
		http.MethodPost,
		"/api/agent-operator-integration/v1/tool-box",
		businessDomain,
		req,
		&resp,
	); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *OperatorIntegrationClient) CreateTool(
	ctx context.Context,
	businessDomain, boxID string,
	req createToolRequest,
) (*createToolResponse, error) {
	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/tool-box/%s/tool",
		url.PathEscape(boxID),
	)

	var resp createToolResponse
	if err := c.doJSON(ctx, http.MethodPost, path, businessDomain, req, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *OperatorIntegrationClient) RegisterOpenAPIBundle(
	ctx context.Context,
	businessDomain string,
	req bundleRequest,
) (*bundleResponse, error) {
	var resp bundleResponse
	if err := c.doJSON(
		ctx,
		http.MethodPost,
		"/api/agent-operator-integration/v1/capabilities/openapi-bundle",
		businessDomain,
		req,
		&resp,
	); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *OperatorIntegrationClient) doJSON(
	ctx context.Context,
	method, path, businessDomain string,
	body any,
	out any,
) error {
	return c.doJSONWithUser(ctx, method, path, businessDomain, "", body, out)
}

func (c *OperatorIntegrationClient) doJSONWithUser(
	ctx context.Context,
	method, path, businessDomain, userID string,
	body any,
	out any,
) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-business-domain", businessDomain)
	if userID != "" {
		req.Header.Set("user_id", userID)
	}
	if authorization, ok := ctx.Value(contextKeyAuthorization).(string); ok && authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if cookie, ok := ctx.Value(contextKeyCookie).(string); ok && cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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
		return fmt.Errorf("operator-integration %s %s failed (%d): %s", method, path, res.StatusCode, string(payload))
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}
