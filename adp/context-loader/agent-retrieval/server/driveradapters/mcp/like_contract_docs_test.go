package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The complete LIKE pattern contract must be stated on every filter surface a caller reads.
// cmd/ptc-stub renders only top-level parameter descriptions into the sandbox stub, so a
// contract nested under condition.value or filters.items.op alone never reaches sandbox
// callers - each surface listed here has to carry it.
func TestLikeContractDocumentedOnEveryFilterSurface(t *testing.T) {
	cases := []struct {
		locale    string
		tool      string
		paths     [][]string
		fragments []string
	}{
		{"zh-CN", "query_object_instance", [][]string{
			{"properties", "condition", "description"},
			{"properties", "condition", "properties", "operation", "description"},
			{"properties", "filters", "description"},
			{"properties", "condition", "properties", "value", "description"},
			{"properties", "filters", "items", "properties", "op", "description"},
		}, []string{"完整 SQL LIKE 模式", "foo 只匹配完整值", "%foo% 匹配包含 foo", "_ 匹配一个字符", `\%、\_、\\ 分别匹配字面量 %、_、反斜杠`, "末尾孤立的反斜杠无效"}},
		{"zh-CN", "query_instance_subgraph", [][]string{
			{"properties", "relation_type_paths", "description"},
			{"properties", "relation_type_paths", "items", "properties", "object_types", "items", "properties", "condition", "properties", "operation", "description"},
			{"properties", "relation_type_paths", "items", "properties", "object_types", "items", "properties", "condition", "properties", "value", "description"},
		}, []string{"完整 SQL LIKE 模式", "foo 只匹配完整值", "%foo% 匹配包含 foo", "_ 匹配一个字符", `\%、\_、\\ 分别匹配字面量 %、_、反斜杠`, "末尾孤立的反斜杠无效"}},
		{"en-US", "query_object_instance", [][]string{
			{"properties", "condition", "properties", "operation", "description"},
			{"properties", "condition", "properties", "value", "description"},
			{"properties", "filters", "description"},
			{"properties", "filters", "items", "properties", "op", "description"},
		}, []string{"complete SQL LIKE pattern", "foo matches the whole value", "%foo% matches the literal substring foo", "_ matches one character", `\%`, `\_`, `\\`, "trailing escape is invalid"}},
		{"en-US", "query_instance_subgraph", [][]string{
			{"properties", "relation_type_paths", "description"},
			{"properties", "relation_type_paths", "items", "properties", "object_types", "items", "properties", "condition", "properties", "operation", "description"},
			{"properties", "relation_type_paths", "items", "properties", "object_types", "items", "properties", "condition", "properties", "value", "description"},
		}, []string{"complete SQL LIKE pattern", "foo matches the whole value", "%foo% matches the literal substring foo", "_ matches one character", `\%`, `\_`, `\\`, "trailing escape is invalid"}},
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
			for _, fragment := range c.fragments {
				if !strings.Contains(s, fragment) {
					t.Errorf("%s/%s %v: missing %q\ngot: %s", c.locale, c.tool, path, fragment, s)
				}
			}
			for _, stale := range []string{"不要自己拼 %", "不是 SQL LIKE 模式", "未转义的 % 会被拒"} {
				if strings.Contains(s, stale) {
					t.Errorf("%s/%s %v: outdated LIKE guidance %q", c.locale, c.tool, path, stale)
				}
			}
		}
	}

	zhInstructions := loadMCPLocaleBundle("zh-CN").ServerInstructions()
	for _, fragment := range []string{"完整 SQL LIKE 模式", "foo 只匹配完整值", "%foo% 匹配包含 foo", "_ 匹配一个字符", `\%、\_、\\ 分别匹配字面量 %、_、反斜杠`, "末尾孤立的反斜杠无效"} {
		if !strings.Contains(zhInstructions, fragment) {
			t.Errorf("zh-CN instructions: missing %q", fragment)
		}
	}
	if strings.Contains(zhInstructions, "不要自己拼 %") {
		t.Error("zh-CN instructions still describe literal substring semantics")
	}
}
