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
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/licverify"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func catalogForLocale(t *testing.T, locale string) *nativeCatalog {
	t.Helper()
	_, b := newMCPServerForLocale(nil, locale)
	return newNativeCatalog(b)
}

// cardEligible reports whether a tool can be a gateway target at all, so may
// carry a gateway card: not published directly, not a gateway tool, not the
// lifecycle pair, and not the sandbox tools, which the builder does not
// assemble.
func cardEligible(name string) bool {
	_, published := compactProfile.published[name]
	_, gateway := gatewayTools[name]
	_, lifecycle := lifecycleToolNames[name]
	return !published && !gateway && !lifecycle
}

// The compact profile narrows loading, not capability: every assembled tool
// it does not publish directly is reachable through the gateway.
func TestCatalogReachesEveryToolNotPublishedDirectly(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		_, b := newMCPServerForLocale(nil, locale)
		catalog := newNativeCatalog(b)
		var want []string
		for _, p := range b.pending {
			if cardEligible(p.tool.Name) {
				want = append(want, p.tool.Name)
			}
		}
		if !slices.Equal(catalog.order, want) {
			t.Errorf("%s: catalogue = %v, want %v", locale, catalog.order, want)
		}
		for _, name := range []string{toolKeyRunCypher, toolKeyExecuteAction, toolKeySearchCapabilities, toolKeyExecuteTool, toolKeyGetSkillContent} {
			if _, _, ok := catalog.lookup(context.Background(), name); !ok {
				t.Errorf("%s: %s is not reachable through the gateway", locale, name)
			}
		}
	}
}

func TestLookupRefusesAnythingNotAdmitted(t *testing.T) {
	catalog := catalogForLocale(t, "zh-CN")
	for _, name := range []string{toolKeySearchSchema, toolKeySearchNativeTools, "bkn_start_interaction", "no_such_tool"} {
		if _, _, ok := catalog.lookup(context.Background(), name); ok {
			t.Errorf("%s resolved through the gateway catalogue", name)
		}
	}
}

func TestExecutableSchemaDropsGatewayFields(t *testing.T) {
	input := json.RawMessage(`{"type":"object","properties":{
		"kn_id":{"type":"string"},
		"limit":{"type":"integer","maximum":9007199254740993},
		"bkn_context":{"type":"object"},
		"response_format":{"type":"string"}},
		"required":["kn_id","bkn_context"]}`)
	got, err := executableSchema(input)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(got, &schema); err != nil {
		t.Fatal(err)
	}
	for _, field := range gatewayManagedFields {
		if _, present := schema.Properties[field]; present {
			t.Errorf("%s is still in the executable schema", field)
		}
	}
	if !slices.Equal(schema.Required, []string{"kn_id"}) {
		t.Errorf("required = %v, want [kn_id]", schema.Required)
	}
	if !strings.Contains(string(got), "9007199254740993") {
		t.Errorf("a wide integer bound was rewritten: %s", got)
	}
}

func TestEveryTargetHasACompilableExecutableSchema(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		catalog := catalogForLocale(t, locale)
		for _, name := range catalog.order {
			tool, _, _ := catalog.lookup(context.Background(), name)
			schema, err := executableSchema(tool.RawInputSchema)
			if err != nil {
				t.Errorf("%s %s: %v", locale, name, err)
				continue
			}
			if _, err := compileExecutableSchema(schema); err != nil {
				t.Errorf("%s %s: executable schema does not compile: %v", locale, name, err)
			}
			for _, field := range gatewayManagedFields {
				if strings.Contains(string(schema), `"`+field+`"`) {
					t.Errorf("%s %s: executable schema still mentions %s", locale, name, field)
				}
			}
		}
	}
}

// The gateway must see the same schema tools/list would show: a decorated
// target gains the paid parameter only while the licence covers it, and the
// change takes effect without rebuilding the catalogue.
func TestLookupFollowsTheLicence(t *testing.T) {
	gate := &mutableGate{ed: licverify.EditionCommunity}
	withSocket(t, gate)
	mcptool.Decorate(toolKeyGetObjectTypes, searchSchemaDecorator())
	catalog := catalogForLocale(t, "zh-CN")

	tool, _, ok := catalog.lookup(context.Background(), toolKeyGetObjectTypes)
	if !ok || strings.Contains(string(tool.RawInputSchema), "probe_depth") {
		t.Fatalf("unlicensed: resolved=%v, schema carries the paid parameter", ok)
	}
	gate.ed = licverify.EditionEnterprise
	tool, _, ok = catalog.lookup(context.Background(), toolKeyGetObjectTypes)
	if !ok || !strings.Contains(string(tool.RawInputSchema), "probe_depth") {
		t.Fatalf("licensed: resolved=%v, schema lacks the paid parameter", ok)
	}
}

// Every core tool the gateway can reach needs a complete card in every
// locale, and nothing else may carry one: a card for a tool the gateway cannot
// reach would send the model to a dead end.
func TestGatewayCardsCoverExactlyTheReachableTools(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		bundle := buildMCPLocaleBundle(locale)
		for name := range allToolMeta() {
			card := bundle.ToolMeta(name).Gateway
			if !cardEligible(name) {
				if card != nil {
					t.Errorf("%s: %s cannot be a gateway target but has a card", locale, name)
				}
				continue
			}
			if card == nil {
				t.Errorf("%s: %s has no gateway card", locale, name)
				continue
			}
			for field, value := range map[string]string{"summary": card.Summary, "use_when": card.UseWhen, "not_for": card.NotFor, "next_step": card.NextStep} {
				if strings.TrimSpace(value) == "" {
					t.Errorf("%s %s: %s is empty", locale, name, field)
				}
			}
			if len(card.Keywords) == 0 || len(card.ExampleArguments) == 0 {
				t.Errorf("%s %s: a card needs keywords and an example", locale, name)
			}
		}
	}
}

// Keywords stay bilingual on purpose, because a question may be asked in
// either language whatever the locale; the text a model reads may not.
func TestEnglishGatewayCopyIsTranslated(t *testing.T) {
	bundle := buildMCPLocaleBundle("en-US")
	for name := range allToolMeta() {
		card := bundle.ToolMeta(name).Gateway
		if card == nil {
			continue
		}
		for _, value := range []string{card.Summary, card.UseWhen, card.NotFor, card.NextStep, string(card.ExampleArguments)} {
			if strings.ContainsFunc(value, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
				t.Errorf("%s: untranslated gateway copy %q", name, value)
			}
		}
	}
}

// A card's example is what describe_native_tool hands a model as a call
// template, so it must pass the same validation the executor applies.
func TestGatewayExamplesPassTheExecutableSchemas(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		catalog := catalogForLocale(t, locale)
		bundle := buildMCPLocaleBundle(locale)
		for _, name := range catalog.order {
			tool, _, _ := catalog.lookup(context.Background(), name)
			raw, err := executableSchema(tool.RawInputSchema)
			if err != nil {
				t.Fatal(err)
			}
			schema, err := compileExecutableSchema(raw)
			if err != nil {
				t.Fatal(err)
			}
			card := bundle.ToolMeta(name).Gateway
			if card == nil {
				t.Errorf("%s %s: no card", locale, name)
				continue
			}
			example, err := jsonschema.UnmarshalJSON(bytes.NewReader(card.ExampleArguments))
			if err != nil {
				t.Errorf("%s %s: example is not JSON: %v", locale, name, err)
				continue
			}
			if err := schema.Validate(example); err != nil {
				t.Errorf("%s %s: example does not pass the executable schema: %v", locale, name, err)
			}
		}
	}
}

func TestLocalizedGatewayCardInheritsWhatItDoesNotRestate(t *testing.T) {
	base := &GatewayCard{Summary: "中文", Keywords: []string{"子图", "subgraph"}, ExampleArguments: json.RawMessage(`{"kn_id":"kn"}`)}
	got := localizeGatewayCard(base, &GatewayCard{Summary: "English"})
	if got.Summary != "English" || !slices.Equal(got.Keywords, base.Keywords) || string(got.ExampleArguments) != `{"kn_id":"kn"}` {
		t.Fatalf("overlay = %+v, want English text with the baseline keywords and example", got)
	}
	if base.Summary != "中文" {
		t.Fatalf("the shared baseline card was modified: %+v", base)
	}
	if got := localizeGatewayCard(nil, &GatewayCard{Summary: "only here"}); got.Summary != "only here" {
		t.Fatalf("overlay without a baseline card = %+v", got)
	}
}

// The gateway tools are ordinary business tools: complete metadata in every
// locale, a schema that compiles, bkn_context required like every managed
// tool, and neither response_format nor an output schema, since the compact
// profile publishes neither.
func TestGatewayToolsAreDefinedInEveryLocale(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		bundle := buildMCPLocaleBundle(locale)
		for name := range gatewayTools {
			meta := bundle.ToolMeta(name)
			if meta.Name != name || meta.Title == "" || meta.Description == "" || meta.Group == "" {
				t.Errorf("%s %s: incomplete metadata %+v", locale, name, meta)
			}
			input, output := tryLoadToolSchemas(bundle, name)
			if len(output) != 0 {
				t.Errorf("%s %s: declares an output schema", locale, name)
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if err := json.Unmarshal(input, &schema); err != nil {
				t.Fatalf("%s %s: %v", locale, name, err)
			}
			if _, ok := schema.Properties["bkn_context"]; !ok || !slices.Contains(schema.Required, "bkn_context") {
				t.Errorf("%s %s: bkn_context is not required", locale, name)
			}
			if _, ok := schema.Properties["response_format"]; ok {
				t.Errorf("%s %s: offers response_format", locale, name)
			}
			if _, err := compileExecutableSchema(input); err != nil {
				t.Errorf("%s %s: input schema does not compile: %v", locale, name, err)
			}
		}
	}
}

// The full profile publishes every native tool itself. A gateway tool in its
// catalogue would advertise a tool it cannot call, and the sandbox toolkit,
// rendered from that catalogue, would gain a stub for it.
func TestFullProfileLeavesTheGatewayOut(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		info, err := buildMCPInfoForLocale("", locale, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range info.Tools {
			if _, gateway := gatewayTools[tool.Name]; gateway {
				t.Errorf("%s: /mcp/info lists %s", locale, tool.Name)
			}
		}
		srv, _ := newMCPServerForLocale(nil, locale)
		for _, name := range listedToolNames(t, srv) {
			if _, gateway := gatewayTools[name]; gateway {
				t.Errorf("%s: tools/list on the full profile lists %s", locale, name)
			}
		}
	}
	toolkit, err := BuildPTCToolkit("", defaultPTCServicePort)
	if err != nil {
		t.Fatal(err)
	}
	for name := range gatewayTools {
		if strings.Contains(toolkit.Stub, name) || strings.Contains(toolkit.Digest, name) {
			t.Errorf("the sandbox toolkit mentions %s", name)
		}
	}
}
