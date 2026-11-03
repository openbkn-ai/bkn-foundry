// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknbackend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
)

// PublishedProxy reads existing internal governance state without granting or
// repairing it. Only a fully synchronized active mapping can read a release.
func (bba *bknBackendAccess) PublishedProxy(ctx context.Context, network string) (kb.PublishedProxy, error) {
	if network == "" || !strings.HasSuffix(bba.baseUrl, "/v1") {
		return kb.PublishedProxy{}, kb.ErrUnavailable
	}
	endpoint := strings.TrimSuffix(bba.baseUrl, "/v1") + "/in/v1/knowledge-networks/" + url.PathEscape(network) + "/proxy-account"
	status, body, err := bba.httpClient.GetNoUnmarshal(ctx, endpoint, nil, nil)
	if err != nil {
		return kb.PublishedProxy{}, kb.ErrUnavailable
	}
	if status != http.StatusOK {
		return kb.PublishedProxy{}, backendError(status, body)
	}
	var mapping struct {
		NetworkID   string `json:"kn_id"`
		AccountID   string `json:"proxy_account_id"`
		AccountType string `json:"proxy_account_type"`
		Lifecycle   string `json:"lifecycle_status"`
		Version     int64  `json:"version"`
		SyncStatus  string `json:"sync_status"`
		Published   string `json:"published_model_version"`
		Synced      string `json:"synced_model_version"`
	}
	if json.Unmarshal(body, &mapping) != nil || mapping.NetworkID != network || mapping.AccountID == "" || mapping.AccountType != "app" || mapping.Lifecycle != "active" || mapping.Version < 1 || mapping.SyncStatus != "ready" || mapping.Published == "" || mapping.Published != mapping.Synced {
		return kb.PublishedProxy{}, kb.ErrUnavailable
	}
	return kb.PublishedProxy{NetworkID: network, AccountID: mapping.AccountID, Version: mapping.Version}, nil
}

var _ kb.NetworkProxy = (*bknBackendAccess)(nil)
