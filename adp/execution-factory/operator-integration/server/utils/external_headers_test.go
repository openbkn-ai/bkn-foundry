// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package utils

import "testing"

func TestSanitizeThirdPartyHeaders(t *testing.T) {
	input := map[string]string{
		"Authorization":  "Bearer third-party-credential",
		"X-Api-Key":      "business-key",
		"x-account-id":   "internal-user",
		"X-Account-Type": "user",
		"user_id":        "internal-user",
		"BKN-Request-ID": "request-1",
		"traceparent":    "trace",
	}
	got := SanitizeThirdPartyHeaders(input)
	if len(got) != 2 || got["Authorization"] != input["Authorization"] || got["X-Api-Key"] != input["X-Api-Key"] {
		t.Fatalf("sanitized headers = %#v", got)
	}
	if len(input) != 7 {
		t.Fatalf("input map was mutated: %#v", input)
	}
}
