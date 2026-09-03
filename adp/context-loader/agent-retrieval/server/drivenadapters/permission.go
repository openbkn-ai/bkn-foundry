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
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infrarest "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const permissionRequestTimeoutSeconds = 5

type permissionAccess struct {
	baseURL    string
	httpClient interfaces.HTTPClient
}

// NewPermissionAccess creates the context-loader bkn-safe adapter.
func NewPermissionAccess(conf *config.Config) interfaces.PermissionAccess {
	baseURL := ""
	if conf != nil {
		baseURL = strings.TrimRight(conf.BknSafe.BuildURL(""), "/")
	}
	return &permissionAccess{
		baseURL: baseURL,
		httpClient: infrarest.NewHTTPClientWithOptions(infrarest.HTTPClientOptions{
			TimeOut: permissionRequestTimeoutSeconds,
		}),
	}
}

func (a *permissionAccess) FilterResources(ctx context.Context,
	request interfaces.PermissionFilterRequest,
) (interfaces.PermissionFilterResponse, error) {
	var response interfaces.PermissionFilterResponse
	endpoint, err := a.resourceFilterEndpoint()
	if err != nil {
		return response, err
	}

	headers := common.GetHeaderForChildOperation(ctx, "safe.query_candidate.filter", 1)
	headers[infrarest.ContentTypeKey] = infrarest.ContentTypeJSON
	status, body, err := a.httpClient.PostNoUnmarshal(ctx, endpoint, headers, request)
	if err != nil {
		return response, fmt.Errorf("call bkn-safe resource-filter: %w", err)
	}
	if status != http.StatusOK {
		return response, fmt.Errorf("bkn-safe resource-filter returned status %d", status)
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

func (a *permissionAccess) resourceFilterEndpoint() (string, error) {
	if a == nil || a.httpClient == nil {
		return "", fmt.Errorf("bkn-safe permission client is not configured")
	}
	parsed, err := url.Parse(a.baseURL)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("bkn-safe URL is missing or invalid")
	}
	return a.baseURL + "/api/safe/v1/authz/resource-filter", nil
}
