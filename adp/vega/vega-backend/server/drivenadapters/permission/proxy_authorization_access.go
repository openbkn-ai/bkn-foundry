// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/bytedance/sonic"

	"vega-backend/common"
	"vega-backend/interfaces"
)

const maximumManagedProxyResponse = 1 << 20

type proxyAuthorizationAccess struct {
	safe *safeClient
}

func NewProxyAuthorizationAccess() interfaces.ProxyAuthorizationAccess {
	return &proxyAuthorizationAccess{safe: newSafeClient(strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_SAFE_URL")), "/"))}
}

func (a *proxyAuthorizationAccess) GetManagedProxy(ctx context.Context, proxyID string) (*interfaces.ManagedProxyAccount, error) {
	if a == nil || a.safe == nil || a.safe.baseURL == "" {
		return nil, fmt.Errorf("BKN_SAFE_URL is not configured")
	}
	path := "/api/safe/in/v1/managed-proxy-accounts/" + url.PathEscape(strings.TrimSpace(proxyID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.safe.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range common.BuildTraceHeadersForChildOperation(ctx, "permission.proxy.state", 1) {
		req.Header.Set(key, value)
	}
	resp, err := a.safe.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
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
	if err := sonic.Unmarshal(data, &account); err != nil {
		return nil, err
	}
	return &account, nil
}

func (a *proxyAuthorizationAccess) CheckPermission(
	ctx context.Context, proxyID, resourceType, resourceID, operation string,
) (bool, error) {
	if a == nil || a.safe == nil || a.safe.baseURL == "" {
		return false, fmt.Errorf("BKN_SAFE_URL is not configured")
	}
	return a.safe.checkOne(ctx, proxyID, resourceType, resourceID, operation)
}
