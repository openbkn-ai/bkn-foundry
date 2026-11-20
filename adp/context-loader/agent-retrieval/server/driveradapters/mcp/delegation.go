// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

// delegationScopeMiddleware holds a call made with an execution credential to
// the one knowledge network the credential was issued for. Every kn_id the
// call names, at any depth of its arguments (a gateway tool nests the call it
// runs), and the X-Kn-ID header must be that network; a call that names none
// is read as naming it. Listing networks is refused: it would reach beyond it.
// Calls made with a sign-in or an AppKey pass unchanged.
func delegationScopeMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			network := delegatedNetwork(ctx)
			if network == "" {
				return next(ctx, req)
			}
			var raw any
			if len(req.Params.RawArguments) > 0 && json.Unmarshal(req.Params.RawArguments, &raw) != nil {
				raw = map[string]any{"kn_id": nil}
			}
			if req.Params.Name == toolKeyListKnowledgeNetworks || !onlyNetwork(req.Params.Arguments, network) || !onlyNetwork(raw, network) ||
				(req.Header != nil && req.Header.Get("X-Kn-ID") != "" && req.Header.Get("X-Kn-ID") != network) {
				return mcpsdk.NewToolResultError("This credential acts only on knowledge network " + network + "."), nil
			}
			if req.Header == nil {
				req.Header = http.Header{}
			}
			req.Header.Set("X-Kn-ID", network)
			return next(ctx, req)
		}
	}
}

// delegatedNetwork is the network an execution credential is bound to; empty
// for any other caller.
func delegatedNetwork(ctx context.Context) string {
	auth, ok := common.GetAccountAuthContextFromCtx(ctx)
	if !ok || auth == nil || auth.TokenInfo == nil {
		return ""
	}
	return auth.TokenInfo.DelegatedKnID
}

// onlyNetwork tells whether every kn_id in value is network.
func onlyNetwork(value any, network string) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if key == "kn_id" {
				if id, ok := item.(string); !ok || id != network {
					return false
				}
				continue
			}
			if !onlyNetwork(item, network) {
				return false
			}
		}
	case string:
		// Arguments a gateway tool takes as a JSON string are parsed by it: read
		// them the same way.
		var parsed any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			if _, object := parsed.(map[string]any); object {
				return onlyNetwork(parsed, network)
			}
		}
	case []any:
		for _, item := range v {
			if !onlyNetwork(item, network) {
				return false
			}
		}
	}
	return true
}
