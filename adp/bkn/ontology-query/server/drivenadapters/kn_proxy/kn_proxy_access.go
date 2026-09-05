// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_proxy

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

const proxyLookupTimeoutSeconds = 5

type knowledgeNetworkProxyAccess struct {
	baseURL    string
	httpClient rest.HTTPClient
}

type resolveProxyBindingRequest struct {
	ChildType  string `json:"child_type"`
	ChildID    string `json:"child_id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Operation  string `json:"operation"`
}

func NewKnowledgeNetworkProxyAccess(appSetting *common.AppSetting) interfaces.KnowledgeNetworkProxyAccess {
	baseURL := ""
	if appSetting != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(appSetting.BKNBackendUrl), "/")
	}
	return &knowledgeNetworkProxyAccess{
		baseURL: baseURL,
		httpClient: rest.NewHTTPClientWithOptions(rest.HttpClientOptions{
			TimeOut: proxyLookupTimeoutSeconds,
		}),
	}
}

func (a *knowledgeNetworkProxyAccess) ResolveKnowledgeNetworkProxy(
	ctx context.Context, binding interfaces.TrustedProxyBinding,
) (*interfaces.KnowledgeNetworkProxyAccount, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ResolveKnowledgeNetworkProxy")
	defer span.End()
	if a == nil || a.httpClient == nil || a.baseURL == "" {
		return nil, fmt.Errorf("BKN proxy client is not configured")
	}

	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	endpoint := fmt.Sprintf("%s/%s/proxy-account/resolve", a.baseURL, url.PathEscape(binding.KNID))
	headers := common.MergeTraceHeadersForChildOperation(ctx, map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   account.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: account.Type,
	}, "bkn.proxy.resolve", 1)
	request := resolveProxyBindingRequest{
		ChildType: binding.ChildType, ChildID: binding.ChildID,
		TargetType: binding.TargetType, TargetID: binding.TargetID, Operation: binding.Operation,
	}
	status, body, err := a.httpClient.PostNoUnmarshal(ctx, endpoint, headers, request)
	if err != nil {
		return nil, fmt.Errorf("call BKN proxy lookup: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("BKN proxy lookup returned status %d", status)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("BKN proxy lookup returned an empty response")
	}

	var mapping interfaces.KnowledgeNetworkProxyAccount
	if err := sonic.Unmarshal(body, &mapping); err != nil {
		return nil, fmt.Errorf("decode BKN proxy lookup response: %w", err)
	}
	return &mapping, nil
}
