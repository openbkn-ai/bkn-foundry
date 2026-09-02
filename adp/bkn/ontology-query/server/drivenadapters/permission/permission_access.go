// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"ontology-query/common"
	"ontology-query/interfaces"
)

const permissionRequestTimeoutSeconds = 5

type permissionAccess struct {
	baseURL    string
	httpClient rest.HTTPClient
}

func NewPermissionAccess(appSetting *common.AppSetting) interfaces.PermissionAccess {
	baseURL := ""
	if appSetting != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(appSetting.BknSafeURL), "/")
	}
	return &permissionAccess{
		baseURL: baseURL,
		httpClient: rest.NewHTTPClientWithOptions(rest.HttpClientOptions{
			TimeOut: permissionRequestTimeoutSeconds,
		}),
	}
}

func (pa *permissionAccess) FilterResources(ctx context.Context,
	request interfaces.PermissionFilterRequest) (interfaces.PermissionFilterResponse, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "FilterQueryResources")
	defer span.End()

	var response interfaces.PermissionFilterResponse
	endpoint, err := pa.resourceFilterEndpoint()
	if err != nil {
		return response, err
	}

	respCode, body, err := pa.httpClient.PostNoUnmarshal(ctx, endpoint, map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}, request)
	if err != nil {
		return response, fmt.Errorf("call bkn-safe resource-filter: %w", err)
	}
	if respCode != http.StatusOK {
		return response, fmt.Errorf("bkn-safe resource-filter returned status %d", respCode)
	}
	if len(body) == 0 {
		return response, fmt.Errorf("bkn-safe resource-filter returned an empty response")
	}
	if err := sonic.Unmarshal(body, &response); err != nil {
		return response, fmt.Errorf("decode bkn-safe resource-filter response: %w", err)
	}
	if response.Resources == nil {
		return response, fmt.Errorf("bkn-safe resource-filter response omitted resources")
	}
	return response, nil
}

func (pa *permissionAccess) resourceFilterEndpoint() (string, error) {
	if pa == nil || pa.httpClient == nil {
		return "", fmt.Errorf("bkn-safe permission client is not configured")
	}
	parsed, err := url.Parse(pa.baseURL)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("BKN_SAFE_BASE_URL is missing or invalid")
	}
	return pa.baseURL + "/api/safe/v1/authz/resource-filter", nil
}
