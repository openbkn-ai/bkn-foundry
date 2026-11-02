// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"testing"
)

func TestLinkingPrefersVerifiedBusinessNumbersAndNeverGuessesAmbiguousTerms(t *testing.T) {
	for _, domain := range []string{"contract", "patent", "maintenance"} {
		t.Run(domain, func(t *testing.T) {
			targets := []LinkTarget{{Identity: "A"}, {Identity: "B"}, {Identity: json.Number("1200")}}
			terms := []ConfirmedTermLink{
				{NetworkID: domain, Term: "direct", Target: "B", Decision: "confirmed"},
				{NetworkID: domain, Term: "ambiguous", Target: "A", Decision: "confirmed"},
				{NetworkID: domain, Term: "ambiguous", Target: "B", Decision: "confirmed"},
				{NetworkID: domain, Term: "alias", Target: "B", Decision: "confirmed"},
				{NetworkID: domain, Term: "rejected", Target: "A", Decision: "rejected"},
			}
			sources := []LinkSource{
				{ID: "exact", Identity: "A", Term: "direct"},
				{ID: "number", Identity: json.Number("12e2")},
				{ID: "alias", Term: "alias"},
				{ID: "ambiguous", Term: "ambiguous"},
				{ID: "unknown", Identity: "C"},
				{ID: "rejected", Term: "rejected"},
			}
			result, err := ResolveLinks(domain, sources, targets, terms)
			if err != nil || result.Eligible != 6 || len(result.Linked) != 3 || len(result.Pending) != 3 {
				t.Fatal("wrong link coverage", result, err)
			}
			if result.Linked[0].SourceID != "alias" || result.Linked[1].SourceID != "exact" || result.Linked[1].Target != "A" || result.Linked[1].Method != "business_number" {
				t.Fatal("confirmed alias overrode a verified number", result)
			}
			if result.Pending[0].Reason != "ambiguous_term" || result.Pending[1].Reason != "term_rejected" || result.Pending[2].Reason != "unmatched" {
				t.Fatal("review reasons were lost", result)
			}
		})
	}
}

func TestLinkingRejectsUnverifiedTargetSnapshotsAndForeignTermDecisions(t *testing.T) {
	cases := []struct {
		targets []LinkTarget
		terms   []ConfirmedTermLink
	}{
		{targets: []LinkTarget{{Identity: "A"}, {Identity: "A"}}},
		{targets: []LinkTarget{{Identity: " "}}},
		{targets: []LinkTarget{{Identity: "A"}}, terms: []ConfirmedTermLink{{NetworkID: "other", Term: "alias", Target: "A", Decision: "confirmed"}}},
		{targets: []LinkTarget{{Identity: "A"}}, terms: []ConfirmedTermLink{{NetworkID: "network", Term: "alias", Target: "A", Decision: "proposed"}}},
	}
	for _, scenario := range cases {
		if _, err := ResolveLinks("network", []LinkSource{{ID: "source", Identity: "A"}}, scenario.targets, scenario.terms); err == nil {
			t.Fatal("unverified linking context accepted")
		}
	}
}

func TestRemovedTermTargetRemainsUnmatchedAndCoverageDoesNotBecomePerfect(t *testing.T) {
	result, err := ResolveLinks("n", []LinkSource{{ID: "source", Term: "alias"}}, nil,
		[]ConfirmedTermLink{{NetworkID: "n", Term: "alias", Target: "removed", Decision: "confirmed"}})
	if err != nil || result.Eligible != 1 || len(result.Linked) != 0 || len(result.Pending) != 1 {
		t.Fatal("removed target produced a phantom edge", result, err)
	}
}

func TestRemovingOneAmbiguousTargetDoesNotResolveAHumanDecision(t *testing.T) {
	result, err := ResolveLinks("n", []LinkSource{{ID: "source", Term: "alias"}}, []LinkTarget{{Identity: "A"}},
		[]ConfirmedTermLink{{NetworkID: "n", Term: "alias", Target: "A", Decision: "confirmed"}, {NetworkID: "n", Term: "alias", Target: "removed", Decision: "confirmed"}})
	if err != nil || len(result.Linked) != 0 || len(result.Pending) != 1 || result.Pending[0].Reason != "ambiguous_term" {
		t.Fatal("target removal silently resolved an ambiguous term", result, err)
	}
}

func TestVerifiedBusinessNumberDoesNotReplaceTheSystemObjectsPrimaryKey(t *testing.T) {
	result, err := ResolveLinks("network", []LinkSource{{ID: "contract-mention", Identity: "CONTRACT-2026-001"}},
		[]LinkTarget{{Identity: "CONTRACT-2026-001", RecordID: json.Number("18446744073709551615")}}, nil)
	if err != nil || len(result.Linked) != 1 || result.Linked[0].Target != json.Number("18446744073709551615") {
		t.Fatal("business-number join replaced or rounded the original system key", result, err)
	}
	_, err = ResolveLinks("network", nil, []LinkTarget{{Identity: "A", RecordID: 1}, {Identity: "B", RecordID: json.Number("1.0")}}, nil)
	if err == nil {
		t.Fatal("a target snapshot with duplicate primary keys was accepted")
	}
}
