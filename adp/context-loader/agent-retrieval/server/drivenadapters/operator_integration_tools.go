// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

// The catalogue and execution always run as the caller. A bearer token selects
// Execution Factory's public authorization face; trusted account headers select
// its caller-scoped internal face. Both paths apply the caller's own visibility
// and execute permissions, without substituting this service's identity.
const (
	listPublishedToolboxesURI         = "/v1/tool-box/list"
	listPublishedToolboxesInternalURI = "/internal-v1/caller/tool-box/list"
	listPublishedToolsURI             = "/v1/tool-box/%s/tools/list"
	listPublishedToolsInternalURI     = "/internal-v1/caller/tool-box/%s/tools/list"
	executePublishedToolURI           = "/v1/tool-box/%s/proxy/%s"
	executePublishedToolInternalURI   = "/internal-v1/caller/tool-box/%s/proxy/%s"

	// The search layer caps what a model is shown, but the walk underneath has
	// to be complete: execute_tool checks its tool against this catalogue, so a
	// tool that falls off the end would be refused as if it were disabled.
	publishedCataloguePageSize = 100
	// A stop for a catalogue that never shortens — a downstream that ignores
	// paging would otherwise loop forever on identical pages.
	publishedCatalogueMaxPages = 50
)

// ListPublishedToolboxes returns the published Function toolboxes the caller can see.
func (o *operatorIntegrationClient) ListPublishedToolboxes(
	ctx context.Context, req *interfaces.ListPublishedToolboxesRequest,
) (*interfaces.ListPublishedToolboxesResponse, error) {
	header, err := o.capabilityAuthorizationHeader(ctx, "operator.published_toolbox.list")
	if err != nil {
		return nil, err
	}
	fullURL := o.baseURL + capabilityURI(ctx, listPublishedToolboxesURI, listPublishedToolboxesInternalURI)

	resp := &interfaces.ListPublishedToolboxesResponse{Toolboxes: []interfaces.PublishedToolboxSummary{}}
	for page := 1; page <= publishedCatalogueMaxPages; page++ {
		query := url.Values{
			"status":        {"published"},
			"metadata_type": {"function"},
			"page":          {strconv.Itoa(page)},
			"page_size":     {strconv.Itoa(publishedCataloguePageSize)},
		}
		if req != nil && strings.TrimSpace(req.Keyword) != "" {
			query.Set("name", strings.TrimSpace(req.Keyword))
		}
		o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ListPublishedToolboxes] URL: %s?%s", fullURL, query.Encode())

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
		// A short page is the last one. Filtered-out rows still count towards
		// the page: the page length, not the kept length, says whether to stop.
		if len(payload.Data) < publishedCataloguePageSize {
			break
		}
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
	header, err := o.capabilityAuthorizationHeader(ctx, "operator.published_tool.list")
	if err != nil {
		return nil, err
	}
	fullURL := o.baseURL + fmt.Sprintf(
		capabilityURI(ctx, listPublishedToolsURI, listPublishedToolsInternalURI),
		url.PathEscape(strings.TrimSpace(req.ToolboxID)))

	resp := &interfaces.ListPublishedToolsResponse{
		ToolboxID: req.ToolboxID,
		Tools:     []interfaces.PublishedToolSummary{},
	}
	for page := 1; page <= publishedCatalogueMaxPages; page++ {
		query := url.Values{
			"status":    {"enabled"},
			"page":      {strconv.Itoa(page)},
			"page_size": {strconv.Itoa(publishedCataloguePageSize)},
		}
		o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ListPublishedTools] URL: %s?%s", fullURL, query.Encode())

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
		if len(payload.Tools) < publishedCataloguePageSize {
			break
		}
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

// ExecutePublishedTool invokes one enabled Function tool through the caller-
// scoped Toolbox proxy: the tool runs as the principal that owns the
// Interaction, not as this service.
func (o *operatorIntegrationClient) ExecutePublishedTool(
	ctx context.Context, req *interfaces.ExecutePublishedToolRequest,
) (map[string]any, error) {
	if req == nil || strings.TrimSpace(req.ToolboxID) == "" || strings.TrimSpace(req.ToolID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ToolboxIDAndToolIDRequired"))
	}
	header, err := o.capabilityAuthorizationHeader(ctx, "operator.published_tool.execute")
	if err != nil {
		return nil, err
	}

	fullURL := o.baseURL + fmt.Sprintf(capabilityURI(ctx, executePublishedToolURI, executePublishedToolInternalURI),
		url.PathEscape(strings.TrimSpace(req.ToolboxID)), url.PathEscape(strings.TrimSpace(req.ToolID)))
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ExecutePublishedTool] URL: %s", fullURL)

	// skillHeader carries the managed Interaction (bkn-conversation-id /
	// bkn-interaction-id) that the lifecycle guard put on the context, which is
	// what lets the Function read BKN inside the same Interaction.
	// The transport operation is derived, but Function reads need the Guard's
	// persisted operation as their parent. Keep those identities separate.
	if traceContext, ok := common.GetTraceContextFromCtx(ctx); ok && traceContext.OperationID != "" {
		header[common.HeaderBKNParentOperationID] = traceContext.OperationID
	}
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

// searchBoundToolsURI ranks a whitelist of tools. It is on internal-v1, unlike the catalogue
// calls above: there is no per-caller listing to authorize here, because the whitelist decides
// the scope and it comes from what the knowledge network bound.
const searchBoundToolsURI = "/internal-v1/tool-box/tools/search"

// SearchBoundTools ranks the Function tools named by a whitelist.
//
// Sending no references returns nothing rather than the whole catalogue, so a binding list that
// could not be read cannot widen into every tool on the platform.
func (o *operatorIntegrationClient) SearchBoundTools(ctx context.Context,
	req *interfaces.SearchBoundToolsRequest) ([]interfaces.ToolHit, error) {
	if req == nil || len(req.ToolRefs) == 0 {
		return []interfaces.ToolHit{}, nil
	}

	fullURL := o.baseURL + searchBoundToolsURI
	header := o.skillHeader(ctx, "operator.tool.search")
	header["Content-Type"] = "application/json"

	payload := map[string]any{
		"query":     req.Query,
		"tool_refs": req.ToolRefs,
	}
	if req.TopK > 0 {
		payload["top_k"] = req.TopK
	}
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#SearchBoundTools] URL: %s, whitelist=%d",
		fullURL, len(req.ToolRefs))

	code, respBody, err := o.httpClient.Post(ctx, fullURL, header, payload)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#SearchBoundTools] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "ToolSearchRequestFailed", err)
	}

	var raw struct {
		Entries []interfaces.ToolHit `json:"entries"`
	}
	if err = sonic.Unmarshal(utils.ObjectToByte(respBody), &raw); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#SearchBoundTools] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "ToolSearchResponseInvalid"))
	}
	if raw.Entries == nil {
		return []interfaces.ToolHit{}, nil
	}
	return raw.Entries, nil
}
