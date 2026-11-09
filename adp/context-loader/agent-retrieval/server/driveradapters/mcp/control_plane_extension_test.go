// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

func TestControlPlaneExtensionCanBootstrapWithoutInteraction(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionProfessional))
	called := false
	tool := extraTool("probe_eval_start", "probe_eval_start")
	tool.ControlPlane = true
	tool.MinEdition = licverify.EditionProfessional
	tool.Handle = func(context.Context, sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		called = true
		return sdk.NewToolResultText("started"), nil
	}
	mcptool.Register(tool)
	if isBusinessTool(tool.Name) || strings.Contains(string(extraInputSchema(tool)), "bkn_context") {
		t.Fatal("control tool requires lifecycle bootstrap")
	}
	reply := errorText(t, callTool(t, tool.Name, map[string]any{}))
	if !called || !strings.Contains(reply, "started") {
		t.Fatalf("reply=%s called=%v", reply, called)
	}
	info, err := BuildMCPInfo("http://localhost/api/agent-retrieval/v1/mcp/")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range info.Tools {
		if entry.Name == tool.Name {
			raw, _ := json.Marshal(entry.InputSchema)
			if strings.Contains(string(raw), "bkn_context") {
				t.Fatal("info schema disagrees with tools/list")
			}
		}
	}
}

func TestControlPlaneExtensionIsDirectlyPublishedAcrossProfiles(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionProfessional))
	tool := extraTool("probe_eval_start", "probe_eval_start")
	tool.ControlPlane = true
	tool.MinEdition = licverify.EditionProfessional
	mcptool.Register(tool)

	for _, profile := range []mcpProfile{fullProfile, compactProfile} {
		t.Run(profile.endpointPath, func(t *testing.T) {
			srv, builder := newMCPServerForProfile(nil, "en-US", defaultPTCServicePort, profile)
			if !slices.Contains(listedToolNames(t, srv), tool.Name) {
				t.Fatal("control tool missing from tools/list")
			}
			var info *MCPInfo
			var err error
			if profile.gateway {
				info, err = BuildCompactMCPInfoForLocale("http://localhost/mcp", "en-US")
			} else {
				info, err = BuildMCPInfoForLocale("http://localhost/mcp", "en-US")
			}
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range info.Tools {
				if entry.Name == tool.Name {
					found = true
				}
			}
			if !found {
				t.Fatal("control tool missing from MCP info")
			}
			if _, _, ok := newNativeCatalog(builder).lookup(context.Background(), tool.Name); ok {
				t.Fatal("control tool entered the business execution gateway")
			}
		})
	}
}

func TestControlPlaneExtensionRechecksRevokedLicense(t *testing.T) {
	gate := &mutableGate{ed: licverify.EditionProfessional}
	withSocket(t, gate)
	called := false
	tool := extraTool("probe_eval_start", "probe_eval_start")
	tool.ControlPlane = true
	tool.MinEdition = licverify.EditionProfessional
	tool.Handle = func(context.Context, sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		called = true
		return sdk.NewToolResultText("started"), nil
	}
	mcptool.Register(tool)
	srv := compactServer(t, "en-US")
	if !slices.Contains(listedToolNames(t, srv), tool.Name) {
		t.Fatal("licensed control tool was hidden")
	}
	gate.ed = licverify.EditionCommunity
	if slices.Contains(listedToolNames(t, srv), tool.Name) {
		t.Fatal("revoked control tool is still visible")
	}
	reply := errorText(t, rpc(t, srv, "tools/call", map[string]any{"name": tool.Name, "arguments": map[string]any{}}))
	if called || !strings.Contains(reply, "not found") {
		t.Fatalf("revoked tool executed or disclosed its presence: %s", reply)
	}
}
