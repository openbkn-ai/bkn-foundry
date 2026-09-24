package common

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestDecodePreciseJSONPreservesLargeInteger(t *testing.T) {
	var target map[string]any
	if err := DecodePreciseJSON(strings.NewReader(`{"value":110101199001152345}`), &target); err != nil {
		t.Fatalf("DecodePreciseJSON() error = %v", err)
	}
	if got, ok := target["value"].(json.Number); !ok || got.String() != "110101199001152345" {
		t.Fatalf("value = %#v, want json.Number preserving literal", target["value"])
	}
}

func TestJSONNumberConversions(t *testing.T) {
	integer, ok := NumberAsInt64(json.Number("9007199254740993"))
	if !ok || integer != 9007199254740993 {
		t.Fatalf("NumberAsInt64() = %d, %v", integer, ok)
	}
	integer, ok = NumberAsInt64(json.Number("42.0"))
	if !ok || integer != 42 {
		t.Fatalf("NumberAsInt64(decimal integer) = %d, %v", integer, ok)
	}
	if _, ok = NumberAsInt64(json.Number("42.5")); ok {
		t.Fatal("NumberAsInt64() accepted a fractional number")
	}
	floating, ok := NumberAsFloat64(json.Number("0.25"))
	if !ok || floating != 0.25 {
		t.Fatalf("NumberAsFloat64() = %v, %v", floating, ok)
	}
	if _, ok = NumberAsFloat64(json.Number("invalid")); ok {
		t.Fatal("NumberAsFloat64() accepted an invalid number")
	}
	if _, ok = NumberAsInt64(math.Inf(1)); ok {
		t.Fatal("NumberAsInt64() accepted infinity")
	}
}
