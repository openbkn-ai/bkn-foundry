// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"testing"
)

func TestReconciliationReportsThreeCountsAndConflictsWithoutMutatingSources(t *testing.T) {
	system := []ReconcileRecord{{Identity: "A", Values: map[string]any{"amount": json.Number("25.50")}}, {Identity: "B", Values: map[string]any{"amount": json.Number("40")}}}
	text := []ReconcileRecord{{Identity: "A", Values: map[string]any{"amount": json.Number("25.5")}}, {Identity: "C", Values: map[string]any{"amount": json.Number("5")}}}
	result, err := ReconcileSources(system, text, []string{"amount"})
	if err != nil || result.Both != 1 || result.SystemOnly != 1 || result.TextOnly != 1 || len(result.Conflicts) != 0 {
		t.Fatal("wrong reconciliation counts", result, err)
	}
	text[0].Values["amount"] = json.Number("30")
	result, err = ReconcileSources(system, text, []string{"amount"})
	if err != nil || len(result.Conflicts) != 1 || system[0].Values["amount"] != json.Number("25.50") {
		t.Fatal("mismatch was hidden or source changed", result, err)
	}
	text[0].Values["amount"] = nil
	result, err = ReconcileSources(system, text, []string{"amount"})
	if err != nil || len(result.Conflicts) != 0 {
		t.Fatal("not-mentioned text was treated as a conflicting null", result, err)
	}
}
func TestReconciliationRejectsDuplicateOrEmptyBusinessNumbers(t *testing.T) {
	for _, rows := range [][]ReconcileRecord{{{Identity: "A"}, {Identity: "A"}}, {{Identity: " "}}, {{Identity: nil}}} {
		if _, err := ReconcileSources(rows, nil, nil); err == nil {
			t.Fatal("unverified business number was accepted")
		}
	}
}

func TestReconciliationAcceptsOnlyScalarIntegerOrTextBusinessNumbers(t *testing.T) {
	for _, key := range []any{true, false, []any{"A"}, map[string]any{"number": "A"}, json.Number("1.5"), float64(0.25)} {
		if _, err := ReconcileSources([]ReconcileRecord{{Identity: key}}, nil, nil); err == nil {
			t.Fatalf("invalid business-number type accepted: %T", key)
		}
	}
	for _, key := range []any{"A", uint64(18446744073709551615), json.Number("12e2"), int64(-1)} {
		if _, err := ReconcileSources([]ReconcileRecord{{Identity: key}}, nil, nil); err != nil {
			t.Fatalf("valid business-number type rejected: %T: %v", key, err)
		}
	}
}
