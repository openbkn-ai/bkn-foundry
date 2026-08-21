// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"testing"
)

// Downstream bodies are decoded with UseNumber (see drivenadapters.precisionJSON),
// so schema defaults reach pyLiteral as json.Number. Falling through to %v on a
// float64 used to emit 9.223372036854776e+18 into generated Python.
func TestPyLiteralKeepsJSONNumberDigits(t *testing.T) {
	cases := map[string]string{
		"9223372036854775807":  "9223372036854775807",
		"9223372036854775808":  "9223372036854775808",
		"18446744073709551615": "18446744073709551615",
		"-9223372036854775808": "-9223372036854775808",
		"42":                   "42",
		"1.5":                  "1.5",
	}
	for literal, want := range cases {
		if got := pyLiteral(json.Number(literal)); got != want {
			t.Errorf("pyLiteral(json.Number(%q)) = %s, want %s", literal, got, want)
		}
	}
}

func TestPyLiteralUnchangedForOtherTypes(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{nil, "None"},
		{true, "True"},
		{false, "False"},
		{"a'b", `'a\'b'`},
		{float64(42), "42"},
		{1.5, "1.5"},
	}
	for _, c := range cases {
		if got := pyLiteral(c.value); got != c.want {
			t.Errorf("pyLiteral(%#v) = %s, want %s", c.value, got, c.want)
		}
	}
}

// End to end over the schema decoder: a wide default must reach the generated
// Python as digits. Default decoding would otherwise produce float64 and render
// the rounded neighbour of 9223372036854775807.
func TestPtcParamsKeepsWideDefaults(t *testing.T) {
	raw := json.RawMessage(`{"properties":{"cursor":{"type":"integer","default":9223372036854775807},` +
		`"limit":{"type":"integer","default":10}},"required":[]}`)

	params := ptcParams(raw)
	got := map[string]string{}
	for _, p := range params {
		got[p.name] = pyLiteral(p.defVal)
	}
	if got["cursor"] != "9223372036854775807" {
		t.Errorf("cursor default = %s, want 9223372036854775807", got["cursor"])
	}
	if got["limit"] != "10" {
		t.Errorf("limit default = %s, want 10", got["limit"])
	}
}
