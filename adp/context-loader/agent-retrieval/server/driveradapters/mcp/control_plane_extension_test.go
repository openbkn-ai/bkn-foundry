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

func TestHTTPProviderDisplayMetadataReachesMCPDiscovery(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionProfessional))
	manifest := []byte(`{"call_path":"/api/extension/call","tools":[{"name":"probe_eval_start","title":"发起评测","description":"Start an external evaluation run.","capability":"context_probe","min_edition":"professional","control_plane":true,"group":"evaluation","group_title":"质量评测","order":820,"input_schema":{"type":"object"}}]}`)
	tools, err := mcptool.LoadHTTPProviderManifest("https://provider.invalid/manifest", manifest, nil)
	if err != nil || len(tools) != 1 {
		t.Fatalf("load provider: %v", err)
	}
	mcptool.Register(tools[0])
	for _, profile := range []mcpProfile{fullProfile, compactProfile} {
		t.Run(profile.endpointPath, func(t *testing.T) {
			srv, _ := newMCPServerForProfile(nil, "zh-CN", defaultPTCServicePort, profile)
			registered, ok := srv.ListTools()[tools[0].Name]
			if !ok {
				t.Fatal("provider tool missing from tools/list")
			}
			listed := registered.Tool
			raw, err := json.Marshal(listed)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Title string         `json:"title"`
				Meta  map[string]any `json:"_meta"`
			}
			if json.Unmarshal(raw, &wire) != nil || wire.Title != "发起评测" ||
				wire.Meta[toolMetaKeyGroup] != "evaluation" || wire.Meta[toolMetaKeyGroupTitle] != "质量评测" || wire.Meta[toolMetaKeyOrder] != float64(820) {
				t.Fatalf("tools/list lost provider display metadata: %s", raw)
			}
			var info *MCPInfo
			if profile.gateway {
				info, err = BuildCompactMCPInfoForLocale("https://example.invalid/mcp", "zh-CN")
			} else {
				info, err = BuildMCPInfoForLocale("https://example.invalid/mcp", "zh-CN")
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range info.Tools {
				if entry.Name == tools[0].Name {
					if entry.Title != wire.Title || entry.Group != "evaluation" || entry.GroupTitle != "质量评测" || entry.Order != 820 {
						t.Fatalf("MCP info disagrees with tools/list: %+v", entry)
					}
					return
				}
			}
			t.Fatal("provider tool missing from MCP info")
		})
	}
}

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
