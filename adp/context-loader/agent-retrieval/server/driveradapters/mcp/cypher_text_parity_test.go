// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"strings"
	"testing"
)

// The model reads the Cypher subset's boundary in two places: run_cypher's own
// description, which the tool carries, and the server instructions, which frame
// the routing. #1792 widened the subset to accept OPTIONAL MATCH and updated the
// description, the PTC hints and the schema descriptions — every copy but the
// instructions, which went on listing it as refused in both locales for four
// months. A model reading both avoids a construct it is allowed to use, and the
// one it is steered away from is exactly the one a left-join count needs.
//
// These tests pin the two texts to each other. They cannot prove either matches
// the analyzer in bkn-backend, which is another module; they do catch a change
// that lands in one copy and not the other, which is what happened.

// cypherRefusals are the constructs the subset refuses, as both texts must name
// them. Written out rather than parsed: a list derived from the text it checks
// agrees with itself by construction.
var cypherRefusals = map[string][]string{
	"zh-CN": {"WITH", "UNION", "变长路径", "XOR", "返回整个节点", "函数调用与算术", "关系变量"},
	"en-US": {"WITH", "UNION", "variable-length paths", "XOR", "returning a whole node", "function calls and arithmetic", "relationship variables"},
}

// cypherAccepted are constructs the subset accepts that a text might wrongly
// call refused. OPTIONAL MATCH is the one that went wrong; the others are here
// so the guard is about the boundary, not about one incident.
var cypherAccepted = map[string][]string{
	"zh-CN": {"OPTIONAL MATCH", "DISTINCT", "ORDER BY"},
	"en-US": {"OPTIONAL MATCH", "DISTINCT", "ORDER BY"},
}

// refusalClause is the sentence naming what the subset refuses, taken from the
// bullet that routes multi-hop work to run_cypher. The instructions say "not
// supported" elsewhere too, so the clause is found on that one line rather
// than anywhere in the document.
func refusalClause(t *testing.T, locale, text string) string {
	t.Helper()
	lead := map[string]string{"zh-CN": "不支持", "en-US": "Unsupported:"}[locale]
	// The routing bullet, not the aggregation one: that line also names
	// run_cypher, and says query_object_instance cannot aggregate.
	anchor := map[string]string{"zh-CN": "run_cypher（只读 Cypher）", "en-US": "Use run_cypher for multi-hop queries"}[locale]
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, anchor) {
			continue
		}
		i := strings.Index(line, lead)
		if i < 0 {
			continue
		}
		rest := line[i+len(lead):]
		for _, stop := range []string{"；", "; "} {
			if j := strings.Index(rest, stop); j >= 0 {
				rest = rest[:j]
			}
		}
		return rest
	}
	t.Fatalf("%s: no run_cypher refusal clause found", locale)
	return ""
}

func TestServerInstructionsAndRunCypherAgreeOnTheSubsetBoundary(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			bundle := loadMCPLocaleBundle(locale)
			instructions := bundle.ServerInstructions()
			description := bundle.ToolMeta(toolKeyRunCypher).Description

			for _, construct := range cypherAccepted[locale] {
				clause := refusalClause(t, locale, instructions)
				if strings.Contains(clause, construct) {
					t.Errorf("the instructions refuse %q, which run_cypher accepts", construct)
				}
				if !strings.Contains(description, construct) {
					t.Errorf("run_cypher's description does not mention %q at all", construct)
				}
			}
			for _, construct := range cypherRefusals[locale] {
				if !strings.Contains(refusalClause(t, locale, instructions), construct) {
					t.Errorf("the instructions do not name %q as refused", construct)
				}
				if !strings.Contains(description, construct) {
					t.Errorf("run_cypher's description does not name %q as refused", construct)
				}
			}
		})
	}
}

// The PTC digest is a third copy, read by a model writing sandbox code, and it
// drifted the other way once already: it describes OPTIONAL MATCH correctly
// only because #1792 remembered it. Keep it in the same check.
func TestPTCHintsAgreeOnTheSubsetBoundary(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		hints := strings.Join(loadMCPLocaleBundle(locale).PTCHints(toolKeyRunCypher), " ")
		if hints == "" {
			t.Fatalf("%s: run_cypher has no PTC hints", locale)
		}
		if !strings.Contains(hints, "OPTIONAL MATCH") {
			t.Errorf("%s: the PTC hints do not mention OPTIONAL MATCH", locale)
		}
	}
}
