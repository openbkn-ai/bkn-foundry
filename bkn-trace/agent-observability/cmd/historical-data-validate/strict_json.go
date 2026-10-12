// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Check tokens before typed decoding: encoding/json otherwise silently accepts
// duplicate keys, including escaped spelling and keys inside RawMessage.
func strictJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := strictValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

func strictValue(decoder *json.Decoder, depth int) error {
	if depth > 128 {
		return errors.New("JSON nesting limit exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return errors.New("duplicate or invalid JSON key")
			}
			keys[key] = true
			if err := strictValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := strictValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func decodeInput(data []byte) (input, error) {
	if err := strictJSON(data); err != nil {
		return input{}, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return input{}, errors.New("input must be an object")
	}
	for key := range fields {
		if key != "kind" && key != "payload" && key != "broker_time" {
			return input{}, errors.New("unknown input field")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var candidate input
	if err := decoder.Decode(&candidate); err != nil {
		return input{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return input{}, errors.New("trailing input value")
	}
	return candidate, nil
}
