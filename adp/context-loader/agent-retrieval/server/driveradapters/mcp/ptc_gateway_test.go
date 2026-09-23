// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"slices"
	"testing"
)

// run_code and run_shell are registered through the builder like every other
// business tool, so the compact entry reaches them through its gateway without
// publishing them: their definitions are the largest on the full entry, and
// the whole point of the compact entry is not to load them up front.
func TestCompactReachesSandboxExecutionThroughTheGateway(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		listed := listedToolNames(t, compactServer(t, locale))
		catalog := catalogForLocale(t, locale)
		for _, name := range []string{toolKeyRunCode, toolKeyRunShell} {
			if slices.Contains(listed, name) {
				t.Errorf("%s: %s is published on the compact entry", locale, name)
			}
			if _, _, ok := catalog.lookup(context.Background(), name); !ok {
				t.Errorf("%s: %s is not reachable through the gateway", locale, name)
			}
			description, err := catalog.describe(context.Background(), name, false)
			if err != nil {
				t.Errorf("%s: describe %s: %v", locale, name, err)
				continue
			}
			// The rendered description of run_code is the tool digest, thousands
			// of characters long; the card's summary is what belongs here.
			tool, _, _ := catalog.lookup(context.Background(), name)
			rendered := len([]rune(tool.Description))
			if got := len([]rune(description.Description)); got == 0 || got >= rendered {
				t.Errorf("%s %s: description is %d runes, the rendered one is %d", locale, name, got, rendered)
			}
		}
	}
}

// The full entry keeps publishing them: only the compact entry narrows.
func TestFullProfileStillPublishesSandboxExecution(t *testing.T) {
	full, _ := newMCPServerForLocale(nil, "zh-CN")
	listed := listedToolNames(t, full)
	for _, name := range []string{toolKeyRunCode, toolKeyRunShell} {
		if !slices.Contains(listed, name) {
			t.Errorf("%s disappeared from the full entry", name)
		}
	}
}
