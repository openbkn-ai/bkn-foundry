// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kntools"
)

// Agents given owner_id and capability_id kept calling a function by its name.
// Each entry now carries the call that uses it, with the IDs already mapped:
// owner_id becomes toolbox_id and capability_id becomes tool_id or skill_id.
func TestSearchCapabilitiesCarriesTheCallToMake(t *testing.T) {
	view := withCapabilityCalls(&kntools.SearchCapabilitiesResp{
		Capabilities: []kntools.CapabilityEntry{
			{CapabilityType: interfaces.CapabilityTypeFunction, OwnerID: "box-1", CapabilityID: "fn-1", Name: "sellable"},
			{CapabilityType: interfaces.CapabilityTypeMCPTool, OwnerID: "srv-1", CapabilityID: "search_reports"},
			{CapabilityType: interfaces.CapabilityTypeSkill, CapabilityID: "skill-1"},
			{CapabilityType: "unknown", CapabilityID: "x"},
		},
		TotalMatched: 4,
	}, "kn_demo")

	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Capabilities []struct {
			CapabilityID string `json:"capability_id"`
			Call         *struct {
				Tool      string         `json:"tool"`
				Arguments map[string]any `json:"arguments"`
			} `json:"call"`
		} `json:"capabilities"`
		TotalMatched int `json:"total_matched"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TotalMatched != 4 || len(decoded.Capabilities) != 4 {
		t.Fatalf("view dropped fields: %s", raw)
	}
	fn := decoded.Capabilities[0].Call
	if fn == nil || fn.Tool != toolKeyExecuteTool || fn.Arguments["kn_id"] != "kn_demo" ||
		fn.Arguments["toolbox_id"] != "box-1" || fn.Arguments["tool_id"] != "fn-1" {
		t.Errorf("function call = %+v", fn)
	}
	if _, ok := fn.Arguments["arguments"].(map[string]any); !ok {
		t.Errorf("function call has no arguments object to fill: %+v", fn)
	}
	if mcpTool := decoded.Capabilities[1].Call; mcpTool == nil || mcpTool.Tool != toolKeyExecuteTool ||
		mcpTool.Arguments["toolbox_id"] != "srv-1" || mcpTool.Arguments["tool_id"] != "search_reports" {
		t.Errorf("mcp tool call = %+v", mcpTool)
	}
	if skill := decoded.Capabilities[2].Call; skill == nil || skill.Tool != toolKeyGetSkillContent ||
		skill.Arguments["skill_id"] != "skill-1" || skill.Arguments["kn_id"] != "kn_demo" {
		t.Errorf("skill call = %+v", skill)
	}
	if decoded.Capabilities[3].Call != nil {
		t.Errorf("an unknown kind got a call: %+v", decoded.Capabilities[3].Call)
	}
}
