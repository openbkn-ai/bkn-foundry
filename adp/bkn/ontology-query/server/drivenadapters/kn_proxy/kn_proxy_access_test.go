// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_proxy

import (
	"context"
	"net/http"
	"testing"

	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"go.uber.org/mock/gomock"

	"ontology-query/interfaces"
)

func TestResolveKnowledgeNetworkProxyUsesCallerAndCanonicalEndpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "caller-1", Type: "user"})

	binding := interfaces.TrustedProxyBinding{
		KNID: "kn-1", ChildType: "object_type", ChildID: "ot-1",
		TargetType: "resource", TargetID: "resource-1", Operation: "query_data",
	}
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(),
		"http://bkn/api/bkn-backend/in/v1/knowledge-networks/kn-1/proxy-account/resolve", gomock.Any(), resolveProxyBindingRequest{
			ChildType: "object_type", ChildID: "ot-1",
			TargetType: "resource", TargetID: "resource-1", Operation: "query_data",
		}).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, request any) (int, []byte, error) {
			if headers[interfaces.HTTP_HEADER_ACCOUNT_ID] != "caller-1" ||
				headers[interfaces.HTTP_HEADER_ACCOUNT_TYPE] != "user" {
				t.Fatalf("unexpected caller headers: %#v", headers)
			}
			if _, ok := request.(interfaces.TrustedProxyBinding); ok {
				t.Fatal("internal context object, including kn_id, leaked into the BKN request body")
			}
			return http.StatusOK, []byte(`{
				"kn_id":"kn-1","proxy_account_id":"proxy-1","proxy_account_type":"app",
				"lifecycle_status":"active","version":2,"sync_status":"ready",
				"published_model_version":"v1","synced_model_version":"v1"
			}`), nil
		})

	access := &knowledgeNetworkProxyAccess{baseURL: "http://bkn/api/bkn-backend/in/v1/knowledge-networks", httpClient: httpClient}
	mapping, err := access.ResolveKnowledgeNetworkProxy(ctx, binding)
	if err != nil {
		t.Fatalf("ResolveKnowledgeNetworkProxy() error = %v", err)
	}
	if mapping.ProxyAccountID != "proxy-1" || mapping.Version != 2 {
		t.Fatalf("unexpected mapping: %#v", mapping)
	}
}

func TestResolveKnowledgeNetworkProxyRejectsIncompleteResponses(t *testing.T) {
	for name, test := range map[string]struct {
		status int
		body   string
	}{
		"non success":  {status: http.StatusForbidden, body: `{}`},
		"empty body":   {status: http.StatusOK, body: ``},
		"invalid json": {status: http.StatusOK, body: `{`},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			httpClient := rmock.NewMockHTTPClient(ctrl)
			httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(test.status, []byte(test.body), nil)
			access := &knowledgeNetworkProxyAccess{baseURL: "http://bkn", httpClient: httpClient}
			if _, err := access.ResolveKnowledgeNetworkProxy(context.Background(), interfaces.TrustedProxyBinding{KNID: "kn-1"}); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestResolveKnowledgeNetworkProxyPreservesOnlyStableDownstreamErrorContract(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusServiceUnavailable, []byte(`{
			"error_code":"BknBackend.KnowledgeNetwork.Proxy.SyncFailed",
			"description":"must not cross service boundary",
			"error_details":"proxy-credential-secret"
		}`), nil)
	access := &knowledgeNetworkProxyAccess{baseURL: "http://bkn", httpClient: httpClient}

	_, err := access.ResolveKnowledgeNetworkProxy(context.Background(), interfaces.TrustedProxyBinding{KNID: "kn-1"})
	resolutionErr, ok := err.(*interfaces.KnowledgeNetworkProxyResolveError)
	if !ok || resolutionErr.StatusCode != http.StatusServiceUnavailable ||
		resolutionErr.Code != "BknBackend.KnowledgeNetwork.Proxy.SyncFailed" {
		t.Fatalf("error = %#v", err)
	}
	if got := resolutionErr.Error(); got == "" || got == "proxy-credential-secret" {
		t.Fatalf("unexpected sanitized error string %q", got)
	}
}
