// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/http"

	"github.com/bytedance/sonic"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

// searchCapabilitiesURI is the unified retrieval surface: one index holding Skills, Function tools
// and MCP tools, ranked together. Internal face only — the whitelist in the body carries the
// caller's authorization decision, so a public caller supplying its own would be deciding its own
// scope.
const searchCapabilitiesURI = "/internal-v1/capabilities/search"

// SearchCapabilities ranks the whitelisted capabilities against a query.
func (o *operatorIntegrationClient) SearchCapabilities(ctx context.Context,
	req *interfaces.SearchCapabilitiesRequest) ([]interfaces.CapabilityHit, error) {
	if req == nil || len(req.Refs) == 0 {
		return []interfaces.CapabilityHit{}, nil
	}

	fullURL := o.baseURL + searchCapabilitiesURI
	header := o.skillHeader(ctx, "operator.capability.search")
	header["Content-Type"] = "application/json"

	payload := map[string]any{
		"query": req.Query,
		"refs":  req.Refs,
	}
	if req.TopK > 0 {
		payload["top_k"] = req.TopK
	}
	if len(req.Types) > 0 {
		payload["types"] = req.Types
	}
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#SearchCapabilities] URL: %s, whitelist=%d, types=%v",
		fullURL, len(req.Refs), req.Types)

	code, respBody, err := o.httpClient.Post(ctx, fullURL, header, payload)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#SearchCapabilities] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "CapabilitySearchRequestFailed", err)
	}

	var raw struct {
		Entries []interfaces.CapabilityHit `json:"entries"`
	}
	if err = sonic.Unmarshal(utils.ObjectToByte(respBody), &raw); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#SearchCapabilities] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "CapabilitySearchResponseInvalid"))
	}
	if raw.Entries == nil {
		return []interfaces.CapabilityHit{}, nil
	}
	return raw.Entries, nil
}
