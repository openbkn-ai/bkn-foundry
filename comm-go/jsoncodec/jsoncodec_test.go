// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package jsoncodec_test

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
)

func TestMarshalAndUnmarshal(t *testing.T) {
	type response struct {
		Message  string `json:"message"`
		Count    int    `json:"count"`
		Optional string `json:"optional,omitempty"`
	}
	input := response{Message: "<script>&中文", Count: 42}
	data, err := jsoncodec.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"message":"<script>&中文","count":42}`
	if string(data) != want {
		t.Fatalf("Marshal() = %s, want %s", data, want)
	}
	text, err := jsoncodec.MarshalString(input)
	if err != nil || text != want {
		t.Fatalf("MarshalString() = %q, %v; want %q", text, err, want)
	}
	var decoded response
	if err := jsoncodec.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, input) {
		t.Fatalf("Unmarshal() = %#v, want %#v", decoded, input)
	}
	invalid := struct {
		Count int
		Value chan int
	}{Count: 42}
	if data, err := jsoncodec.Marshal(invalid); err == nil || data != nil {
		t.Fatalf("Marshal(unsupported value) = %q, %v; want nil data and an error", data, err)
	}
	if err := jsoncodec.Unmarshal([]byte(`{"count":`), &decoded); err == nil {
		t.Fatal("Unmarshal must reject malformed JSON")
	}
	var numbers map[string]json.Number
	if err := jsoncodec.Unmarshal([]byte(`{"number":9007199254740993}`), &numbers); err != nil || numbers["number"].String() != "9007199254740993" {
		t.Fatalf("large number: %v, %v", numbers, err)
	}
}

func TestMarshalV2Defaults(t *testing.T) {
	type response struct {
		Enabled bool              `json:"enabled,omitempty"`
		Count   int               `json:"count,omitempty"`
		List    []string          `json:"list"`
		Map     map[string]string `json:"map"`
		Bytes   []byte            `json:"bytes"`
		Empty   struct{}          `json:"empty,omitempty"`
		Raw     json.RawMessage   `json:"raw"`
	}
	input := response{Raw: json.RawMessage(`{ "text": "\u0061" }`)}
	data, err := jsoncodec.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"enabled":false,"count":0,"list":[],"map":{},"bytes":"","raw":{"text":"a"}}`
	if string(data) != want {
		t.Fatalf("Marshal() = %q, want %q", data, want)
	}
}

func TestUnmarshalV2Defaults(t *testing.T) {
	response := struct {
		Count int    `json:"count"`
		Label string `json:"label"`
	}{Count: 4, Label: "original"}
	const input = `{"count":null,"LABEL":"different","unknown":true}`
	if err := jsoncodec.Unmarshal([]byte(input), &response); err != nil {
		t.Fatal(err)
	}
	if response.Count != 0 || response.Label != "original" {
		t.Errorf("Unmarshal() = %+v, want Count 0 and Label original", response)
	}

	for _, tc := range []struct {
		name  string
		input string
	}{
		{name: "duplicate name", input: `{"label":"first","label":"second"}`},
		{name: "invalid UTF-8", input: "{\"label\":\"\xff\"}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded map[string]string
			if err := jsoncodec.Unmarshal([]byte(tc.input), &decoded); err == nil {
				t.Fatalf("Unmarshal(%q) succeeded, want an error", tc.input)
			}
		})
	}
}

func TestUnmarshalPreservesNumbers(t *testing.T) {
	var response struct {
		ID     int64            `json:"id"`
		Values []map[string]any `json:"values"`
	}
	const input = `{"id":9007199254740993,"values":[{"integer":9007199254740993,"decimal":12.5000,"exponent":1e+1000,"string":"42","other":[true,null]}]}`
	if err := jsoncodec.Unmarshal([]byte(input), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != 9007199254740993 {
		t.Errorf("ID = %d, want 9007199254740993", response.ID)
	}
	want := []map[string]any{{
		"integer":  json.Number("9007199254740993"),
		"decimal":  json.Number("12.5000"),
		"exponent": json.Number("1e+1000"),
		"string":   "42",
		"other":    []any{true, nil},
	}}
	if !reflect.DeepEqual(response.Values, want) {
		t.Fatalf("Values = %#v, want %#v", response.Values, want)
	}
}

func TestUnmarshalDecodePreservesNumbers(t *testing.T) {
	dec := jsontext.NewDecoder(strings.NewReader("[-0,12.5000]\n1e+1000"))
	var first []any
	if err := jsoncodec.UnmarshalDecode(dec, &first); err != nil {
		t.Fatal(err)
	}
	if want := []any{json.Number("-0"), json.Number("12.5000")}; !reflect.DeepEqual(first, want) {
		t.Fatalf("first value = %#v, want %#v", first, want)
	}

	var next any
	if err := jsoncodec.UnmarshalDecode(dec, &next); err != nil || next != json.Number("1e+1000") {
		t.Fatalf("next value = %v (%T), error = %v", next, next, err)
	}
	if err := jsoncodec.UnmarshalDecode(dec, &next); !errors.Is(err, io.EOF) {
		t.Fatalf("UnmarshalDecode() at end of stream = %v, want EOF", err)
	}
}

func TestUnmarshalWithOptions(t *testing.T) {
	type label string
	custom := jsonv2.WithUnmarshalers(jsonv2.UnmarshalFunc(func(data []byte, dst *label) error {
		var value string
		if err := jsonv2.Unmarshal(data, &value); err != nil {
			return err
		}
		*dst = label(strings.ToUpper(value))
		return nil
	}))
	type response struct {
		Label label `json:"label"`
		Value any   `json:"value"`
	}
	decodeBytes := func(input string, dst any, opts ...jsonv2.Options) error {
		return jsoncodec.Unmarshal([]byte(input), dst, opts...)
	}
	decodeStream := func(input string, dst any, opts ...jsonv2.Options) error {
		dec := jsontext.NewDecoder(strings.NewReader(input), custom)
		return jsoncodec.UnmarshalDecode(dec, dst, opts...)
	}
	for _, tc := range []struct {
		name   string
		decode func(string, any, ...jsonv2.Options) error
		opts   []jsonv2.Options
		label  label
	}{
		{name: "byte options", decode: decodeBytes, opts: []jsonv2.Options{custom}, label: "TEST"},
		{name: "inherited decoder options", decode: decodeStream, label: "TEST"},
		{name: "explicit decoder override", decode: decodeStream, opts: []jsonv2.Options{jsonv2.WithUnmarshalers(nil)}, label: "test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dst response
			const input = `{"label":"test","value":9007199254740993}`
			if err := tc.decode(input, &dst, tc.opts...); err != nil {
				t.Fatal(err)
			}
			if dst.Label != tc.label || dst.Value != json.Number("9007199254740993") {
				t.Fatalf("decoded value = %#v, want label %q and exact JSON number", dst, tc.label)
			}

			strict := append([]jsonv2.Options{jsonv2.RejectUnknownMembers(true)}, tc.opts...)
			if err := tc.decode(`{"unknown":true}`, &dst, strict...); !errors.Is(err, jsonv2.ErrUnknownName) {
				t.Fatalf("unexpected error decoding unknown member: %v", err)
			}
		})
	}
}
