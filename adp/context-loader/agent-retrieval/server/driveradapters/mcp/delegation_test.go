// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestAnExecutionCredentialIsHeldToItsNetwork(t *testing.T) {
	convey.Convey("A tool call made with an execution credential", t, func() {
		delegated := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
			AccountID: "delegator", TokenInfo: &interfaces.TokenInfo{VisitorID: "delegator", DelegatedKnID: "kn-1"}})
		var seen *mcpsdk.CallToolRequest
		next := func(_ context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			seen = &req
			return mcpsdk.NewToolResultText("ran"), nil
		}
		call := func(ctx context.Context, name string, arguments any, raw string, header http.Header) bool {
			seen = nil
			req := mcpsdk.CallToolRequest{Header: header}
			req.Params.Name, req.Params.Arguments = name, arguments
			if raw != "" {
				req.Params.RawArguments = json.RawMessage(raw)
			}
			result, err := delegationScopeMiddleware()(next)(ctx, req)
			convey.So(err, convey.ShouldBeNil)
			return seen != nil && !result.IsError
		}

		convey.Convey("runs on its network, and one naming none is read as naming it", func() {
			convey.So(call(delegated, "search_schema", map[string]any{"kn_id": "kn-1", "query": "q"}, "", nil), convey.ShouldBeTrue)
			convey.So(call(delegated, "search_schema", map[string]any{"query": "q"}, "", nil), convey.ShouldBeTrue)
			convey.So(seen.Header.Get("X-Kn-ID"), convey.ShouldEqual, "kn-1")
		})
		convey.Convey("is refused on another network, however it names it", func() {
			convey.So(call(delegated, "search_schema", map[string]any{"kn_id": "kn-2"}, "", nil), convey.ShouldBeFalse)
			convey.So(call(delegated, "execute_native_tool", map[string]any{"name": "x", "arguments": map[string]any{"kn_id": "kn-2"}}, "", nil), convey.ShouldBeFalse)
			convey.So(call(delegated, "execute_native_tool", map[string]any{"arguments": `{"kn_id":"kn-2"}`}, "", nil), convey.ShouldBeFalse)
			convey.So(call(delegated, "execute_native_tool", nil, `{"arguments":{"kn_id":"kn-2"}}`, nil), convey.ShouldBeFalse)
			convey.So(call(delegated, "search_schema", map[string]any{"kn_id": 7}, "", nil), convey.ShouldBeFalse)
			convey.So(call(delegated, "search_schema", map[string]any{}, "", http.Header{"X-Kn-Id": {"kn-2"}}), convey.ShouldBeFalse)
		})
		convey.Convey("may not list the networks its delegator sees", func() {
			convey.So(call(delegated, toolKeyListKnowledgeNetworks, map[string]any{}, "", nil), convey.ShouldBeFalse)
		})
		convey.Convey("made with a sign-in passes unchanged", func() {
			signedIn := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
				AccountID: "user", TokenInfo: &interfaces.TokenInfo{VisitorID: "user"}})
			convey.So(call(signedIn, toolKeyListKnowledgeNetworks, map[string]any{}, "", nil), convey.ShouldBeTrue)
			convey.So(call(signedIn, "search_schema", map[string]any{"kn_id": "kn-2"}, "", nil), convey.ShouldBeTrue)
			convey.So(seen.Header, convey.ShouldBeNil)
		})
	})
}
