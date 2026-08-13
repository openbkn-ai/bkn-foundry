// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package vega_backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	"ontology-query/common"
	"ontology-query/common/bkntrace"
	"ontology-query/interfaces"
)

var (
	vbOnce sync.Once
	vb     interfaces.VegaBackendAccess
)

type vegaBackendAccess struct {
	appSetting *common.AppSetting
	httpClient rest.HTTPClient
	baseURL    string
}

// NewVegaBackendAccess creates vega-backend client (aligned with bkn-backend).
func NewVegaBackendAccess(appSetting *common.AppSetting) interfaces.VegaBackendAccess {
	vbOnce.Do(func() {
		vb = &vegaBackendAccess{
			appSetting: appSetting,
			httpClient: common.NewHTTPClient(),
			baseURL:    appSetting.VegaBackendUrl,
		}
	})
	return vb
}

func (v *vegaBackendAccess) buildHeaders(ctx context.Context) map[string]string {
	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	// 用当前token的用户去访问vega
	return common.MergeTraceHeadersForChildOperation(ctx, map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}, "vega.resource.query", 1)
}

func (v *vegaBackendAccess) QueryResourceData(ctx context.Context, resourceID string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "QueryResourceData")
	defer span.End()

	httpURL := fmt.Sprintf("%s/resources/%s/data", v.baseURL, url.PathEscape(resourceID))
	headers := v.buildHeaders(ctx)
	headers[interfaces.HTTP_HEADER_METHOD_OVERRIDE] = http.MethodGet

	request := normalizeResourceDataQueryParams(params)
	respCode, respData, err := v.httpClient.PostNoUnmarshal(ctx, httpURL, headers, request)
	logger.Debugf("QueryResourceData [%s] query_hash [%s] code [%d] err [%v]", httpURL, bkntrace.HashValue(request), respCode, err)
	if err != nil {
		return nil, fmt.Errorf("QueryResourceData http request failed: %w", err)
	}
	if respCode != http.StatusOK {
		return nil, interfaces.NewVegaDownstreamError(respCode, string(respData))
	}
	var response interfaces.DatasetQueryResponse
	if err := json.Unmarshal([]byte(respData), &response); err != nil {
		return nil, fmt.Errorf("unmarshal QueryResourceData response: %w", err)
	}
	return &response, nil
}

// normalizeResourceDataQueryParams copies Limit/Offset helpers into paging so the
// outbound body matches vega-backend's paging contract (DefaultPageLimit otherwise).
func normalizeResourceDataQueryParams(params *interfaces.ResourceDataQueryParams) *interfaces.ResourceDataQueryParams {
	if params == nil {
		return &interfaces.ResourceDataQueryParams{
			Paging: interfaces.ResourceDataPagingRequest{Mode: "single"},
		}
	}
	request := *params
	if request.Paging.Cursor == "" && request.Paging.Mode == "" {
		request.Paging.Mode = "single"
		if request.Paging.Limit == 0 {
			request.Paging.Limit = request.Limit
		}
		if request.Paging.Offset == 0 {
			request.Paging.Offset = request.Offset
		}
	}
	// Keep helpers out of the wire payload (json:"-"), paging carries the values.
	request.Limit = 0
	request.Offset = 0
	return &request
}
