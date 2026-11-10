// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/knversion"
)

func versionReq(args map[string]any, headers map[string]string) mcp.CallToolRequest {
	req := mcp.CallToolRequest{Header: http.Header{}}
	req.Params.Arguments = args
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func requested(t *testing.T, args map[string]any, headers map[string]string) (string, string) {
	t.Helper()
	sel, err := knVersionFromRequest(versionReq(args, headers))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel == nil {
		return "", ""
	}
	return sel.Params()
}

func TestKnVersionNothingRequestedLeavesTheCallAlone(t *testing.T) {
	sel, err := knVersionFromRequest(versionReq(map[string]any{"kn_id": "kn"}, nil))
	if sel != nil || err != nil {
		t.Fatalf("got %v, %v", sel, err)
	}
}

func TestKnVersionArgumentWinsOverHeader(t *testing.T) {
	headers := map[string]string{knversion.HeaderVersion: "draft", knversion.HeaderDraftRevision: "9"}
	if v, r := requested(t, map[string]any{"kn_version": "5"}, headers); v != "5" || r != "" {
		t.Fatalf("argument kn_version: %q %q", v, r)
	}
	if v, r := requested(t, nil, headers); v != "draft" || r != "9" {
		t.Fatalf("header only: %q %q", v, r)
	}
	if v, r := requested(t, map[string]any{"kn_version": "draft", "draft_revision": float64(4)}, headers); v != "draft" || r != "4" {
		t.Fatalf("argument draft + revision: %q %q", v, r)
	}
}

func TestKnVersionDraftRevisionNeedsDraft(t *testing.T) {
	for _, args := range []map[string]any{
		{"draft_revision": 3},
		{"kn_version": "running", "draft_revision": 3},
		{"kn_version": "bogus"},
	} {
		if _, err := knVersionFromRequest(versionReq(args, nil)); err == nil {
			t.Errorf("args %v: want a parameter error", args)
		}
	}
}

func TestKnVersionBranchConflict(t *testing.T) {
	for _, tc := range []struct {
		branch, version string
		ok              bool
	}{
		{"main", "running", true}, {"draft", "draft", true},
		{"main", "draft", false}, {"draft", "running", false}, {"draft", "3", false}, {"feature", "running", false},
	} {
		_, err := knVersionFromRequest(versionReq(map[string]any{"branch": tc.branch, "kn_version": tc.version}, nil))
		if (err == nil) != tc.ok {
			t.Errorf("branch=%s kn_version=%s: err=%v, want ok=%v", tc.branch, tc.version, err, tc.ok)
		}
	}
	// Branch alone keeps today's behaviour.
	if sel, err := knVersionFromRequest(versionReq(map[string]any{"branch": "feature"}, nil)); sel != nil || err != nil {
		t.Fatalf("branch alone: %v, %v", sel, err)
	}
}

func TestWithKnVersionPassesTheSelectorDown(t *testing.T) {
	var got *knversion.Selector
	h := withKnVersion(func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		got = knversion.FromContext(ctx)
		return mcp.NewToolResultText("ok"), nil
	})
	res, _ := h(context.Background(), versionReq(map[string]any{"kn_version": "3"}, nil))
	if res.IsError || got.Version() != "3" {
		t.Fatalf("selector = %v, result error = %v", got, res.IsError)
	}
	res, _ = h(context.Background(), versionReq(map[string]any{"draft_revision": 3}, nil))
	if !res.IsError {
		t.Fatal("a draft_revision without draft must be refused")
	}
}

func mismatchingHandler(calls *int) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		*calls++
		if *calls == 1 {
			knversion.FromContext(ctx).Observe(http.StatusConflict, http.Header{},
				[]byte(`{"error_code":"`+knversion.CodeDraftRevisionMismatch+`"}`))
			return mcp.NewToolResultError("draft moved"), nil
		}
		return mcp.NewToolResultText("ok"), nil
	}
}

func TestWithKnVersionRetriesOnceOnDraftRevisionMismatch(t *testing.T) {
	calls := 0
	h := withKnVersion(mismatchingHandler(&calls))
	res, _ := h(context.Background(), versionReq(map[string]any{"kn_version": "draft"}, nil))
	if calls != 2 || res.IsError {
		t.Fatalf("calls = %d, error = %v; want one retry that succeeds", calls, res.IsError)
	}
}

func TestWithKnVersionDoesNotRetryWhenTheCallerFixedTheRevision(t *testing.T) {
	calls := 0
	h := withKnVersion(mismatchingHandler(&calls))
	res, _ := h(context.Background(), versionReq(map[string]any{"kn_version": "draft", "draft_revision": 2}, nil))
	if calls != 1 || !res.IsError || !strings.Contains(knvResultText(res), "draft moved") {
		t.Fatalf("calls = %d, error = %v", calls, res.IsError)
	}
}

func TestWithKnVersionRetriesAtMostOnce(t *testing.T) {
	calls := 0
	h := withKnVersion(func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls++
		knversion.FromContext(ctx).Observe(http.StatusConflict, http.Header{},
			[]byte(knversion.CodeDraftRevisionMismatch))
		return mcp.NewToolResultError("draft moved"), nil
	})
	h(context.Background(), versionReq(map[string]any{"kn_version": "draft"}, nil))
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestOfferKnVersionOnlyOnNetworkTools(t *testing.T) {
	with := offerKnVersion([]byte(`{"type":"object","properties":{"kn_id":{"type":"string"}}}`))
	if !hasKnVersionProperty(with) {
		t.Fatal("a tool with kn_id must offer kn_version")
	}
	without := offerKnVersion([]byte(`{"type":"object","properties":{"q":{"type":"string"}}}`))
	if hasKnVersionProperty(without) {
		t.Fatal("a tool without kn_id must not offer kn_version")
	}
}

func knvResultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}
