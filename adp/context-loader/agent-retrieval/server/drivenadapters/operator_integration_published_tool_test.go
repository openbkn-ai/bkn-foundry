// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestExecutePublishedToolForwardsOriginalCredentialAndManagedContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/agent-operator-integration/v1/tool-box/box-1/proxy/tool-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer caller-appkey" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get(common.HeaderBKNConversationID); got != "conv-1" {
			t.Fatalf("conversation header = %q", got)
		}
		if got := r.Header.Get(common.HeaderBKNInteractionID); got != "int-1" {
			t.Fatalf("interaction header = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		payload, ok := body["body"].(map[string]any)
		if !ok || len(body) != 1 || payload["material_code"] != "606-000989" {
			t.Fatalf("proxy envelope or business body is invalid: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"body":{"exit_code":0,"result":{"leadtime_days":14}}}`))
	}))
	defer server.Close()

	client := &operatorIntegrationClient{
		baseURL:    server.URL + "/api/agent-operator-integration",
		httpClient: rest.NewHTTPClientWithRawClient(server.Client()),
	}
	ctx := common.SetRawTokenToCtx(context.Background(), "caller-appkey")
	result, err := client.ExecutePublishedTool(ctx, &interfaces.ExecutePublishedToolRequest{
		ToolboxID: "box-1", ToolID: "tool-1",
		Parameters:        map[string]any{"material_code": "606-000989"},
		BKNConversationID: "conv-1", BKNInteractionID: "int-1",
	})
	if err != nil {
		t.Fatalf("execute published tool: %v", err)
	}
	body, ok := result["body"].(map[string]any)
	if !ok || body["exit_code"].(float64) != 0 {
		t.Fatalf("unexpected response: %#v", result)
	}
}
