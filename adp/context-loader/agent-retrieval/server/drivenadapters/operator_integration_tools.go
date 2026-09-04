// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

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

// The catalogue and the execution both go through Execution Factory's public
// API rather than internal-v1. The public routes authenticate by introspecting
// the bearer token, so the caller's own visibility and execute permission
// decide the answer. Calling internal-v1 with this service's identity would
// disclose another caller's unpublished inventory and skip the permission check
// on the way in.
const (
	listPublishedToolboxesURI = "/v1/tool-box/list"
	listPublishedToolsURI     = "/v1/tool-box/%s/tools/list"
	executePublishedToolURI   = "/v1/tool-box/%s/proxy/%s"

	// The catalogue is a discovery surface, not a paging API: one page each is
	// what a model can hold, and the search layer caps what it keeps anyway.
	publishedCataloguePageSize = "100"
)

// ListPublishedToolboxes returns the published Function toolboxes the caller can see.
func (o *operatorIntegrationClient) ListPublishedToolboxes(
	ctx context.Context, req *interfaces.ListPublishedToolboxesRequest,
) (*interfaces.ListPublishedToolboxesResponse, error) {
	token, ok := common.GetRawTokenFromCtx(ctx)
	if !ok {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusUnauthorized, ErrCallerTokenMissing.Error())
	}
	query := url.Values{
		"status":        {"published"},
		"metadata_type": {"function"},
		"page":          {"1"},
		"page_size":     {publishedCataloguePageSize},
	}
	if req != nil && strings.TrimSpace(req.Keyword) != "" {
		query.Set("name", strings.TrimSpace(req.Keyword))
	}

	fullURL := o.baseURL + listPublishedToolboxesURI
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ListPublishedToolboxes] URL: %s?%s", fullURL, query.Encode())

	header := o.skillHeader(ctx, "operator.published_toolbox.list")
	header["Authorization"] = "Bearer " + token
	code, body, err := o.httpClient.Get(ctx, fullURL, query, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ListPublishedToolboxes] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "ToolboxCatalogRequestFailed", err)
	}

	var payload struct {
		Data []struct {
			BoxID   string `json:"box_id"`
			BoxName string `json:"box_name"`
			BoxDesc string `json:"box_desc"`
			Status  string `json:"status"`
		} `json:"data"`
	}
	if err = sonic.Unmarshal(utils.ObjectToByte(body), &payload); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ListPublishedToolboxes] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "ToolboxCatalogResponseInvalid"))
	}

	resp := &interfaces.ListPublishedToolboxesResponse{
		Toolboxes: make([]interfaces.PublishedToolboxSummary, 0, len(payload.Data)),
	}
	for _, box := range payload.Data {
		if box.Status != "published" || strings.TrimSpace(box.BoxID) == "" {
			continue
		}
		resp.Toolboxes = append(resp.Toolboxes, interfaces.PublishedToolboxSummary{
			ToolboxID:   box.BoxID,
			Name:        box.BoxName,
			Description: box.BoxDesc,
		})
	}
	return resp, nil
}

// ListPublishedTools returns the enabled Function tools of one published toolbox.
func (o *operatorIntegrationClient) ListPublishedTools(
	ctx context.Context, req *interfaces.ListPublishedToolsRequest,
) (*interfaces.ListPublishedToolsResponse, error) {
	if req == nil || strings.TrimSpace(req.ToolboxID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ToolboxIDRequired"))
	}
	token, ok := common.GetRawTokenFromCtx(ctx)
	if !ok {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusUnauthorized, ErrCallerTokenMissing.Error())
	}
	query := url.Values{"status": {"enabled"}, "page": {"1"}, "page_size": {publishedCataloguePageSize}}

	fullURL := o.baseURL + fmt.Sprintf(listPublishedToolsURI, url.PathEscape(strings.TrimSpace(req.ToolboxID)))
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ListPublishedTools] URL: %s?%s", fullURL, query.Encode())

	header := o.skillHeader(ctx, "operator.published_tool.list")
	header["Authorization"] = "Bearer " + token
	code, body, err := o.httpClient.Get(ctx, fullURL, query, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ListPublishedTools] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "ToolCatalogRequestFailed", err)
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
	if err = sonic.Unmarshal(utils.ObjectToByte(body), &payload); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ListPublishedTools] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "ToolCatalogResponseInvalid"))
	}

	resp := &interfaces.ListPublishedToolsResponse{
		ToolboxID: req.ToolboxID,
		Tools:     make([]interfaces.PublishedToolSummary, 0, len(payload.Tools)),
	}
	for _, tool := range payload.Tools {
		if tool.Status != "enabled" || strings.TrimSpace(tool.ToolID) == "" {
			continue
		}
		apiSpec, _ := tool.Metadata["api_spec"].(map[string]any)
		resp.Tools = append(resp.Tools, interfaces.PublishedToolSummary{
			ToolID:      tool.ToolID,
			Name:        tool.Name,
			Description: tool.Description,
			UseRule:     tool.UseRule,
			InputSchema: businessInputSchema(apiSpec),
		})
	}
	return resp, nil
}

// businessInputSchema keeps just a Function's business-input contract. The
// public API's metadata also describes service topology, which is neither a
// callable parameter nor useful to a model already authenticated through
// Context Loader — and every field kept here is spent context on every search.
func businessInputSchema(apiSpec map[string]any) map[string]any {
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
	if len(input) == 0 {
		return nil
	}
	return input
}

// ExecutePublishedTool invokes one enabled Function tool through the public
// Toolbox proxy, carrying the caller's own bearer token: the tool runs as the
// principal that owns the Interaction, not as this service.
func (o *operatorIntegrationClient) ExecutePublishedTool(
	ctx context.Context, req *interfaces.ExecutePublishedToolRequest,
) (map[string]any, error) {
	if req == nil || strings.TrimSpace(req.ToolboxID) == "" || strings.TrimSpace(req.ToolID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ToolboxIDAndToolIDRequired"))
	}
	token, ok := common.GetRawTokenFromCtx(ctx)
	if !ok {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusUnauthorized, ErrCallerTokenMissing.Error())
	}

	fullURL := o.baseURL + fmt.Sprintf(executePublishedToolURI,
		url.PathEscape(strings.TrimSpace(req.ToolboxID)), url.PathEscape(strings.TrimSpace(req.ToolID)))
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ExecutePublishedTool] URL: %s", fullURL)

	// skillHeader carries the managed Interaction (bkn-conversation-id /
	// bkn-interaction-id) that the lifecycle guard put on the context, which is
	// what lets the Function read BKN inside the same Interaction.
	header := o.skillHeader(ctx, "operator.published_tool.execute")
	header["Authorization"] = "Bearer " + token

	parameters := req.Parameters
	if parameters == nil {
		parameters = map[string]any{}
	}
	// The Toolbox proxy expects HTTPRequestParams. The Function's business
	// parameters belong inside its body field; treating them as envelope fields
	// would leave the proxied request body empty.
	code, response, err := o.httpClient.Post(ctx, fullURL, header, map[string]any{"body": parameters})
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ExecutePublishedTool] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "ToolExecutionRequestFailed", err)
	}
	result, ok := response.(map[string]any)
	if !ok {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "ToolExecutionResponseInvalid"))
	}
	return result, nil
}
