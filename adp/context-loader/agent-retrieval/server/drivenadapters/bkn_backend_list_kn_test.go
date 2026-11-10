// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/http"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// bkn-backend names a network's description `comment`. Read as `description`, every
// network listed with an empty one, which left get_kn_detail -- the whole concept
// model -- as the only way an agent could learn what a network is about.
func TestListKnowledgeNetworksCarriesComment(t *testing.T) {
	const body = `{"entries":[
		{"id":"kn-1","name":"供应链","comment":"采购、库存与交付","module_type":"knowledge_network"},
		{"id":"kn-2","name":"空说明","comment":""}
	],"total_count":2}`
	ctrl := gomock.NewController(t)
	mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
	mockHTTPClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://bkn/in/v1/knowledge-networks", gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte(body), nil)
	client := &bknBackendAccess{logger: mocks.NewMockLogger(ctrl), baseURL: "http://bkn", httpClient: mockHTTPClient}

	resp, err := client.ListKnowledgeNetworks(context.Background(), &interfaces.ListKnReq{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 || len(resp.Entries) != 2 {
		t.Fatalf("got %d entries of %d, want 2 of 2", len(resp.Entries), resp.TotalCount)
	}
	if got := resp.Entries[0]; got.ID != "kn-1" || got.Name != "供应链" || got.Description != "采购、库存与交付" || got.ModuleType != "knowledge_network" {
		t.Fatalf("entry = %+v, want the network's comment as its description", got)
	}
	if got := resp.Entries[1].Description; got != "" {
		t.Fatalf("description = %q, want empty for a network without a comment", got)
	}
}
