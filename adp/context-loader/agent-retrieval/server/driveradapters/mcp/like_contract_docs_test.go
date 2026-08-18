package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The like/regex value contract must be stated on every filter surface a caller reads.
// cmd/ptc-stub renders only top-level parameter descriptions into the sandbox stub, so a
// contract nested under condition.value or filters.items.op alone never reaches sandbox
// callers - each surface listed here has to carry it.
func TestLikeContractDocumentedOnEveryFilterSurface(t *testing.T) {
	cases := []struct {
		locale string
		tool   string
		paths  [][]string
		needle string
	}{
		{"zh-CN", "query_object_instance", [][]string{
			{"properties", "condition", "description"},
			{"properties", "filters", "description"},
			{"properties", "condition", "properties", "value", "description"},
			{"properties", "filters", "items", "properties", "op", "description"},
		}, "字面子串"},
		{"zh-CN", "query_instance_subgraph", [][]string{
			{"properties", "relation_type_paths", "description"},
		}, "字面子串"},
		{"en-US", "query_object_instance", [][]string{
			{"properties", "condition", "properties", "operation", "description"},
			{"properties", "condition", "properties", "value", "description"},
			{"properties", "filters", "description"},
			{"properties", "filters", "items", "properties", "op", "description"},
		}, "literal substring"},
		{"en-US", "query_instance_subgraph", [][]string{
			{"properties", "relation_type_paths", "description"},
		}, "literal substring"},
	}

	for _, c := range cases {
		bundle := loadMCPLocaleBundle(c.locale)
		in, _ := tryLoadToolSchemas(bundle, c.tool)
		if in == nil {
			t.Fatalf("%s/%s: no input schema", c.locale, c.tool)
		}
		var doc map[string]any
		if err := json.Unmarshal(in, &doc); err != nil {
			t.Fatalf("%s/%s: %v", c.locale, c.tool, err)
		}
		for _, path := range c.paths {
			cur := any(doc)
			for _, key := range path {
				m, ok := cur.(map[string]any)
				if !ok {
					t.Fatalf("%s/%s: %v not a map at %s", c.locale, c.tool, path, key)
				}
				cur = m[key]
			}
			s, _ := cur.(string)
			if !strings.Contains(s, c.needle) {
				t.Errorf("%s/%s %v: missing %q\ngot: %s", c.locale, c.tool, path, c.needle, s)
			}
		}
	}
}
