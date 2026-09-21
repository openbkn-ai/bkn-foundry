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
	return newNativeCatalog(b, longTailTargets)
}

func TestLongTailTargetsAreAssembledAndOutsideTheOtherLists(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		catalog := catalogForLocale(t, locale)
		for _, name := range longTailTargets {
			if _, _, ok := catalog.lookup(context.Background(), name); !ok {
				t.Errorf("%s: long-tail target %s is not assembled", locale, name)
			}
		}
	}
	for _, name := range longTailTargets {
		if slices.Contains(compactProfileTools, name) {
			t.Errorf("%s is both published directly and reached through the gateway", name)
		}
		if slices.Contains(notInProfileTools, name) {
			t.Errorf("%s is both a gateway target and outside the profile", name)
		}
	}
}

// A typo in notInProfileTools would silently turn a known public name into an
// "unknown target" answer; every entry must be a tool the service defines.
func TestNotInProfileToolsArePublicToolNames(t *testing.T) {
	data, err := schemasFS.ReadFile("schemas/tools_meta.json")
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	for _, name := range notInProfileTools {
		if _, ok := meta[name]; !ok {
			t.Errorf("%s is not a tool this service defines", name)
		}
		if slices.Contains(compactProfileTools, name) {
			t.Errorf("%s is published on the compact profile and listed as outside it", name)
		}
	}
}

func TestLookupRefusesAnythingNotAdmitted(t *testing.T) {
	catalog := catalogForLocale(t, "zh-CN")
	for _, name := range []string{toolKeyRunSQL, toolKeySearchSchema, toolKeyRunCode, "no_such_tool"} {
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
		for _, name := range longTailTargets {
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

// Every name the gateway can answer for needs its copy in every locale, and
// nothing else may carry any: a card for a tool the gateway cannot reach would
// send the model to a dead end.
func TestGatewayCardsCoverExactlyTheGatewayNames(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		bundle := buildMCPLocaleBundle(locale)
		for name := range allToolMeta() {
			card := bundle.ToolMeta(name).Gateway
			text := map[string]string{}
			if card != nil {
				text = map[string]string{"summary": card.Summary, "use_when": card.UseWhen, "not_for": card.NotFor, "next_step": card.NextStep}
			}
			switch {
			case slices.Contains(longTailTargets, name):
				if card == nil {
					t.Errorf("%s: gateway target %s has no card", locale, name)
					continue
				}
				for field, value := range text {
					if strings.TrimSpace(value) == "" {
						t.Errorf("%s %s: %s is empty", locale, name, field)
					}
				}
				if card.Boundary != "" || len(card.Keywords) == 0 || len(card.ExampleArguments) == 0 {
					t.Errorf("%s %s: a target needs keywords and an example and no boundary", locale, name)
				}
			case slices.Contains(notInProfileTools, name):
				if card == nil || strings.TrimSpace(card.Boundary) == "" || len(card.Keywords) == 0 {
					t.Errorf("%s: %s is outside the profile without a boundary and keywords", locale, name)
					continue
				}
				for field, value := range text {
					if value != "" {
						t.Errorf("%s %s: a tool outside the profile has a %s", locale, name, field)
					}
				}
			default:
				if card != nil {
					t.Errorf("%s: %s is neither a gateway target nor outside the profile, but has a card", locale, name)
				}
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
		for _, value := range []string{card.Summary, card.UseWhen, card.NotFor, card.NextStep, card.Boundary, string(card.ExampleArguments)} {
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
		for _, name := range longTailTargets {
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
	if got := localizeGatewayCard(nil, &GatewayCard{Boundary: "only here"}); got.Boundary != "only here" {
		t.Fatalf("overlay without a baseline card = %+v", got)
	}
}
