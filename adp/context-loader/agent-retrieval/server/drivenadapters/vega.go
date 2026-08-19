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
	"sync"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type vegaAccess struct {
	logger     interfaces.Logger
	baseURL    string
	httpClient interfaces.HTTPClient
}

var (
	vegaAccessOnce sync.Once
	vegaAccessInst interfaces.DrivenVega
	vegaJSON       = sonic.Config{UseNumber: true}.Froze()
)

// NewVegaAccess creates a Vega backend access object for read-only queries.
func NewVegaAccess() interfaces.DrivenVega {
	vegaAccessOnce.Do(func() {
		conf := config.NewConfigLoader()
		vegaAccessInst = &vegaAccess{
			logger:     conf.GetLogger(),
			baseURL:    conf.Vega.BuildURL("/api/vega-backend"),
			httpClient: rest.NewHTTPClient(),
		}
	})
	return vegaAccessInst
}

// RawQuery calls Vega's internal raw query API to execute SQL.
func (v *vegaAccess) RawQuery(ctx context.Context, req *interfaces.VegaRawQueryReq) (*interfaces.VegaRawQueryResp, error) {
	src := fmt.Sprintf("%s/in/v1/resources/query", v.baseURL)
	header := common.GetHeaderForChildOperation(ctx, "vega.raw_query", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	respCode, respBody, err := v.httpClient.PostNoUnmarshal(ctx, src, header, req)
	if err != nil {
		v.logger.WithContext(ctx).Errorf("[VegaAccess] RawQuery request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[VegaAccess] RawQuery request failed, err: %v", err))
	}
	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		v.logger.WithContext(ctx).Errorf("[VegaAccess] RawQuery resp failed, code=%d, body=%s", respCode, string(respBody))
		return nil, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("vega raw query failed: %s", string(respBody)))
	}

	resp := &interfaces.VegaRawQueryResp{}
	if len(respBody) == 0 {
		return resp, nil
	}
	if err := vegaJSON.Unmarshal(respBody, resp); err != nil {
		v.logger.WithContext(ctx).Errorf("[VegaAccess] RawQuery unmarshal failed: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("parse vega raw query response failed: %v", err))
	}
	return resp, nil
}

// vegaEntriesWrapper is the unified {"entries":[...]} envelope for Vega get-by-ids APIs.
type vegaResourceLite struct {
	ID        string `json:"id"`
	CatalogID string `json:"catalog_id"`
}

type vegaResourceEnvelope struct {
	Entries []vegaResourceLite `json:"entries"`
}

type vegaCatalogLite struct {
	ID            string `json:"id"`
	ConnectorType string `json:"connector_type"`
}

type vegaCatalogEnvelope struct {
	Entries []vegaCatalogLite `json:"entries"`
}

// GetResourceConnectorType resource_id -> catalog_id -> connector_type (two hops).
func (v *vegaAccess) GetResourceConnectorType(ctx context.Context, resourceID string) (string, error) {
	header := common.GetHeaderForChildOperation(ctx, "vega.resource.connector_type", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	// 1) resource -> catalog_id
	resSrc := fmt.Sprintf("%s/in/v1/resources/%s", v.baseURL, url.PathEscape(resourceID))
	code, body, err := v.httpClient.GetNoUnmarshal(ctx, resSrc, url.Values{}, header)
	if err != nil || code < http.StatusOK || code >= http.StatusMultipleChoices {
		return "", infraErr.DefaultHTTPError(ctx, code,
			fmt.Sprintf("[VegaAccess] get resource %s failed: code=%d, err=%v, body=%s", resourceID, code, err, string(body)))
	}
	var resEnv vegaResourceEnvelope
	if err := sonic.Unmarshal(body, &resEnv); err != nil {
		return "", infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("parse vega resource response failed: %v", err))
	}
	if len(resEnv.Entries) == 0 || resEnv.Entries[0].CatalogID == "" {
		return "", infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			fmt.Sprintf("resource %s not found or has no catalog", resourceID))
	}
	catalogID := resEnv.Entries[0].CatalogID

	// 2) catalog -> connector_type
	catSrc := fmt.Sprintf("%s/in/v1/catalogs/%s", v.baseURL, url.PathEscape(catalogID))
	code, body, err = v.httpClient.GetNoUnmarshal(ctx, catSrc, url.Values{}, header)
	if err != nil || code < http.StatusOK || code >= http.StatusMultipleChoices {
		return "", infraErr.DefaultHTTPError(ctx, code,
			fmt.Sprintf("[VegaAccess] get catalog %s failed: code=%d, err=%v, body=%s", catalogID, code, err, string(body)))
	}
	var catEnv vegaCatalogEnvelope
	if err := sonic.Unmarshal(body, &catEnv); err != nil {
		return "", infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("parse vega catalog response failed: %v", err))
	}
	if len(catEnv.Entries) == 0 || catEnv.Entries[0].ConnectorType == "" {
		return "", infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			fmt.Sprintf("catalog %s not found or has no connector_type", catalogID))
	}
	return catEnv.Entries[0].ConnectorType, nil
}

// vegaResourceFullEnvelope {"entries":[...],"total_count":N} envelope (with physical columns) for vega resources list/get.
type vegaResourceFullEnvelope struct {
	Entries    []interfaces.VegaResource `json:"entries"`
	TotalCount int64                     `json:"total_count"`
}

// ListResources lists queryable data resources. Vega enforces account-level view_detail authorization on this /in endpoint.
// This method only passes account headers through and assembles filter/pagination query parameters.
func (v *vegaAccess) ListResources(ctx context.Context, req *interfaces.VegaListResourcesReq) (*interfaces.VegaListResourcesResp, error) {
	header := common.GetHeaderForChildOperation(ctx, "vega.resource.list", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	params := url.Values{}
	if req != nil {
		if req.CatalogID != "" {
			params.Set("catalog_id", req.CatalogID)
		}
		if req.Category != "" {
			params.Set("category", req.Category)
		}
		if req.Offset > 0 {
			params.Set("offset", strconv.Itoa(req.Offset))
		}
		if req.Limit > 0 {
			params.Set("limit", strconv.Itoa(req.Limit))
		}
	}

	src := fmt.Sprintf("%s/in/v1/resources", v.baseURL)
	code, body, err := v.httpClient.GetNoUnmarshal(ctx, src, params, header)
	if err != nil || code < http.StatusOK || code >= http.StatusMultipleChoices {
		v.logger.WithContext(ctx).Errorf("[VegaAccess] ListResources failed: code=%d, err=%v, body=%s", code, err, string(body))
		return nil, infraErr.DefaultHTTPError(ctx, code,
			fmt.Sprintf("vega list resources failed: %s", string(body)))
	}

	resp := &interfaces.VegaListResourcesResp{}
	if len(body) == 0 {
		return resp, nil
	}
	var env vegaResourceFullEnvelope
	if err := sonic.Unmarshal(body, &env); err != nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("parse vega resources list response failed: %v", err))
	}
	resp.Entries = env.Entries
	resp.TotalCount = env.TotalCount
	return resp, nil
}

// GetResource gets a single resource, including physical columns. Vega get-by-id returns an entries envelope; take the first entry.
// When the resource does not exist or the calling account has no permission, Vega returns non-2xx and this method passes that through as an error.
func (v *vegaAccess) GetResource(ctx context.Context, resourceID string) (*interfaces.VegaResource, error) {
	header := common.GetHeaderForChildOperation(ctx, "vega.resource.get", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	src := fmt.Sprintf("%s/in/v1/resources/%s", v.baseURL, url.PathEscape(resourceID))
	code, body, err := v.httpClient.GetNoUnmarshal(ctx, src, url.Values{}, header)
	if err != nil || code < http.StatusOK || code >= http.StatusMultipleChoices {
		v.logger.WithContext(ctx).Errorf("[VegaAccess] GetResource %s failed: code=%d, err=%v, body=%s", resourceID, code, err, string(body))
		return nil, infraErr.DefaultHTTPError(ctx, code,
			fmt.Sprintf("vega get resource %s failed: %s", resourceID, string(body)))
	}

	var env vegaResourceFullEnvelope
	if err := sonic.Unmarshal(body, &env); err != nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("parse vega resource response failed: %v", err))
	}
	if len(env.Entries) == 0 {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusNotFound,
			fmt.Sprintf("resource %s not found", resourceID))
	}
	res := env.Entries[0]
	return &res, nil
}
