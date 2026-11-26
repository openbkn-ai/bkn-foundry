// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
)

func TestCondCfgMarshalPreservesExplicitValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "absent"},
		{name: "empty string", value: "", want: `""`},
		{name: "empty array", value: []any{}, want: `[]`},
		{name: "empty object", value: map[string]any{}, want: `{}`},
		{name: "zero", value: 0, want: `0`},
		{name: "precise number", value: json.Number("9007199254740993"), want: `9007199254740993`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			condition := &CondCfg{Name: "name", Operation: "==", Value: tc.value}
			data, err := jsoncodec.Marshal(condition)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := jsoncodec.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if got := string(fields["value"]); got != tc.want {
				t.Fatalf("value = %q, want %q (JSON: %s)", got, tc.want, data)
			}
		})
	}
}
