//go:build garble_compat

// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package jsoncodec

import (
	"bytes"
	"encoding/json"
)

func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

func MarshalString(v any) (string, error) {
	data, err := Marshal(v)
	return string(data), err
}

func Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
