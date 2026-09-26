// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

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
	if r := refusal(toolKeyExecuteNativeTool); r.Code != refusalPublishedDirectly {
		t.Errorf("execute_native_tool: %+v, want published_directly", r)
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

// An answered search is worth a turn only if the model can act on it. When one
// candidate clearly wins, the search carries that candidate's arguments schema
// and call template, so the common two-hop search → describe → execute becomes
// one hop. These tests pin when that happens and, more importantly, that what
// it hands back is exactly what describe_native_tool would have said.

func TestSearchIsReadyOnAnUnambiguousHit(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		catalog := catalogForLocale(t, locale)
		result := catalog.search(context.Background(), "用 run_cypher 查一下", searchDefaultLimit)
		if result.Ready == nil {
			t.Fatalf("%s: naming one tool must come back ready, got %v", locale, candidateNames(result))
		}
		if result.Ready.Name != toolKeyRunCypher {
			t.Fatalf("%s: ready on %s", locale, result.Ready.Name)
		}
		// Byte-identical to describe_native_tool, or a caller that trusts the
		// shortcut builds a call the executor then judges by another schema.
		described, err := catalog.describe(context.Background(), toolKeyRunCypher, false)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(result.Ready.ArgumentsSchema, described.ArgumentsSchema) {
			t.Errorf("%s: the ready schema differs from describe_native_tool's", locale)
		}
		gotTemplate, _ := json.Marshal(result.Ready.CallTemplate)
		wantTemplate, _ := json.Marshal(described.CallTemplate)
		if !bytes.Equal(gotTemplate, wantTemplate) {
			t.Errorf("%s: call template = %s, describe_native_tool says %s", locale, gotTemplate, wantTemplate)
		}
	}
}

// A ready call has to be runnable as handed over: the template it carries
// must pass the schema it carries.
func TestReadyCallTemplatesPassTheirOwnSchema(t *testing.T) {
	catalog := catalogForLocale(t, "zh-CN")
	ready := 0
	for _, name := range catalog.order {
		result := catalog.search(context.Background(), "用 "+name+" 查一下", searchDefaultLimit)
		if result.Ready == nil {
			continue
		}
		ready++
		schema, err := compileExecutableSchema(result.Ready.ArgumentsSchema)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		template, err := jsonschema.UnmarshalJSON(bytes.NewReader(result.Ready.CallTemplate.Arguments))
		if err != nil {
			t.Fatalf("%s: template is not JSON: %v", name, err)
		}
		if err := schema.Validate(template); err != nil {
			t.Errorf("%s: the ready template does not pass the ready schema: %v", name, err)
		}
	}
	if ready == 0 {
		t.Fatal("no target came back ready: the shortcut is dead code")
	}
	t.Logf("%d of %d targets come back ready when named outright", ready, len(catalog.order))
}

// Two candidates that score the same leave the model a choice to make, and
// attaching one schema would weigh that choice by sort order alone.
func TestSearchIsNotReadyOnATie(t *testing.T) {
	catalog := catalogForLocale(t, "zh-CN")
	result := catalog.search(context.Background(), "实例", searchDefaultLimit)
	if len(result.Candidates) < 2 {
		t.Skipf("no tie to test: %v", candidateNames(result))
	}
	scores := map[string]bool{}
	for _, name := range []string{result.Candidates[0].Name, result.Candidates[1].Name} {
		scores[name] = true
	}
	// Rebuild the scores the search used, to assert this really is a tie.
	first, second := scoreOf(t, catalog, "实例", result.Candidates[0].Name), scoreOf(t, catalog, "实例", result.Candidates[1].Name)
	if first != second {
		t.Skipf("not a tie: %s=%d %s=%d", result.Candidates[0].Name, first, result.Candidates[1].Name, second)
	}
	if result.Ready != nil {
		t.Fatalf("a tie came back ready on %s", result.Ready.Name)
	}
}

func scoreOf(t *testing.T, catalog *nativeCatalog, query, name string) int {
	t.Helper()
	tool, _, ok := catalog.lookup(context.Background(), name)
	if !ok {
		t.Fatalf("%s is not a target", name)
	}
	return matchScore(normalizeSearchText(query), catalog.targetMeta(name, tool))
}

// The schema is spent on every ready search, including the ones where the
// model would not have asked for it, so the largest schemas keep costing a
// describe_native_tool call rather than being carried to everyone.
func TestSearchIsNotReadyOnAnOversizedSchema(t *testing.T) {
	catalog := catalogForLocale(t, "zh-CN")
	oversized := 0
	for _, name := range catalog.order {
		described, err := catalog.describe(context.Background(), name, false)
		if err != nil {
			continue
		}
		if len(described.ArgumentsSchema) <= maxReadySchemaBytes {
			continue
		}
		oversized++
		result := catalog.search(context.Background(), "用 "+name+" 查一下", searchDefaultLimit)
		if result.Ready != nil {
			t.Errorf("%s has a %d-byte schema and still came back ready", name, len(described.ArgumentsSchema))
		}
	}
	if oversized == 0 {
		t.Skip("no target is over the budget")
	}
}

func TestSearchWithoutAMatchIsNeverReady(t *testing.T) {
	catalog := catalogForLocale(t, "zh-CN")
	result := catalog.search(context.Background(), "你好", searchDefaultLimit)
	if !result.NoMatch {
		t.Fatalf("expected no match, got %v", candidateNames(result))
	}
	if result.Ready != nil {
		t.Fatalf("a no-match answer came back ready on %s", result.Ready.Name)
	}
}
