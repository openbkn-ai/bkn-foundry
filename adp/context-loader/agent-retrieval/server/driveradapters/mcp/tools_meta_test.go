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
