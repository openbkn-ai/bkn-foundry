// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package maskrule

import (
	"encoding/json"
	"testing"
)

func pointer[T any](value T) *T { return &value }

func TestApplyStringRulesUseUnicodeAndAlwaysMask(t *testing.T) {
	tests := []struct {
		name  string
		rule  *Rule
		input any
		want  any
	}{
		{name: "fixed", rule: &Rule{Kind: KindFixed, Replacement: "保密"}, input: "secret", want: "保密"},
		{name: "partial unicode", rule: &Rule{Kind: KindPartial, KeepStart: pointer(1), KeepEnd: pointer(1), Replacement: "*"}, input: "张三丰", want: "张*丰"},
		{name: "partial short", rule: &Rule{Kind: KindPartial, KeepStart: pointer(64), KeepEnd: pointer(64), Replacement: "*"}, input: "A", want: "*"},
		{name: "partial empty", rule: &Rule{Kind: KindPartial, KeepStart: pointer(0), KeepEnd: pointer(0), Replacement: "*"}, input: "", want: "*"},
		{name: "email", rule: &Rule{Kind: KindEmail, LocalKeepStart: pointer(1), PreserveDomain: pointer(true), Replacement: "*"}, input: "alice@example.com", want: "a****@example.com"},
		{name: "email hidden domain", rule: &Rule{Kind: KindEmail, LocalKeepStart: pointer(1), PreserveDomain: pointer(false), Replacement: "*"}, input: "alice@example.com", want: "a****"},
		{name: "invalid email", rule: &Rule{Kind: KindEmail, LocalKeepStart: pointer(1), PreserveDomain: pointer(true), Replacement: "***"}, input: "not-an-email", want: "***"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Apply("string", test.rule, test.input)
			if err != nil || got != test.want {
				t.Fatalf("Apply() = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
}

func TestApplyRoundDateAndFailureSemantics(t *testing.T) {
	rounded, err := Apply("decimal", &Rule{Kind: KindRound, Step: pointer(1000.0)}, json.Number("123456"))
	if err != nil || rounded.(json.Number).String() != "123000" {
		t.Fatalf("round = %#v, %v", rounded, err)
	}
	negative, err := Apply("decimal", &Rule{Kind: KindRound, Step: pointer(0.1)}, json.Number("-1.21"))
	if err != nil || negative.(json.Number).String() != "-1.3" {
		t.Fatalf("negative round = %#v, %v", negative, err)
	}
	date, err := Apply("datetime", &Rule{Kind: KindDateGranularity, Granularity: "month"}, "2026-09-07T13:14:15+08:00")
	if err != nil || date != "2026-09-01T00:00:00+08:00" {
		t.Fatalf("date = %#v, %v", date, err)
	}
	nullValue, err := Apply("string", &Rule{Kind: KindFixed, Replacement: "*"}, nil)
	if err != nil || nullValue != nil {
		t.Fatalf("null = %#v, %v", nullValue, err)
	}
	if _, err := Apply("string", nil, "secret"); err == nil {
		t.Fatal("missing rule must fail closed")
	}
	if _, err := Apply("decimal", &Rule{Kind: KindRound, Step: pointer(1.0)}, "123"); err == nil {
		t.Fatal("wrong runtime type must fail closed")
	}
	if err := Validate("string", &Rule{Kind: KindFixed, Replacement: "\n"}); err == nil {
		t.Fatal("non-printable replacement must fail validation")
	}
	if err := Validate("string", &Rule{Kind: KindFixed, Replacement: "*", Step: pointer(1.0)}); err == nil {
		t.Fatal("kind-inapplicable fields must fail validation")
	}
}
