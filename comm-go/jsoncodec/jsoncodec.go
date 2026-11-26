// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package jsoncodec encodes and decodes JSON with the Go standard library.
package jsoncodec

import (
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
)

var (
	numberUnmarshalers = jsonv2.UnmarshalFromFunc(func(dec *jsontext.Decoder, dst *any) error {
		if *dst == nil && dec.PeekKind() == '0' {
			*dst = jsonv1.Number("")
		}
		return errors.ErrUnsupported // Let JSON v2 decode into the selected type.
	})
	useNumber = jsonv2.WithUnmarshalers(numberUnmarshalers)
)

// Marshal encodes v using the JSON v2 defaults and any supplied options.
func Marshal(v any, opts ...jsonv2.Options) ([]byte, error) {
	data, err := jsonv2.Marshal(v, opts...)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// MarshalString is like Marshal but returns the JSON as a string.
func MarshalString(v any, opts ...jsonv2.Options) (string, error) {
	data, err := Marshal(v, opts...)
	return string(data), err
}

// Unmarshal decodes a JSON value using the supplied options.
// Unless handled by a custom unmarshaler, numbers decoded into nil any values use json.Number.
func Unmarshal(data []byte, v any, opts ...jsonv2.Options) error {
	return jsonv2.Unmarshal(data, v, unmarshalOptions(nil, opts))
}

// UnmarshalDecode decodes the next value from dec with the same number handling as Unmarshal.
// Supplied options override the decoder's options for this call.
func UnmarshalDecode(dec *jsontext.Decoder, v any, opts ...jsonv2.Options) error {
	inherited, _ := jsonv2.GetOption(dec.Options(), jsonv2.WithUnmarshalers)
	return jsonv2.UnmarshalDecode(dec, v, unmarshalOptions(inherited, opts))
}

func unmarshalOptions(custom *jsonv2.Unmarshalers, opts []jsonv2.Options) jsonv2.Options {
	if len(opts) == 0 && custom == nil {
		return useNumber
	}
	options := jsonv2.JoinOptions(opts...)
	if selected, ok := jsonv2.GetOption(options, jsonv2.WithUnmarshalers); ok {
		custom = selected
	}
	if custom == nil {
		return jsonv2.JoinOptions(options, useNumber)
	}
	return jsonv2.JoinOptions(options, jsonv2.WithUnmarshalers(jsonv2.JoinUnmarshalers(custom, numberUnmarshalers)))
}
