// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestGetActionExecutionPagesAllResults(t *testing.T) {
	const total = 251
	client := &ontologyQueryClient{
		logger:  &mockLogger{},
		baseURL: "http://ontology.example.com",
		httpClient: &mockHTTPClient{handlerFunc: func(_ context.Context, method, rawURL string, _ map[string]string, _ interface{}) (int, interface{}, error) {
			if method != "GET" {
				return 0, nil, fmt.Errorf("method = %s, want GET", method)
			}
			u, err := url.Parse(rawURL)
			if err != nil {
				return 0, nil, err
			}
			if !strings.HasSuffix(u.Path, "/in/v1/knowledge-networks/kn-1/action-logs/e-1") {
				return 0, nil, fmt.Errorf("path = %s, want action-logs/e-1", u.Path)
			}
			limit, err := strconv.Atoi(u.Query().Get("results_limit"))
			if err != nil {
				return 0, nil, err
			}
			offset, err := strconv.Atoi(u.Query().Get("results_offset"))
			if err != nil {
				return 0, nil, err
			}
			results := make([]map[string]any, 0, limit)
			for i := offset; i < min(offset+limit, total); i++ {
				results = append(results, map[string]any{"_instance_id": fmt.Sprintf("object-%d", i)})
			}
			return 200, map[string]any{
				"id": "e-1", "status": "completed", "results": results,
				"results_total": total, "results_offset": offset, "results_limit": limit,
			}, nil
		}},
	}

	seen := map[string]bool{}
	for offset := 0; offset < total; offset += 100 {
		limit := 100
		pageOffset := offset
		resp, err := client.GetActionExecution(context.Background(), &interfaces.GetActionExecutionRequest{
			KnID: "kn-1", ExecutionID: "e-1", ResultsLimit: &limit, ResultsOffset: &pageOffset,
		})
		if err != nil {
			t.Fatalf("page at offset %d: %v", offset, err)
		}
		if got := resp["results_total"]; got == nil || fmt.Sprint(got) != "251" {
			t.Fatalf("page total = %v, want 251", got)
		}
		for _, item := range resp["results"].([]any) {
			id := item.(map[string]any)["_instance_id"].(string)
			if seen[id] {
				t.Fatalf("duplicate result %s", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != total {
		t.Fatalf("received %d results, want %d", len(seen), total)
	}
}

func TestGetActionExecutionWithoutPaginationUsesLegacyPath(t *testing.T) {
	client := &ontologyQueryClient{
		logger:  &mockLogger{},
		baseURL: "http://ontology.example.com",
		httpClient: &mockHTTPClient{handlerFunc: func(_ context.Context, _, rawURL string, _ map[string]string, _ interface{}) (int, interface{}, error) {
			if !strings.HasSuffix(rawURL, "/in/v1/knowledge-networks/kn-1/action-executions/e-1") {
				return 0, nil, fmt.Errorf("unexpected legacy URL: %s", rawURL)
			}
			return 200, map[string]any{"id": "e-1", "results": []any{}}, nil
		}},
	}
	if _, err := client.GetActionExecution(context.Background(), &interfaces.GetActionExecutionRequest{
		KnID: "kn-1", ExecutionID: "e-1",
	}); err != nil {
		t.Fatal(err)
	}
}
