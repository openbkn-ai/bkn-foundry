// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cursorcodec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodecRoundTripAndAuthentication(t *testing.T) {
	codec, err := New([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := codec.Encode("cursor-v1", struct {
		Value string `json:"value"`
	}{Value: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(token, "secret") {
		t.Fatalf("opaque cursor exposes plaintext: %q", token)
	}
	var decoded struct {
		Value string `json:"value"`
	}
	if err := codec.Decode("cursor-v1", token, &decoded); err != nil || decoded.Value != "secret" {
		t.Fatalf("decode cursor: value=%q error=%v", decoded.Value, err)
	}
	if err := codec.Decode("cursor-v2", token, &decoded); err == nil {
		t.Fatal("cursor accepted a different version")
	}
	replacement := "A"
	if token[len(token)/2:len(token)/2+1] == replacement {
		replacement = "B"
	}
	tampered := token[:len(token)/2] + replacement + token[len(token)/2+1:]
	if err := codec.Decode("cursor-v1", tampered, &decoded); err == nil {
		t.Fatal("cursor accepted modified ciphertext")
	}
}

func TestCodecPreservesExactDynamicNumbers(t *testing.T) {
	codec, err := New([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := codec.Encode("cursor-v1", map[string]any{"id": json.Number("18446744073709551615")})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := codec.Decode("cursor-v1", token, &decoded); err != nil {
		t.Fatal(err)
	}
	number, ok := decoded["id"].(json.Number)
	if !ok || string(number) != "18446744073709551615" {
		t.Fatalf("cursor lost numeric precision: %#v", decoded)
	}
}
