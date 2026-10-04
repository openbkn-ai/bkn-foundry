// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import (
	"testing"
)

func TestEvidenceSnapshotDetachesImmutableCompletion(t *testing.T) {
	completion := &EvidenceCompletion{Expectation: &EvidenceExpectation{Version: 1, Closed: true, Events: []ExpectedEvidenceEvent{{EventID: "original"}}}, OriginalDurability: DurabilityPending, OriginalObservedRefs: []string{"original-ref"}, OriginalPartialReasons: []string{"original-reason"}}
	copied, err := CopyEvidenceSnapshot(Interaction{}, nil, nil, []OperationCallFact{{EvidenceCompletion: completion}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	copied.CallFacts[0].EvidenceCompletion.Expectation.Events[0].EventID = "changed"
	copied.CallFacts[0].EvidenceCompletion.OriginalObservedRefs[0] = "changed"
	if completion.Expectation.Events[0].EventID != "original" || completion.OriginalObservedRefs[0] != "original-ref" {
		t.Fatal("snapshot shared mutable confirmation metadata with stored contract")
	}
}
func TestNormalizeTraceCallGaps(t *testing.T) {
	valid := "trace_call_unrecorded:execute_tool:host:request-1"
	result, err := NormalizeTraceCallGaps([]string{valid, valid})
	if err != nil || len(result) != 1 || result[0] != valid {
		t.Fatalf("canonical gap normalization: %v %v", result, err)
	}
	for _, reason := range []string{"unknown", "trace_call_unrecorded:arbitrary_function:request", "trace_call_unrecorded:execute_tool:space request", "trace_call_unrecorded:execute_tool:"} {
		if _, err := NormalizeTraceCallGaps([]string{reason}); err == nil {
			t.Fatalf("accepted unsupported gap %q", reason)
		}
	}
}
