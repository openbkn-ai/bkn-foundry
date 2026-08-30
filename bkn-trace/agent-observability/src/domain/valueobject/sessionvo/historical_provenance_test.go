// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import (
	"testing"
	"time"
)

func TestHistoricalProvenanceBuildRequestCanonicalizesFactsAndExplicitNetworks(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	facts := []OperationCallFact{
		{OperationID: "op-later", Attempt: 1, StartedAt: startedAt.Add(time.Second), ToolName: "run_sql", Input: mustInlinePayload(t, `{"kn_id":"network-b"}`)},
		{OperationID: "op-earlier", Attempt: 2, StartedAt: startedAt, ToolName: "run_sql", Input: mustInlinePayload(t, `{"knowledge_network_id":"network-a"}`)},
		{OperationID: "op-no-network", Attempt: 1, StartedAt: startedAt, ToolName: "run_sql", Input: mustInlinePayload(t, `{"query":"select 1"}`)},
	}

	request, err := NewHistoricalProvenanceBuildRequest("interaction-1", Owner{
		TenantID: "tenant-1",
	}, facts)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got, want := request.KnowledgeNetworkIDs, []string{"network-a", "network-b"}; !sameStrings(got, want) {
		t.Fatalf("knowledge network IDs = %#v, want %#v", got, want)
	}
	if len(request.Facts) != 3 || request.Facts[0].OperationID != "op-earlier" || request.Facts[1].OperationID != "op-no-network" || request.Facts[2].OperationID != "op-later" {
		t.Fatalf("facts were not canonically sorted: %#v", request.Facts)
	}
	if request.FactsHash == "" {
		t.Fatal("facts hash is required")
	}

	reversed := []OperationCallFact{facts[2], facts[0], facts[1]}
	second, err := NewHistoricalProvenanceBuildRequest("interaction-1", Owner{
		TenantID: "tenant-1",
	}, reversed)
	if err != nil {
		t.Fatalf("build reversed request: %v", err)
	}
	if second.FactsHash != request.FactsHash {
		t.Fatalf("facts hash changed with input ordering: %q != %q", second.FactsHash, request.FactsHash)
	}
}

func mustInlinePayload(t *testing.T, raw string) PayloadEnvelope {
	t.Helper()
	payload, err := InlineJSONPayload([]byte(raw))
	if err != nil {
		t.Fatalf("inline payload: %v", err)
	}
	return payload
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
