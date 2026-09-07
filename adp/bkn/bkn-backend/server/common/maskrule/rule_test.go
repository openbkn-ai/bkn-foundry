// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package maskrule

import (
	"encoding/json"
	"math"
	"testing"
)

func intPointer(value int) *int           { return &value }
func boolPointer(value bool) *bool        { return &value }
func floatPointer(value float64) *float64 { return &value }

func TestValidateAcceptsSupportedRulesAndBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		propertyType string
		rule         *Rule
	}{
		{name: "fixed unicode", propertyType: "string", rule: &Rule{Kind: KindFixed, Replacement: "保密🔒"}},
		{name: "partial bounds", propertyType: "keyword", rule: &Rule{Kind: KindPartial, KeepStart: intPointer(0), KeepEnd: intPointer(64), Replacement: "*"}},
		{name: "email hides domain", propertyType: "text", rule: &Rule{Kind: KindEmail, LocalKeepStart: intPointer(64), PreserveDomain: boolPointer(false), Replacement: "●"}},
		{name: "round decimal", propertyType: "decimal", rule: &Rule{Kind: KindRound, Step: floatPointer(1e-20)}},
		{name: "date to month", propertyType: "date", rule: &Rule{Kind: KindDateGranularity, Granularity: "month"}},
		{name: "time to hour", propertyType: "time", rule: &Rule{Kind: KindDateGranularity, Granularity: "hour"}},
		{name: "timestamp to day", propertyType: "timestamp", rule: &Rule{Kind: KindDateGranularity, Granularity: "day"}},
		{name: "optional rule", propertyType: "json", rule: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(test.propertyType, test.rule); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestValidateRejectsInvalidRules(t *testing.T) {
	tests := []struct {
		name         string
		propertyType string
		rule         *Rule
	}{
		{name: "unknown kind", propertyType: "string", rule: &Rule{Kind: "regex", Replacement: "*"}},
		{name: "fixed type mismatch", propertyType: "boolean", rule: &Rule{Kind: KindFixed, Replacement: "*"}},
		{name: "empty replacement", propertyType: "string", rule: &Rule{Kind: KindFixed}},
		{name: "replacement too long", propertyType: "string", rule: &Rule{Kind: KindFixed, Replacement: "123456789"}},
		{name: "replacement control", propertyType: "string", rule: &Rule{Kind: KindFixed, Replacement: "*\n"}},
		{name: "partial missing start", propertyType: "string", rule: &Rule{Kind: KindPartial, KeepEnd: intPointer(1), Replacement: "*"}},
		{name: "partial start overflow", propertyType: "string", rule: &Rule{Kind: KindPartial, KeepStart: intPointer(65), KeepEnd: intPointer(0), Replacement: "*"}},
		{name: "email missing preserve domain", propertyType: "string", rule: &Rule{Kind: KindEmail, LocalKeepStart: intPointer(1), Replacement: "*"}},
		{name: "round type mismatch", propertyType: "string", rule: &Rule{Kind: KindRound, Step: floatPointer(10)}},
		{name: "round zero", propertyType: "integer", rule: &Rule{Kind: KindRound, Step: floatPointer(0)}},
		{name: "round non finite", propertyType: "float", rule: &Rule{Kind: KindRound, Step: floatPointer(math.Inf(1))}},
		{name: "round cross kind replacement", propertyType: "float", rule: &Rule{Kind: KindRound, Step: floatPointer(1), Replacement: "*"}},
		{name: "date same precision", propertyType: "date", rule: &Rule{Kind: KindDateGranularity, Granularity: "day"}},
		{name: "unsupported masked type", propertyType: "vector", rule: &Rule{Kind: KindDateGranularity, Granularity: "day"}},
		{name: "cross kind field", propertyType: "string", rule: &Rule{Kind: KindFixed, Replacement: "*", KeepStart: intPointer(1)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(test.propertyType, test.rule); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestRuleUnmarshalJSONRejectsUnknownFields(t *testing.T) {
	inputs := []string{
		`{"kind":"fixed","replacement":"*","keep_strat":1}`,
		`{"kind":"round","step":1,"replacement":""}`,
	}
	for _, input := range inputs {
		var rule Rule
		if err := json.Unmarshal([]byte(input), &rule); err == nil {
			t.Fatalf("json.Unmarshal(%s) error = nil, want field error", input)
		}
	}
}
