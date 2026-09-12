// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import "testing"

func TestLocalizedMCPHandlerUsesConfiguredSandboxPort(t *testing.T) {
	previous := buildInlinePTCToolkit
	t.Cleanup(func() { buildInlinePTCToolkit = previous })

	const configuredPort = 31080
	portsByLocale := map[string]int{}
	buildInlinePTCToolkit = func(port int, locale string) (*PTCToolkit, error) {
		portsByLocale[locale] = port
		return &PTCToolkit{}, nil
	}

	_ = newLocalizedMCPHandler(nil, configuredPort)

	for _, locale := range []string{defaultMCPLocale, "en-US"} {
		if got := portsByLocale[locale]; got != configuredPort {
			t.Fatalf("sandbox port for locale %q = %d, want %d", locale, got, configuredPort)
		}
	}
}
