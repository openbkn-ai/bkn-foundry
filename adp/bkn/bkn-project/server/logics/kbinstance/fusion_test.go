// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"reflect"
	"testing"
)

func int64ptr(value int64) *int64 { return &value }
func intptr(value int) *int       { return &value }
func TestFusionUsesEffectiveTimeThenConfirmedEvidenceStrengthThenRecordedTime(t *testing.T) {
	for _, domain := range []string{"contract", "patent", "maintenance"} {
		t.Run(domain, func(t *testing.T) {
			claims := []FusionClaim{{ID: "original", EntityID: domain, Property: "term", Value: "old", EffectiveFrom: int64ptr(10), Strength: intptr(9), RecordedAt: 100}, {ID: "later", EntityID: domain, Property: "term", Value: "new", EffectiveFrom: int64ptr(20), Strength: intptr(1), RecordedAt: 90}, {ID: "future", EntityID: domain, Property: "term", Value: "future", EffectiveFrom: int64ptr(200), Strength: intptr(10), RecordedAt: 110}}
			result, err := ResolvePropertyClaims(claims, 100, true)
			if err != nil || result.Value != "new" || !reflect.DeepEqual(result.Current, []string{"later"}) || !reflect.DeepEqual(result.Superseded, []string{"original"}) || !reflect.DeepEqual(result.Pending, []string{"future"}) {
				t.Fatal("wrong effective-time selection", result, err)
			}
			claims[0].EffectiveFrom = int64ptr(20)
			result, err = ResolvePropertyClaims(claims, 100, true)
			if err != nil || result.Value != "old" {
				t.Fatal("evidence strength was ignored", result, err)
			}
			claims[1].Strength = intptr(9)
			claims[1].RecordedAt = 150
			result, err = ResolvePropertyClaims(claims, 100, true)
			if err != nil || result.Value != "new" {
				t.Fatal("recorded time was ignored", result, err)
			}
		})
	}
}
func TestFusionDoesNotGuessMissingPriorityOrBreakATieByRecordID(t *testing.T) {
	for _, scenario := range []string{"missing_time", "missing_strength", "equal_priority"} {
		t.Run(scenario, func(t *testing.T) {
			claims := []FusionClaim{{ID: "a", EntityID: "e", Property: "p", Value: "A", EffectiveFrom: int64ptr(10), Strength: intptr(1), RecordedAt: 20}, {ID: "z", EntityID: "e", Property: "p", Value: "B", EffectiveFrom: int64ptr(10), Strength: intptr(1), RecordedAt: 20}}
			if scenario == "missing_time" {
				claims[0].EffectiveFrom = nil
			}
			if scenario == "missing_strength" {
				claims[0].Strength = nil
			}
			result, err := ResolvePropertyClaims(claims, 100, true)
			if err != nil || result.Value != nil || len(result.Current) != 0 || len(result.Pending) != 2 || result.Reason == "" {
				t.Fatal("ambiguous source was silently made current", result, err)
			}
		})
	}
}
func TestFusionPreservesIndependentFieldsAndDoesNotTreatMissingAsEmpty(t *testing.T) {
	result, err := ResolvePropertyClaims([]FusionClaim{{ID: "a", EntityID: "e", Property: "p", Value: "existing"}, {ID: "b", EntityID: "e", Property: "p", Value: nil}}, 100, false)
	if err != nil || result.Value != "existing" || !reflect.DeepEqual(result.Current, []string{"a"}) {
		t.Fatal("not-mentioned value erased an existing claim", result, err)
	}
	_, err = ResolvePropertyClaims([]FusionClaim{{ID: "a", EntityID: "e", Property: "p", Value: "A"}, {ID: "b", EntityID: "e", Property: "another", Value: "B"}}, 100, false)
	if err == nil {
		t.Fatal("unrelated fields were fused together")
	}
}
func TestExpiredClaimWithUnknownStartDoesNotBlockCurrentEvidence(t *testing.T) {
	claims := []FusionClaim{
		{ID: "expired", EntityID: "e", Property: "p", Value: "old", EffectiveTo: int64ptr(50)},
		{ID: "active", EntityID: "e", Property: "p", Value: "current", EffectiveFrom: int64ptr(60)},
	}
	result, err := ResolvePropertyClaims(claims, 100, true)
	if err != nil || result.Value != "current" || !reflect.DeepEqual(result.Current, []string{"active"}) {
		t.Fatal("expired evidence blocked an independently dated current value", result, err)
	}
}
