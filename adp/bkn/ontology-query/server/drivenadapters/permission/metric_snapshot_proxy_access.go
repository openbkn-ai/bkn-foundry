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
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func NewMetricSnapshotProxyAccess(setting *common.AppSetting) interfaces.MetricSnapshotProxyAccess {
	return newPermissionAccess(setting)
}

func (pa *permissionAccess) GetManagedProxy(ctx context.Context, proxyID string) (*interfaces.ManagedProxyAccountState, error) {
	if proxyID == "" || strings.TrimSpace(proxyID) != proxyID {
		return nil, fmt.Errorf("managed proxy identity is invalid")
	}
	endpoint, err := pa.endpoint("/api/safe/in/v1/managed-proxy-accounts/" + url.PathEscape(proxyID))
	if err != nil {
		return nil, err
	}
	code, body, err := pa.httpClient.GetNoUnmarshal(ctx, endpoint, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("load managed proxy state: %w", err)
	}
	if code == http.StatusNotFound {
		return nil, nil
	}
	if code != http.StatusOK || len(body) == 0 || len(body) > 1<<20 {
		return nil, fmt.Errorf("invalid managed proxy state response: status %d", code)
	}
	var state interfaces.ManagedProxyAccountState
	if err := sonic.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("decode managed proxy state: %w", err)
	}
	return &state, nil
}
