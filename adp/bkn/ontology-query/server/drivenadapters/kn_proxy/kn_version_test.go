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

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func TestResolveKnowledgeNetworkProxyAsksForTheBranchOfTheRunningVersion(t *testing.T) {
	for branch, endpoint := range map[string]string{
		"v3": "http://bkn/in/v1/knowledge-networks/kn-1/proxy-account/resolve?branch=v3",
		"":   "http://bkn/in/v1/knowledge-networks/kn-1/proxy-account/resolve",
	} {
		ctrl := gomock.NewController(t)
		httpClient := rmock.NewMockHTTPClient(ctrl)
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
			interfaces.AccountInfo{ID: "caller-1", Type: "user"})
		httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), endpoint, gomock.Any(), gomock.Any()).
			Return(http.StatusOK, []byte(`{"kn_id":"kn-1"}`), nil)

		access := &knowledgeNetworkProxyAccess{baseURL: "http://bkn/in/v1/knowledge-networks", httpClient: httpClient}
		if _, err := access.ResolveKnowledgeNetworkProxy(ctx, interfaces.TrustedProxyBinding{
			KNID: "kn-1", ChildType: "object_type", ChildID: "ot-1", TargetID: "resource-1",
			Operation: "query_data", Branch: branch,
		}); err != nil {
			t.Fatal(err)
		}
	}
}
