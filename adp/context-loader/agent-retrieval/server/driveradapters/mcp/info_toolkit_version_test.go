// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import "testing"

// TestInfoReportsTheImageToolkitVersion is the assertion the field exists for.
//
// /mcp/info reports toolkit_version so an operator can compare it against the
// __toolkit_version__ baked into the sandbox image and tell whether the image
// has fallen behind. That only works if both sides are the same rendering: the
// hash covers the digest, the digest differs between the signature-carrying
// variant and the inline one, and reporting the wrong one yields a value that
// never matches - which reads as "out of sync" even right after make bkn-tools,
// and is worse than reporting nothing.
func TestInfoReportsTheImageToolkitVersion(t *testing.T) {
	want, err := ImageToolkitVersion()
	if err != nil {
		t.Fatalf("ImageToolkitVersion: %v", err)
	}

	info, err := BuildMCPInfoForLocale("http://example.invalid/mcp", defaultMCPLocale)
	if err != nil {
		t.Fatalf("BuildMCPInfoForLocale: %v", err)
	}
	if info.ToolkitVersion != want {
		t.Fatalf("toolkit_version = %q, want the image hash %q", info.ToolkitVersion, want)
	}
}

// TestImageToolkitVersionIsLocaleStable guards the other half. The image is
// built once, in the default locale; rendering per request language would make
// the answer depend on the caller's Accept-Language and break the comparison for
// everyone not asking in that language.
func TestImageToolkitVersionIsLocaleStable(t *testing.T) {
	want, err := ImageToolkitVersion()
	if err != nil {
		t.Fatalf("ImageToolkitVersion: %v", err)
	}
	for _, locale := range []string{defaultMCPLocale, "en-US"} {
		info, err := BuildMCPInfoForLocale("http://example.invalid/mcp", locale)
		if err != nil {
			t.Fatalf("BuildMCPInfoForLocale(%s): %v", locale, err)
		}
		if info.ToolkitVersion != want {
			t.Fatalf("locale %s reported %q, want %q", locale, info.ToolkitVersion, want)
		}
	}
}
