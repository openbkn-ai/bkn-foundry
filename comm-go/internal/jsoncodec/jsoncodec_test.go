// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package jsoncodec

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMarshalAndUnmarshal(t *testing.T) {
	type response struct {
		Message  string `json:"message"`
		Count    int    `json:"count"`
		Optional string `json:"optional,omitempty"`
	}
	input := response{Message: "<script>&中文", Count: 42}
	data, err := Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"message":"<script>&中文","count":42}`
	if string(data) != want {
		t.Fatalf("Marshal() = %s, want %s", data, want)
	}
	text, err := MarshalString(input)
	if err != nil || text != want {
		t.Fatalf("MarshalString() = %q, %v; want %q", text, err, want)
	}
	var decoded response
	if err := Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, input) {
		t.Fatalf("Unmarshal() = %#v, want %#v", decoded, input)
	}
	if _, err := Marshal(make(chan int)); err == nil {
		t.Fatal("Marshal must reject unsupported values")
	}
	if err := Unmarshal([]byte(`{"count":`), &decoded); err == nil {
		t.Fatal("Unmarshal must reject malformed JSON")
	}
	var numbers map[string]json.Number
	if err := Unmarshal([]byte(`{"number":9007199254740993}`), &numbers); err != nil || numbers["number"].String() != "9007199254740993" {
		t.Fatalf("large number: %v, %v", numbers, err)
	}
}
