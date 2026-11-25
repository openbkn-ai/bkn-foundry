package common

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBindJSONPreservesLargeInteger(t *testing.T) {
	var target struct {
		Value any `json:"value"`
	}
	if err := BindJSON(strings.NewReader(`{"value":110101199001152345}`), &target); err != nil {
		t.Fatalf("BindJSON() error = %v", err)
	}
	if got, ok := target.Value.(json.Number); !ok || got.String() != "110101199001152345" {
		t.Fatalf("value = %#v, want json.Number preserving literal", target.Value)
	}
}

func TestBindJSONUsesV2FieldMatching(t *testing.T) {
	var target struct {
		Value string `json:"value"`
	}
	if err := BindJSON(strings.NewReader(`{"value":"expected","VALUE":"ignored","extra":true}`), &target); err != nil {
		t.Fatal(err)
	}
	if target.Value != "expected" {
		t.Fatalf("value = %q, want %q", target.Value, "expected")
	}
	if err := BindJSON(strings.NewReader(`{"value":"first","value":"second"}`), &target); err == nil {
		t.Fatal("BindJSON() accepted a duplicate field")
	}
}
