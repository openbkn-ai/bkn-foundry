package common

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeJSONPreservesLargeInteger(t *testing.T) {
	var target map[string]any
	if err := DecodeJSON(strings.NewReader(`{"value":110101199001152345}`), &target); err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}
	if got, ok := target["value"].(json.Number); !ok || got.String() != "110101199001152345" {
		t.Fatalf("value = %#v, want json.Number preserving literal", target["value"])
	}
}
