// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"testing"
)

func TestStableBusinessValuesIgnoreNumericPresentationWithoutLosingPrecision(t *testing.T) {
	first, err := canonicalBusinessValue(map[string]any{"amount": json.Number("25.50"), "identity": json.Number("18446744073709551615")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := canonicalBusinessValue(map[string]any{"identity": json.Number("18446744073709551615"), "amount": json.Number("2.55e1")})
	if err != nil || string(first) != string(second) {
		t.Fatal("numeric formatting changed business identity", err)
	}
	third, _ := canonicalBusinessValue(map[string]any{"identity": json.Number("18446744073709551614"), "amount": json.Number("25.5")})
	if string(third) == string(first) {
		t.Fatal("large integer precision lost")
	}
	numeric, _ := canonicalBusinessValue(json.Number("1"))
	text, _ := canonicalBusinessValue("1")
	if string(numeric) == string(text) {
		t.Fatal("value types conflated")
	}
}
