// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ledgervo

import "testing"

func TestCanonicalPayloadHashFixtures(t *testing.T) {
	fixtures := []struct {
		payload string
		want    string
	}{
		{` { "b" : 2, "a" : 1 } `, "43258cff783fe7036d8a43033f830adfc60ec037382473548ac742b888292777"},
		{`{"items":[3,2,1],"nested":{"z":2,"a":1}}`, "7f1b2afcbb4cec480f8e65ea7fb85338ce2571b5b10251c83f24bb03318d86a4"},
		{`{"number":1.5,"message":"\u4f60\u597d"}`, "b20ba0633e4f6cfc766715ff6e8d996f19602c268eac402951be732f26a9956a"},
		// Shared with context-loader's infra/bkntrace/canonical_hash_test.go, which hashes its
		// evidence the same way (#1711): struct-shaped keys out of order, HTML characters, an
		// integer above 2^53 and a float. Change both copies together.
		{`{"event_id":"evt-1711","payload":{"definition":{"zeta":"a<b>&c","alpha":9007199254740993,"mid":{"name":"n","code":"c"},"ratio":0.5}},"event_type":"ontology.schema.snapshot"}`, "42210712b1261e4939024f526fa34b419faf2058ab44afc75e8aae249a4ec4a6"},
	}
	for _, fixture := range fixtures {
		if got := CanonicalPayloadHash([]byte(fixture.payload)); got != fixture.want {
			t.Fatalf("CanonicalPayloadHash(%s) = %s, want %s", fixture.payload, got, fixture.want)
		}
	}
}
