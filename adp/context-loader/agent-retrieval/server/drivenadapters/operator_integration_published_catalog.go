// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

const (
	listPublishedToolboxesURI = "/v1/tool-box/list"
	listPublishedToolsURI     = "/v1/tool-box/%s/tools/list"
)

// ListPublishedToolboxes calls the standard public Toolbox catalogue with the
// original MCP credential. Filtering on the server prevents a Context Loader
// service identity from disclosing another caller's unpublished inventory.
func (o *operatorIntegrationClient) ListPublishedToolboxes(ctx context.Context, req *interfaces.ListPublishedToolboxesRequest) (*interfaces.ListPublishedToolboxesResponse, error) {
	token, ok := common.GetRawTokenFromCtx(ctx)
	if !ok {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusUnauthorized, ErrCallerTokenMissing.Error())
	}
	query := url.Values{"status": {"published"}, "metadata_type": {"function"}, "page": {"1"}, "page_size": {"100"}}
	if req != nil && strings.TrimSpace(req.Keyword) != "" {
		query.Set("name", strings.TrimSpace(req.Keyword))
	}
	header := o.skillHeader(ctx, "operator.published_toolbox.list")
	header["Authorization"] = "Bearer " + token
	_, body, err := o.httpClient.Get(ctx, o.baseURL+listPublishedToolboxesURI, query, header)
	if err != nil {
		return nil, skillUpstreamError(ctx, http.StatusBadGateway, "PublishedToolboxCatalogRequestFailed", err)
	}
	var payload struct {
		Data []struct {
			BoxID   string `json:"box_id"`
			BoxName string `json:"box_name"`
			BoxDesc string `json:"box_desc"`
			Status  string `json:"status"`
		} `json:"data"`
	}
	if err := sonic.Unmarshal(utils.ObjectToByte(body), &payload); err != nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, "published toolbox catalog response is invalid")
	}
	resp := &interfaces.ListPublishedToolboxesResponse{}
	for _, box := range payload.Data {
		if box.Status != "published" || strings.TrimSpace(box.BoxID) == "" {
			continue
		}
		resp.Toolboxes = append(resp.Toolboxes, interfaces.PublishedToolboxSummary{ToolboxID: box.BoxID, Name: box.BoxName, Description: box.BoxDesc})
	}
	return resp, nil
}

// ListPublishedTools returns a deliberately narrow Function catalogue. The
// public API's Metadata also contains service topology; only api_spec is
// retained, so an Agent learns business parameters without learning internals.
func (o *operatorIntegrationClient) ListPublishedTools(ctx context.Context, req *interfaces.ListPublishedToolsRequest) (*interfaces.ListPublishedToolsResponse, error) {
	if req == nil || strings.TrimSpace(req.ToolboxID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest, "toolbox_id is required")
	}
	token, ok := common.GetRawTokenFromCtx(ctx)
	if !ok {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusUnauthorized, ErrCallerTokenMissing.Error())
	}
	query := url.Values{"status": {"enabled"}, "page": {"1"}, "page_size": {"100"}}
	header := o.skillHeader(ctx, "operator.published_tool.list")
	header["Authorization"] = "Bearer " + token
	_, body, err := o.httpClient.Get(ctx, o.baseURL+fmt.Sprintf(listPublishedToolsURI, req.ToolboxID), query, header)
	if err != nil {
		return nil, skillUpstreamError(ctx, http.StatusBadGateway, "PublishedToolCatalogRequestFailed", err)
	}
	var payload struct {
		BoxID string `json:"box_id"`
		Tools []struct {
			ToolID      string         `json:"tool_id"`
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Status      string         `json:"status"`
			UseRule     string         `json:"use_rule"`
			Metadata    map[string]any `json:"metadata"`
		} `json:"tools"`
	}
	if err := sonic.Unmarshal(utils.ObjectToByte(body), &payload); err != nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, "published tool catalog response is invalid")
	}
	resp := &interfaces.ListPublishedToolsResponse{ToolboxID: req.ToolboxID}
	for _, tool := range payload.Tools {
		if tool.Status != "enabled" || strings.TrimSpace(tool.ToolID) == "" {
			continue
		}
		inputSchema, _ := tool.Metadata["api_spec"].(map[string]any)
		inputSchema = safeInputSchema(inputSchema)
		resp.Tools = append(resp.Tools, interfaces.PublishedToolSummary{ToolID: tool.ToolID, Name: tool.Name, Description: tool.Description, UseRule: tool.UseRule, InputSchema: inputSchema})
	}
	return resp, nil
}

// safeInputSchema keeps just a Function's business-input contract. Transport
// topology and security metadata are neither callable business parameters nor
// useful to an Agent which is already authenticated through Context Loader.
func safeInputSchema(apiSpec map[string]any) map[string]any {
	if apiSpec == nil {
		return nil
	}
	input := make(map[string]any, 3)
	for _, key := range []string{"parameters", "request_body"} {
		if value, ok := apiSpec[key]; ok {
			input[key] = value
		}
	}
	if components, ok := apiSpec["components"].(map[string]any); ok {
		if schemas, ok := components["schemas"]; ok {
			input["components"] = map[string]any{"schemas": schemas}
		}
	}
	return input
}
