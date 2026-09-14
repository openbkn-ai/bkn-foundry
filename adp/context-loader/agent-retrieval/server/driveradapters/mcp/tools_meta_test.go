// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecuteActionToolMetaDocumentsDuplicate409(t *testing.T) {
	for _, path := range []string{
		"schemas/tools_meta.json",
		"schemas/locales/en-US/tools_meta.json",
	} {
		t.Run(path, func(t *testing.T) {
			raw, err := schemasFS.ReadFile(path)
			if err != nil {
				t.Fatalf("read tool metadata: %v", err)
			}
			var meta map[string]ToolMeta
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Fatalf("decode tool metadata: %v", err)
			}
			tool, ok := meta["execute_action"]
			if !ok {
				t.Fatal("execute_action metadata missing")
			}
			desc := strings.ToLower(tool.Description)
			if !strings.Contains(desc, "409") {
				t.Fatalf("execute_action description must mention HTTP 409 duplicate semantics: %q", tool.Description)
			}
			if strings.Contains(path, "en-US") {
				if !strings.Contains(desc, "do not retry") {
					t.Fatalf("en-US execute_action description must tell agents not to retry: %q", tool.Description)
				}
				return
			}
			if !strings.Contains(tool.Description, "不要重试") {
				t.Fatalf("execute_action description must tell agents not to retry: %q", tool.Description)
			}
		})
	}
}

func TestPhysicalResourceToolsDocumentIndependentAuthorizationBoundary(t *testing.T) {
	for _, path := range []string{
		"schemas/tools_meta.json",
		"schemas/locales/en-US/tools_meta.json",
	} {
		t.Run(path, func(t *testing.T) {
			raw, err := schemasFS.ReadFile(path)
			if err != nil {
				t.Fatalf("read tool metadata: %v", err)
			}
			var meta map[string]ToolMeta
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Fatalf("decode tool metadata: %v", err)
			}
			for _, name := range []string{"list_resources", "describe_resource", "run_sql"} {
				desc := strings.ToLower(meta[name].Description)
				for _, required := range []string{"vega", "none/schema/masked/full"} {
					if !strings.Contains(desc, required) {
						t.Fatalf("%s description must document %q boundary: %q", name, required, meta[name].Description)
					}
				}
				if strings.Contains(path, "en-US") {
					if !strings.Contains(desc, "must never") {
						t.Fatalf("%s must prohibit internal object-query use: %q", name, meta[name].Description)
					}
				} else if !strings.Contains(meta[name].Description, "不得") {
					t.Fatalf("%s must prohibit internal object-query use: %q", name, meta[name].Description)
				}
			}
		})
	}
}

// run_sql is the one query tool that does not follow knowledge-network
// authorization. Its description must say which grant it needs and where a caller
// authorized only through object types should go instead, so an agent does not
// learn it from a 403. See #1543.
func TestRunSQLToolMetaStatesDirectResourcePermission(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"schemas/tools_meta.json",
			[]string{"view_detail", "知识网络授权", "对象类", "query_object_instance", "run_cypher"}},
		{"schemas/locales/en-US/tools_meta.json",
			[]string{"view_detail", "knowledge-network authorization", "object types", "query_object_instance", "run_cypher"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			raw, err := schemasFS.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read tool metadata: %v", err)
			}
			var meta map[string]ToolMeta
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Fatalf("decode tool metadata: %v", err)
			}
			desc := meta["run_sql"].Description
			for _, want := range tc.want {
				if !strings.Contains(desc, want) {
					t.Errorf("run_sql description must mention %q: %q", want, desc)
				}
			}
		})
	}
}
