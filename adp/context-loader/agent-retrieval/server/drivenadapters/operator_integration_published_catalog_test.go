// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestPublishedToolCatalogUsesCallerCredentialAndReturnsSafeFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer caller-appkey" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("x-business-domain"); got == "" {
			t.Fatal("x-business-domain is missing")
		}
		switch r.URL.Path {
		case "/api/agent-operator-integration/v1/tool-box/list":
			if got := r.URL.Query().Get("status"); got != "published" {
				t.Fatalf("toolbox status = %q", got)
			}
			if got := r.URL.Query().Get("metadata_type"); got != "function" {
				t.Fatalf("toolbox metadata_type = %q", got)
			}
			_, _ = w.Write([]byte(`{"data":[{"box_id":"box-1","box_name":"供应链函数","box_desc":"已发布函数","status":"published","box_svc_url":"http://internal"}]}`))
		case "/api/agent-operator-integration/v1/tool-box/box-1/tools/list":
			if got := r.URL.Query().Get("status"); got != "enabled" {
				t.Fatalf("tool status = %q", got)
			}
			_, _ = w.Write([]byte(`{"box_id":"box-1","tools":[{"tool_id":"tool-1","name":"标准交期","description":"返回标准交期","status":"enabled","use_rule":"按物料编码查询","metadata":{"server_url":"http://internal","api_spec":{"servers":[{"url":"http://internal"}],"security":[{"api_key":[]}],"parameters":[{"name":"material_code","required":true,"type":"string"}]}}}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &operatorIntegrationClient{
		baseURL:    server.URL + "/api/agent-operator-integration",
		httpClient: rest.NewHTTPClientWithRawClient(server.Client()),
	}
	ctx := common.SetRawTokenToCtx(context.Background(), "caller-appkey")

	boxes, err := client.ListPublishedToolboxes(ctx, &interfaces.ListPublishedToolboxesRequest{Keyword: "供应链"})
	if err != nil {
		t.Fatalf("list toolboxes: %v", err)
	}
	if len(boxes.Toolboxes) != 1 || boxes.Toolboxes[0].ToolboxID != "box-1" || boxes.Toolboxes[0].Name != "供应链函数" {
		t.Fatalf("unexpected toolboxes: %#v", boxes)
	}

	tools, err := client.ListPublishedTools(ctx, &interfaces.ListPublishedToolsRequest{ToolboxID: "box-1"})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].ToolID != "tool-1" || tools.Tools[0].InputSchema["parameters"] == nil {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	if _, leaked := tools.Tools[0].InputSchema["server_url"]; leaked {
		t.Fatalf("internal server URL leaked: %#v", tools.Tools[0])
	}
	if _, leaked := tools.Tools[0].InputSchema["servers"]; leaked {
		t.Fatalf("OpenAPI server topology leaked: %#v", tools.Tools[0])
	}
	if _, leaked := tools.Tools[0].InputSchema["security"]; leaked {
		t.Fatalf("transport security detail leaked: %#v", tools.Tools[0])
	}
}
