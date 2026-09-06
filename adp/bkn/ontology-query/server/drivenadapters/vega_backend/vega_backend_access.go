// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package vega_backend

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

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

func directCallerHeaders(ctx context.Context, operation string) map[string]string {
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return common.MergeTraceHeadersForChildOperation(ctx, map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   account.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: account.Type,
	}, operation, 1)
}

func useDirectCaller(ctx context.Context) bool {
	proxy, ok := interfaces.TrustedProxyContextFromContext(ctx)
	return ok && proxy.UseDirectCaller
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

func (v *vegaBackendAccess) buildProxyHeaders(
	ctx context.Context, resourceID, operation string,
) (map[string]string, error) {
	proxy, ok := interfaces.TrustedProxyContextFromContext(ctx)
	if !ok || proxy.Proxy.ID == "" || proxy.Proxy.Type != interfaces.ProxyAccountTypeApp ||
		proxy.Caller.ID == "" || proxy.Caller.Type == "" || proxy.ProxyVersion <= 0 {
		return nil, fmt.Errorf("trusted proxy context is missing or incomplete")
	}
	binding := proxy.Binding
	if binding.TargetType != interfaces.ProxyTargetTypeResource ||
		binding.TargetID != resourceID || binding.Operation != operation ||
		binding.KNID == "" || binding.ChildType == "" || binding.ChildID == "" {
		return nil, fmt.Errorf("resource does not match the trusted published binding")
	}

	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME:         interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:    proxy.Proxy.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE:  proxy.Proxy.Type,
		interfaces.HTTPHeaderBKNCallerID:     proxy.Caller.ID,
		interfaces.HTTPHeaderBKNCallerType:   proxy.Caller.Type,
		interfaces.HTTPHeaderBKNKnowledgeID:  binding.KNID,
		interfaces.HTTPHeaderBKNChildType:    binding.ChildType,
		interfaces.HTTPHeaderBKNChildID:      binding.ChildID,
		interfaces.HTTPHeaderBKNProxyVersion: strconv.FormatInt(proxy.ProxyVersion, 10),
		interfaces.HTTPHeaderBKNTargetType:   binding.TargetType,
		interfaces.HTTPHeaderBKNTargetID:     binding.TargetID,
		interfaces.HTTPHeaderBKNOperation:    binding.Operation,
	}
	if executionID := strings.TrimSpace(proxy.ExecutionID); executionID != "" {
		headers[interfaces.HTTPHeaderBKNExecutionID] = executionID
	}
	return common.MergeTraceHeadersForChildOperation(ctx, headers, "vega.resource.proxy.query", 1), nil
}

func (v *vegaBackendAccess) GetResourceSchema(ctx context.Context,
	resourceID string) (*interfaces.ResourceSchemaResponse, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetResourceSchema")
	defer span.End()

	httpURL := fmt.Sprintf("%s/proxy/resources/%s/schema", v.baseURL, url.PathEscape(resourceID))
	headers := directCallerHeaders(ctx, "vega.resource.schema")
	if !useDirectCaller(ctx) {
		var err error
		headers, err = v.buildProxyHeaders(ctx, resourceID, interfaces.PermissionOperationViewDetail)
		if err != nil {
			return nil, err
		}
	} else {
		httpURL = fmt.Sprintf("%s/resources/%s/schema", v.baseURL, url.PathEscape(resourceID))
	}
	respCode, respData, err := v.httpClient.GetNoUnmarshal(ctx, httpURL, nil, headers)
	logger.Debugf("GetResourceSchema [%s] code [%d] err [%v]", httpURL, respCode, err)
	if err != nil {
		return nil, fmt.Errorf("GetResourceSchema http request failed: %w", err)
	}
	if respCode != http.StatusOK {
		return nil, interfaces.NewVegaDownstreamError(respCode, "")
	}
	var response interfaces.ResourceSchemaResponse
	if err := common.UnmarshalPreciseJSON(respData, &response); err != nil {
		return nil, fmt.Errorf("unmarshal GetResourceSchema response: %w", err)
	}
	if response.SchemaDefinition == nil {
		response.SchemaDefinition = []map[string]any{}
	}
	return &response, nil
}

func (v *vegaBackendAccess) QueryResourceData(ctx context.Context, resourceID string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "QueryResourceData")
	defer span.End()

	httpURL := fmt.Sprintf("%s/proxy/resources/%s/data", v.baseURL, url.PathEscape(resourceID))
	headers := directCallerHeaders(ctx, "vega.resource.query")
	if !useDirectCaller(ctx) {
		var err error
		headers, err = v.buildProxyHeaders(ctx, resourceID, interfaces.PermissionOperationQueryData)
		if err != nil {
			return nil, err
		}
	} else {
		httpURL = fmt.Sprintf("%s/resources/%s/data", v.baseURL, url.PathEscape(resourceID))
	}
	headers[interfaces.HTTP_HEADER_METHOD_OVERRIDE] = http.MethodGet

	request := normalizeResourceDataQueryParams(params)
	respCode, respData, err := v.httpClient.PostNoUnmarshal(ctx, httpURL, headers, request)
	logger.Debugf("QueryResourceData [%s] query_hash [%s] code [%d] err [%v]", httpURL, bkntrace.HashValue(request), respCode, err)
	if err != nil {
		return nil, fmt.Errorf("QueryResourceData http request failed: %w", err)
	}
	if respCode != http.StatusOK {
		// Restricted proxy responses may contain resource or catalog identifiers.
		// Preserve only the status classification for the business caller.
		return nil, interfaces.NewVegaDownstreamError(respCode, "")
	}
	var response interfaces.DatasetQueryResponse
	// Dynamic resource fields must keep their original JSON number representation.
	if err := common.UnmarshalPreciseJSON(respData, &response); err != nil {
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
