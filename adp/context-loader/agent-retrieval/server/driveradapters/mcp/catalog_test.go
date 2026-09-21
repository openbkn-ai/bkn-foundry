// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/licverify"
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
