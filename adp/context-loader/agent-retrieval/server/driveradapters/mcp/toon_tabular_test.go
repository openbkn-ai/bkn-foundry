// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

// After Slim(summary), each data_property is a flat {name,type} object, so TOON
// renders the array as a compact table instead of expanding each property.
func TestSummaryDataPropertiesRenderAsToonTable(t *testing.T) {
	d := &interfaces.KnowledgeNetworkDetail{
		ID:   "kn-1",
		Name: "sales",
		ObjectTypes: []*interfaces.ObjectType{{
			ID:   "ot_order",
			Name: "order",
			DataProperties: []*interfaces.DataProperty{
				{Name: "amount", DisplayName: "金额", Type: "double", Comment: "订单金额", MappedField: map[string]any{"column": "amt"}},
				{Name: "status", DisplayName: "状态", Type: "string", Comment: "订单状态", MappedField: map[string]any{"column": "st"}},
			},
		}},
	}
	d.Slim(interfaces.DetailLevelSummary)

	_, body, err := rest.MarshalResponse(rest.FormatTOON, d)
	if err != nil {
		t.Fatalf("marshal toon: %v", err)
	}
	out := string(body)
	t.Logf("TOON output:\n%s", out)

	// tabular header lists the columns once: data_properties[#2]{...}
	if !strings.Contains(out, "data_properties[#2]{") {
		t.Errorf("expected tabular data_properties header, got:\n%s", out)
	}
	// heavy/dropped fields must be absent
	for _, banned := range []string{"mapped_field", "display_name", "金额", "订单金额"} {
		if strings.Contains(out, banned) {
			t.Errorf("summary TOON should not contain %q, got:\n%s", banned, out)
		}
	}
}
