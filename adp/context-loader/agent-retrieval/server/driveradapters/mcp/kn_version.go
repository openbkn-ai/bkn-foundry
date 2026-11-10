// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/knversion"
)

// offerKnVersion adds kn_version and draft_revision to the input schema of a
// tool that addresses a knowledge network (one that has kn_id). Other schemas
// are returned unchanged.
func offerKnVersion(input json.RawMessage) json.RawMessage {
	var schema map[string]any
	if err := sonic.Unmarshal(input, &schema); err != nil {
		return input
	}
	properties, _ := schema["properties"].(map[string]any)
	if _, hasKn := properties["kn_id"]; !hasKn {
		return input
	}
	properties[knversion.QueryVersion] = map[string]any{
		"type":        "string",
		"description": "Version to read: running (default), draft or a number. Overrides X-Kn-Version.",
	}
	properties[knversion.QueryDraftRevision] = map[string]any{
		"type":        "integer",
		"description": "Draft revision to pin; only with kn_version=draft.",
	}
	raw, err := sonic.ConfigStd.Marshal(schema)
	if err != nil {
		return input
	}
	return raw
}

func hasKnVersionProperty(input json.RawMessage) bool {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(input, &schema); err != nil {
		return false
	}
	_, ok := schema.Properties[knversion.QueryVersion]
	return ok
}

// withKnVersion makes a tool read one knowledge-network version.
//
// The version comes from the tool arguments, else from the connection headers.
// It is carried on the context to the bkn-backend and ontology-query clients,
// which send it on every read and pin it from the first response. If the draft
// moves on during a call whose draft revision the caller did not fix, the whole
// call is repeated once against the new draft.
func withKnVersion(h mcptool.Handler) mcptool.Handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sel, err := knVersionFromRequest(req)
		if err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}
		if sel == nil {
			return h(ctx, req)
		}
		result, err := h(knversion.WithSelector(ctx, sel), req)
		if sel.RevisionMismatch() && !sel.RevisionFixed() {
			sel = sel.Renew()
			result, err = h(knversion.WithSelector(ctx, sel), req)
		}
		bkntrace.RecordKnVersion(ctx, sel.Label())
		return result, err
	}
}

// knVersionFromRequest returns nil when neither the arguments nor the headers
// ask for a version, so such calls run exactly as before.
func knVersionFromRequest(req mcp.CallToolRequest) (*knversion.Selector, error) {
	args := req.GetArguments()
	version := argString(args[knversion.QueryVersion])
	revision := argString(args[knversion.QueryDraftRevision])
	if version == "" {
		// An argument wins over the header as a whole, so the header's draft
		// revision only goes with the header's own version.
		version = strings.TrimSpace(req.Header.Get(knversion.HeaderVersion))
		if revision == "" {
			revision = strings.TrimSpace(req.Header.Get(knversion.HeaderDraftRevision))
		}
	}
	if version == "" && revision == "" {
		return nil, nil
	}
	sel, err := knversion.Resolve(version, revision)
	if err != nil {
		return nil, err
	}
	if branch := argString(args["branch"]); branch != "" && sel.Version() != "" {
		if !branchMatchesVersion(branch, sel.Version()) {
			return nil, fmt.Errorf("branch %q and kn_version %q disagree: branch main is the running version and "+
				"branch draft is the draft; pass only one of them", branch, sel.Version())
		}
	}
	return sel, nil
}

// branchMatchesVersion maps the legacy branch onto kn_version: main is the
// running version, draft is the draft; no other branch has a version.
func branchMatchesVersion(branch, version string) bool {
	switch branch {
	case "main":
		return version == knversion.Running
	case knversion.Draft:
		return version == knversion.Draft
	}
	return false
}

func argString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		return fmt.Sprintf("%.0f", t)
	case json.Number:
		return t.String()
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}
