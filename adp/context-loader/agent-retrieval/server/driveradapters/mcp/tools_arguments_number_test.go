// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestBindPreciseArgumentsPreservesDynamicNumber(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.RawArguments = json.RawMessage(`{"value":9223372036854775807}`)

	var target struct {
		Value any `json:"value"`
	}
	if err := bindPreciseArguments(req, &target); err != nil {
		t.Fatalf("bindPreciseArguments() error = %v", err)
	}
	if got, ok := target.Value.(json.Number); !ok || got.String() != "9223372036854775807" {
		t.Fatalf("value = %#v, want json.Number preserving literal", target.Value)
	}
}

func TestBindArgumentsUsesNormalNumberDecoding(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.RawArguments = json.RawMessage(`{"value":42}`)

	var target struct {
		Value any `json:"value"`
	}
	if err := bindArguments(req, &target); err != nil {
		t.Fatalf("bindArguments() error = %v", err)
	}
	if got, ok := target.Value.(float64); !ok || got != 42 {
		t.Fatalf("value = %#v, want float64(42)", target.Value)
	}
}
