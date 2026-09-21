// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

type intentCase struct {
	Query  string `json:"query"`
	Expect string `json:"expect"`
	None   bool   `json:"none"`
}

func loadIntentCases(t *testing.T) []intentCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/gateway_intents.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []intentCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	return file.Cases
}

func candidateNames(result gatewaySearchResult) []string {
	names := make([]string, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		names = append(names, candidate.Name)
	}
	return names
}

// Every case must land in the top 3 in both locales; how often the expected
// tool comes first is logged, not enforced, because the model reads all
// three candidates and their not_for lines before choosing.
func TestSearchFindsTheIntendedTool(t *testing.T) {
	cases := loadIntentCases(t)
	for _, locale := range []string{"zh-CN", "en-US"} {
		catalog := catalogForLocale(t, locale)
		targets, first := 0, 0
		for _, tc := range cases {
			result := catalog.search(context.Background(), tc.Query, searchDefaultLimit)
			names := candidateNames(result)
			switch {
			case tc.Expect != "":
				targets++
				if result.NoMatch || !slices.Contains(names, tc.Expect) {
					t.Errorf("%s %q: want %s in the top %d, got %v (no_match=%v)", locale, tc.Query, tc.Expect, searchDefaultLimit, names, result.NoMatch)
				} else if names[0] == tc.Expect {
					first++
				}
			case tc.None:
				if !result.NoMatch {
					t.Errorf("%s %q: want no match, got %v", locale, tc.Query, names)
				}
			}
		}
		t.Logf("%s: expected tool ranked first in %d of %d target cases", locale, first, targets)
	}
}

func TestSearchRanksANamedToolFirst(t *testing.T) {
	catalog := catalogForLocale(t, "zh-CN")
	for _, name := range catalog.order {
		result := catalog.search(context.Background(), "用 "+name+" 查一下", searchDefaultLimit)
		if len(result.Candidates) == 0 || result.Candidates[0].Name != name {
			t.Errorf("naming %s ranked %v", name, candidateNames(result))
		}
	}
}

func TestSearchWithoutAMatchListsEveryTarget(t *testing.T) {
	catalog := catalogForLocale(t, "zh-CN")
	result := catalog.search(context.Background(), "你好", 1)
	if !result.NoMatch || result.Matched != 0 || !slices.Equal(candidateNames(result), catalog.order) {
		t.Fatalf("no-match result = %+v, want every target in catalogue order", result)
	}
	for _, candidate := range result.Candidates {
		if candidate.Summary == "" || candidate.UseWhen != "" || candidate.Arguments != "" {
			t.Errorf("no-match entry %s should carry the summary only: %+v", candidate.Name, candidate)
		}
	}
}

func TestSearchHonoursTheLimit(t *testing.T) {
	catalog := catalogForLocale(t, "zh-CN")
	// Hits several targets at once: 对象类, 关系类, 子图, 行动, 逻辑属性.
	query := "对象类 关系类 子图 行动 逻辑属性 执行结果 执行历史 探索"
	for _, tc := range []struct{ limit, want int }{{0, searchDefaultLimit}, {1, 1}, {5, 5}, {9, searchMaxLimit}} {
		result := catalog.search(context.Background(), query, tc.limit)
		if len(result.Candidates) != tc.want || !result.Truncated || result.Matched <= tc.want {
			t.Errorf("limit %d: got %d candidates, matched %d, truncated %v", tc.limit, len(result.Candidates), result.Matched, result.Truncated)
		}
		for _, candidate := range result.Candidates {
			if candidate.UseWhen == "" || candidate.NotFor == "" || candidate.NextStep == "" || !strings.Contains(candidate.Arguments, "kn_id*") {
				t.Errorf("candidate %s is missing its card or signature: %+v", candidate.Name, candidate)
			}
		}
	}
}

// Search returns short cards, never schemas: five candidates must stay well
// below what one schema costs.
func TestSearchResultStaysSmall(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		catalog := catalogForLocale(t, locale)
		result := catalog.search(context.Background(), "对象类 关系类 子图 行动 逻辑属性 执行结果 执行历史 探索", searchMaxLimit)
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > 4000 {
			t.Errorf("%s: five candidates take %d bytes", locale, len(raw))
		}
		// The no-match listing names every target with its summary: the whole
		// catalogue in one short answer.
		noMatch, _ := json.Marshal(catalog.search(context.Background(), "hello", searchDefaultLimit))
		if len(noMatch) > 3500 {
			t.Errorf("%s: the no-match listing takes %d bytes", locale, len(noMatch))
		}
	}
}

func TestContainsTerm(t *testing.T) {
	for _, tc := range []struct {
		text, term string
		want       bool
	}{
		{"count orders", "count", true},
		{"account balance", "count", false},
		{"recent executions", "execution", true},
		{"run get_object_types now", "get_object_types", true},
		{"run get_object_types_v2", "get_object_types", false},
		{"写一段sql查询", "sql", true},
		{"查看对象类字段", "对象类", true},
		{"", "对象类", false},
		{"anything", "", false},
	} {
		if got := containsTerm(tc.text, tc.term); got != tc.want {
			t.Errorf("containsTerm(%q, %q) = %v, want %v", tc.text, tc.term, got, tc.want)
		}
	}
}

func TestFieldSignature(t *testing.T) {
	got := fieldSignature(json.RawMessage(`{"properties":{"limit":{},"kn_id":{},"at_id":{},"_x":{}},"required":["kn_id","at_id"]}`))
	if got != "kn_id*, at_id*, _x, limit" {
		t.Fatalf("signature = %q", got)
	}
	if fieldSignature(nil) != "" || fieldSignature(json.RawMessage(`{"type":"object"}`)) != "" {
		t.Fatal("a schema without properties should have no signature")
	}
}

// describe_native_tool must hand back exactly what the executor will check.
func TestDescribeReturnsTheExecutableSchemaAndATemplate(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		catalog := catalogForLocale(t, locale)
		for _, name := range catalog.order {
			description, err := catalog.describe(context.Background(), name, false)
			if err != nil {
				t.Fatalf("%s %s: %v", locale, name, err)
			}
			tool, _, _ := catalog.lookup(context.Background(), name)
			want, _ := executableSchema(tool.RawInputSchema)
			if string(description.ArgumentsSchema) != string(want) {
				t.Errorf("%s %s: arguments schema differs from the executable schema", locale, name)
			}
			if description.Description == "" || description.CallTemplate.Name != name || string(description.CallTemplate.Arguments) == "{}" {
				t.Errorf("%s %s: incomplete description %+v", locale, name, description)
			}
			if description.OutputSchema != nil {
				t.Errorf("%s %s: the output schema is sent by default", locale, name)
			}
			if tool.RawOutputSchema != nil && fieldSignature(tool.RawOutputSchema) != "" && description.OutputFields == "" {
				t.Errorf("%s %s: no output field hints", locale, name)
			}
			full, err := catalog.describe(context.Background(), name, true)
			if err != nil || len(full.OutputSchema) == 0 {
				t.Errorf("%s %s: include_output_schema did not add the output schema (%v)", locale, name, err)
			}
		}
	}
}

func TestDescribeRefusesWhatTheGatewayCannotRun(t *testing.T) {
	// An enterprise tool the licence does not cover is real but unreachable.
	withSocket(t, entitlement.FixedGate(licverify.EditionCommunity))
	mcptool.Register(extraTool("probe_context", "probe_context"))
	catalog := catalogForLocale(t, "zh-CN")
	refusal := func(name string) *gatewayRefusal {
		_, err := catalog.describe(context.Background(), name, false)
		var r *gatewayRefusal
		if !errors.As(err, &r) {
			t.Fatalf("%s: err = %v, want a refusal", name, err)
		}
		return r
	}
	if r := refusal(toolKeySearchSchema); r.Code != refusalPublishedDirectly {
		t.Errorf("search_schema: %+v, want published_directly", r)
	}
	if r := refusal(toolKeyExecuteNativeReadTool); r.Code != refusalPublishedDirectly {
		t.Errorf("execute_native_read_tool: %+v, want published_directly", r)
	}
	// An unknown name and a real tool the licence does not cover must look
	// alike, so the answer reveals nothing about what exists.
	unknown, hidden := refusal("no_such_tool"), refusal("probe_context")
	if unknown.Code != refusalUnknownTool || hidden.Code != refusalUnknownTool ||
		strings.ReplaceAll(unknown.Message, "no_such_tool", "X") != strings.ReplaceAll(hidden.Message, "probe_context", "X") {
		t.Errorf("unknown %+v and not admitted %+v differ", unknown, hidden)
	}
}

// A licensed enterprise tool is part of what the full profile offers, so the
// gateway reaches it too, described by its own title and description.
func TestGatewayReachesLicensedEnterpriseTools(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionEnterprise))
	mcptool.Register(extraTool("probe_context", "probe_context"))
	catalog := catalogForLocale(t, "zh-CN")
	result := catalog.search(context.Background(), "用 probe_context 查一下", searchDefaultLimit)
	if len(result.Candidates) == 0 || result.Candidates[0].Name != "probe_context" || result.Candidates[0].Summary != "enterprise probe" {
		t.Fatalf("search = %+v, want the enterprise tool with its description", result)
	}
	description, err := catalog.describe(context.Background(), "probe_context", false)
	if err != nil || description.Description != "enterprise probe" || string(description.CallTemplate.Arguments) != "{}" {
		t.Fatalf("describe = %+v, %v", description, err)
	}
}

func TestSearchNormalizationAndKeywordWeight(t *testing.T) {
	if got := normalizeSearchText("行动执行的状态 SQL"); got != "行动执行状态 sql" {
		t.Errorf("normalizeSearchText = %q", got)
	}
	for keyword, want := range map[string]int{"关联": 3, "字段关联": 4, "sql": 3, "relation type": 4, "有哪些关联": 4} {
		if got := keywordWeight(keyword); got != want {
			t.Errorf("keywordWeight(%q) = %d, want %d", keyword, got, want)
		}
	}
}
