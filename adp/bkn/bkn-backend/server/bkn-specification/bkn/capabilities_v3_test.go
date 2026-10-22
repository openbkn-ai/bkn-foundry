// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const capabilitiesBlock = "capabilities:\n" +
	"  skills:\n    - id: skill_8f2c\n      name: 交期评估\n" +
	"  functions:\n    - box_id: box_supply\n      tool_id: tool_bom\n      box_name: 供应链计算\n      tool_name: BOM 展开\n" +
	"  mcp_tools:\n    - mcp_id: mcp_erp\n      mcp_name: ERP 查询\n      tool_name: query_po\n"

// withCapabilities writes a capabilities section into the 3.0 network.bkn.
func withCapabilities(section string) v3Edit {
	return v3Edit{fNet, "bkn_spec: \"3.0\"\n", "bkn_spec: \"3.0\"\n" + section}
}

func TestLoadV3_ReadsCapabilities(t *testing.T) {
	net, err := loadV3(t, withCapabilities(capabilitiesBlock))
	require.NoError(t, err)
	assert.Equal(t, &BknCapabilities{
		Skills:    []*BknCapabilitySkill{{ID: "skill_8f2c", Name: "交期评估"}},
		Functions: []*BknCapabilityFunction{{BoxID: "box_supply", ToolID: "tool_bom", BoxName: "供应链计算", ToolName: "BOM 展开"}},
		MCPTools:  []*BknCapabilityMCPTool{{MCPID: "mcp_erp", MCPName: "ERP 查询", ToolName: "query_po"}},
	}, net.Capabilities)

	// Either half of an identity is enough: ids match at home, names elsewhere.
	net, err = loadV3(t, withCapabilities("capabilities:\n  functions:\n    - box_name: 供应链计算\n      tool_name: BOM 展开\n"+
		"  mcp_tools:\n    - mcp_name: ERP 查询\n      tool_name: query_po\n  skills:\n    - id: 001\n"))
	require.NoError(t, err)
	assert.Equal(t, "001", net.Capabilities.Skills[0].ID, "values are read as written")

	net, err = loadV3(t, withCapabilities("capabilities:\n  skills: []\n"))
	require.NoError(t, err)
	assert.Nil(t, net.Capabilities, "a section without entries is none, as under 2.x")
}

func TestLoadV3_RefusesMalformedCapabilities(t *testing.T) {
	cases := []struct {
		name, section, want string
	}{
		{"not a mapping", "capabilities: [skill_8f2c]\n", "capabilities must be a mapping with skills, functions, mcp_tools"},
		{"unknown category", "capabilities:\n  function:\n    - box_id: b\n      tool_id: t\n", `capabilities category "function" is unknown; the categories are skills, functions, mcp_tools`},
		{"category that is not a list", "capabilities:\n  skills: 交期评估\n", "capabilities skills must be a list of entries"},
		{"category left empty", "capabilities:\n  skills:\n", "capabilities skills must be a list of entries"},
		{"entry that is not a mapping", "capabilities:\n  skills:\n    - 交期评估\n", "capabilities skills item 1 must be a mapping of id, name"},
		{"unknown field", "capabilities:\n  functions:\n    - box_id: b\n      tool-id: t\n", `capabilities functions item 1 field "tool-id" is unknown; the fields are box_id, tool_id, box_name, tool_name`},
		{"field that is a list", "capabilities:\n  skills:\n    - name: [交期评估]\n", "capabilities skills item 1 name must be a single value"},
		{"field left empty", "capabilities:\n  skills:\n    - id: s\n      name:\n", "capabilities skills item 1 name must be a single value"},
		{"skill without id or name", "capabilities:\n  skills:\n    - id: s\n    - {}\n", "capabilities skills item 2 has neither id nor name"},
		{"function with half of each identity", "capabilities:\n  functions:\n    - box_id: b\n      tool_name: t\n", "capabilities functions item 1 needs box_id and tool_id, or box_name and tool_name"},
		{"MCP tool without tool_name", "capabilities:\n  mcp_tools:\n    - mcp_id: m\n", "capabilities mcp_tools item 1 needs tool_name"},
		{"MCP tool without server", "capabilities:\n  mcp_tools:\n    - tool_name: query_po\n", "capabilities mcp_tools item 1 has neither mcp_id nor mcp_name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadV3(t, withCapabilities(c.section))
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			var specErr *SpecError
			assert.ErrorAs(t, err, &specErr)
		})
	}
}

// A 2.x package keeps the 2.x reading: a misspelt category is dropped, a type error drops the
// whole section, and the network still loads.
func TestLoadLegacy_KeepsCapabilitiesLenient(t *testing.T) {
	legacy := v3Edit{fNet, "bkn_spec: \"3.0\"\n", ""}
	net, err := loadV3(t, legacy, v3Edit{fNet, "tags: []\n", "tags: []\ncapabilities:\n  function:\n    - box_id: b\n      tool_id: t\n"})
	require.NoError(t, err)
	assert.Nil(t, net.Capabilities)

	net, err = loadV3(t, legacy, v3Edit{fNet, "tags: []\n", "tags: []\ncapabilities: [skill_8f2c]\n"})
	require.NoError(t, err)
	assert.Nil(t, net.Capabilities)
}

func TestWriteV3_CapabilitiesRoundTrip(t *testing.T) {
	net, err := loadV3(t, withCapabilities("capabilities:\n  skills:\n    - id: \"001\"\n      name: 交期评估\n"+
		"  functions:\n    - box_id: box_supply\n      tool_id: tool_bom\n      box_name: 供应链计算\n      tool_name: BOM 展开\n"))
	require.NoError(t, err)
	first := exportFiles(t, net)
	again, err := loadFiles(first)
	require.NoError(t, err, "the exporter's own output is valid 3.0")
	assert.Equal(t, first, exportFiles(t, again))
	assert.Equal(t, "001", again.Capabilities.Skills[0].ID)
}
