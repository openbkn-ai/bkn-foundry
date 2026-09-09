// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"net/http"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kntools"
)

// capturingTools records the request the handler built without calling anything downstream.
type capturingTools struct {
	kntools.KnToolsService
	searchKnID string
	execKnID   string
}

func (c *capturingTools) SearchCapabilities(_ context.Context,
	req *kntools.SearchCapabilitiesReq) (*kntools.SearchCapabilitiesResp, error) {
	c.searchKnID = req.KnID
	return &kntools.SearchCapabilitiesResp{Capabilities: []kntools.CapabilityEntry{}}, nil
}

func (c *capturingTools) ExecuteTool(_ context.Context,
	req *kntools.ExecuteToolReq) (map[string]any, error) {
	c.execKnID = req.KnID
	return map[string]any{}, nil
}

func callWithKnHeader(t *testing.T, tool string, args map[string]any, svc kntools.KnToolsService) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	req.Header = http.Header{}
	req.Header.Set("X-Kn-ID", "kn_from_header")

	var err error
	switch tool {
	case "search_capabilities":
		_, err = handleSearchCapabilities(svc)(context.Background(), req)
	case "execute_tool":
		_, err = handleExecuteTool(svc)(context.Background(), req)
	}
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", tool, err)
	}
}

// TestCapabilitySurfaceTakesTheNetworkFromTheHeader pins a path find_skills had and the two narrow
// tool searches never did.
//
// Clients configure X-Kn-ID once per connection instead of repeating kn_id in every call — the
// platform's own CLI does exactly this. Consolidating onto search_capabilities without picking the
// header up turned those calls into "kn_id is required".
func TestCapabilitySurfaceTakesTheNetworkFromTheHeader(t *testing.T) {
	svc := &capturingTools{}
	callWithKnHeader(t, "search_capabilities", map[string]any{"query": "汇率"}, svc)
	if svc.searchKnID != "kn_from_header" {
		t.Fatalf("search_capabilities 该从 X-Kn-ID 取网络, got %q", svc.searchKnID)
	}

	callWithKnHeader(t, "execute_tool", map[string]any{
		"toolbox_id": "box-1", "tool_id": "t1", "arguments": map[string]any{},
	}, svc)
	if svc.execKnID != "kn_from_header" {
		t.Fatalf("execute_tool 该从 X-Kn-ID 取网络, got %q", svc.execKnID)
	}
}

// TestExplicitNetworkArgumentWinsOverTheHeader keeps the header a fallback rather than an override.
func TestExplicitNetworkArgumentWinsOverTheHeader(t *testing.T) {
	svc := &capturingTools{}
	callWithKnHeader(t, "search_capabilities", map[string]any{"kn_id": "kn_from_args"}, svc)
	if svc.searchKnID != "kn_from_args" {
		t.Fatalf("参数里给了就该用参数的, got %q", svc.searchKnID)
	}
}
