// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

// Canonical business identity preserves scalar types and exact numeric values.
// Typed nodes prevent a JSON string/object from colliding with number metadata.
// The result is for comparison and stable hashes, never a stored business value.
func canonicalBusinessValue(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalidBuild
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) != nil {
		return nil, ErrInvalidBuild
	}
	node, err := canonicalNode(decoded)
	if err != nil {
		return nil, err
	}
	return json.Marshal(node)
}
func canonicalNode(value any) (any, error) {
	switch v := value.(type) {
	case nil:
		return []any{"null"}, nil
	case string:
		return []any{"string", v}, nil
	case bool:
		return []any{"boolean", v}, nil
	case json.Number:
		literal := v.String()
		if len(literal) > 4096 {
			return nil, ErrInvalidBuild
		}
		if i := strings.IndexAny(literal, "eE"); i >= 0 {
			exponent, err := strconv.Atoi(literal[i+1:])
			if err != nil || exponent > 4096 || exponent < -4096 {
				return nil, ErrInvalidBuild
			}
		}
		number, ok := new(big.Rat).SetString(literal)
		if !ok {
			return nil, ErrInvalidBuild
		}
		return []any{"number", number.RatString()}, nil
	case []any:
		values := make([]any, len(v))
		for i, item := range v {
			node, err := canonicalNode(item)
			if err != nil {
				return nil, err
			}
			values[i] = node
		}
		return []any{"array", values}, nil
	case map[string]any:
		fields := map[string]any{}
		for key, item := range v {
			node, err := canonicalNode(item)
			if err != nil {
				return nil, err
			}
			fields[key] = node
		}
		return []any{"object", fields}, nil
	default:
		return nil, ErrInvalidBuild
	}
}
