// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"bkn-backend/interfaces"
)

const maxResponseBytes = 4 << 20

type safeClient struct {
	baseURL string
	http    *http.Client
}

// NewManagedProxyAccess creates the internal bkn-safe lifecycle and grant client.
func NewManagedProxyAccess(baseURL string) interfaces.ManagedProxyAccess {
	return &safeClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *safeClient) Create(ctx context.Context, knID, name string) (*interfaces.ManagedProxyAccount, bool, error) {
	var account interfaces.ManagedProxyAccount
	status, err := c.do(ctx, http.MethodPost, "/api/safe/in/v1/managed-proxy-accounts", map[string]any{
		"managed_resource_type": "knowledge_network",
		"managed_resource_id":   knID,
		"name":                  name,
	}, &account)
	if err != nil {
		return nil, false, err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return nil, false, fmt.Errorf("unexpected managed proxy create status %d", status)
	}
	if account.ProxyAccountID == "" || account.ManagedResourceID != knID ||
		account.ManagedResourceType != "knowledge_network" || account.ManagedBy != "bkn" ||
		account.AccountType != interfaces.KNProxyAccountTypeApp ||
		account.LifecycleStatus != interfaces.KNProxyLifecycleActive || !account.Enabled ||
		account.LoginEnabled || account.CredentialIssuanceEnabled {
		return nil, false, fmt.Errorf("invalid managed proxy create response")
	}
	return &account, status == http.StatusCreated, nil
}

func (c *safeClient) Disable(ctx context.Context, proxyAccountID string) (*interfaces.ManagedProxyAccount, error) {
	return c.changeLifecycle(ctx, proxyAccountID, "disable")
}

func (c *safeClient) Archive(ctx context.Context, proxyAccountID string) (*interfaces.ManagedProxyAccount, error) {
	return c.changeLifecycle(ctx, proxyAccountID, "archive")
}

func (c *safeClient) changeLifecycle(ctx context.Context, proxyAccountID, action string) (*interfaces.ManagedProxyAccount, error) {
	var account interfaces.ManagedProxyAccount
	_, err := c.do(ctx, http.MethodPost,
		"/api/safe/in/v1/managed-proxy-accounts/"+proxyAccountID+"/"+action, nil, &account)
	if err != nil {
		return nil, err
	}
	if account.ProxyAccountID != proxyAccountID {
		return nil, fmt.Errorf("invalid managed proxy lifecycle response")
	}
	if action == "disable" && account.LifecycleStatus != interfaces.KNProxyLifecycleDisabling &&
		account.LifecycleStatus != interfaces.KNProxyLifecycleArchived {
		return nil, fmt.Errorf("invalid managed proxy disable state")
	}
	if action == "archive" && account.LifecycleStatus != interfaces.KNProxyLifecycleArchived {
		return nil, fmt.Errorf("invalid managed proxy archive state")
	}
	if account.Enabled || account.LoginEnabled || account.CredentialIssuanceEnabled {
		return nil, fmt.Errorf("managed proxy lifecycle response is not fail-closed")
	}
	return &account, nil
}

func (c *safeClient) CheckGrant(ctx context.Context, proxyAccountID, grantorID string,
	source interfaces.ProxyGrantSourceSpec) (interfaces.ProxyGrantCheckResult, error) {
	var result interfaces.ProxyGrantCheckResult
	_, err := c.do(ctx, http.MethodPost, "/api/safe/in/v1/proxy-grant-sources/check", map[string]any{
		"proxy_account_id": proxyAccountID,
		"grantor_id":       grantorID,
		"source":           source,
	}, &result)
	return result, err
}

func (c *safeClient) SyncGrants(ctx context.Context, proxyAccountID, grantorID string,
	sources []interfaces.ProxyGrantSourceSpec) (interfaces.ProxyGrantSyncResult, error) {
	var result interfaces.ProxyGrantSyncResult
	_, err := c.do(ctx, http.MethodPost, "/api/safe/in/v1/proxy-grant-sources/sync", map[string]any{
		"proxy_account_id": proxyAccountID,
		"grantor_id":       grantorID,
		"sources":          sources,
	}, &result)
	return result, err
}

func (c *safeClient) ReconcileGrants(ctx context.Context, proxyAccountID, requestedBy string) (interfaces.ProxyGrantReconcileResult, error) {
	var result interfaces.ProxyGrantReconcileResult
	_, err := c.do(ctx, http.MethodPost, "/api/safe/in/v1/proxy-grant-sources/reconcile", map[string]any{
		"proxy_account_id": proxyAccountID,
		"requested_by":     requestedBy,
	}, &result)
	return result, err
}

func (c *safeClient) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return resp.StatusCode, fmt.Errorf("bkn-safe %s %s returned status %d", method, path, resp.StatusCode)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}
