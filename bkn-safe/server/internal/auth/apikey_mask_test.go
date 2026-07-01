// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package auth

import "testing"

func TestMaskKey(t *testing.T) {
	// prefix + first 4 of key id + "****" + last 4 of secret
	if got, want := maskKey("2882bb603277", "BSxTZHTBiJXk4QoUBc0u17zSWua"), "bak_2882****SWua"; got != want {
		t.Fatalf("maskKey = %q, want %q", got, want)
	}
}

func TestMaskFromKeyID(t *testing.T) {
	// fallback for legacy rows: tail comes from the key id itself
	if got, want := MaskFromKeyID("2882bb603277"), "bak_2882****3277"; got != want {
		t.Fatalf("MaskFromKeyID = %q, want %q", got, want)
	}
}

func TestMask_ShortInputsNoPanic(t *testing.T) {
	// halves shorter than 4 must not panic or slice out of range
	_ = maskKey("ab", "cd")
	_ = MaskFromKeyID("x")
	_ = maskKey("", "")
}
