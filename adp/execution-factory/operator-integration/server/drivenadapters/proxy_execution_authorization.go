// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const maximumManagedProxyResponse = 1 << 20

type proxyExecutionAuthorizationAccess struct {
	safe *safeAuthorization
}

// NewProxyExecutionAuthorizationAccess creates the bkn-safe access used by the
// mandatory managed-proxy PEP. It intentionally does not depend on AUTH_ENABLED.
func NewProxyExecutionAuthorizationAccess() interfaces.ProxyExecutionAuthorizationAccess {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_SAFE_URL")), "/")
	return &proxyExecutionAuthorizationAccess{
		safe: newSafeAuthorization(baseURL, config.NewConfigLoader().GetLogger()),
	}
}

func (a *proxyExecutionAuthorizationAccess) GetManagedProxy(
	ctx context.Context, proxyID string,
) (*interfaces.ManagedProxyAccount, error) {
	if a == nil || a.safe == nil || a.safe.baseURL == "" {
		return nil, fmt.Errorf("BKN_SAFE_URL is not configured")
	}

	path := "/api/safe/in/v1/managed-proxy-accounts/" + url.PathEscape(strings.TrimSpace(proxyID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.safe.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range common.BuildTraceHeaders(ctx) {
		req.Header.Set(key, value)
	}
	resp, err := a.safe.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("bkn-safe GET %s: status %d", path, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maximumManagedProxyResponse+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maximumManagedProxyResponse {
		return nil, fmt.Errorf("bkn-safe managed proxy response is too large")
	}
	var account interfaces.ManagedProxyAccount
	if err := json.Unmarshal(data, &account); err != nil {
		return nil, err
	}
	return &account, nil
}

func (a *proxyExecutionAuthorizationAccess) CheckPermission(
	ctx context.Context, proxyID, resourceType, resourceID, operation string,
) (bool, error) {
	if a == nil || a.safe == nil || a.safe.baseURL == "" {
		return false, fmt.Errorf("BKN_SAFE_URL is not configured")
	}
	return a.safe.checkOne(ctx, proxyID, resourceType, resourceID, operation)
}
