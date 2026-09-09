// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
)

func TestBoundToolResultTextLeavesShortTextAlone(t *testing.T) {
	text := "a: 1\nb: 2\n"
	if got := boundToolResultText(text, 100); got != text {
		t.Fatalf("short text was altered: %q", got)
	}
	if got := boundToolResultText(text, 0); got != text {
		t.Fatalf("a zero budget must disable the bound, got %q", got)
	}
}

func TestBoundToolResultTextCutsOnLineAndNamesTheCut(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("row: 中文值中文值\n") // 12 characters per line
	}
	text := b.String()
	got := boundToolResultText(text, 500)

	body, note, found := strings.Cut(got, "\n\n[truncated]")
	if !found {
		t.Fatalf("missing truncation note in %q", got)
	}
	if !strings.HasSuffix(body, "中文值中文值") || strings.Count(body, "row:") != 41 {
		t.Fatalf("expected 41 whole rows, got %d rows ending %q", strings.Count(body, "row:"), body[len(body)-12:])
	}
	if utf8.RuneCountInString(body) > 500 {
		t.Fatalf("kept %d characters over a 500 budget", utf8.RuneCountInString(body))
	}
	if !strings.Contains(note, "of 1200 characters") || !strings.Contains(note, "concept_groups") {
		t.Fatalf("note must state the total and the knobs, got %q", note)
	}
}

func TestBoundToolResultTextCutsALongSingleLineAtTheBudget(t *testing.T) {
	text := strings.Repeat("x", 1000)
	got := boundToolResultText(text, 300)
	body, _, found := strings.Cut(got, "\n\n[truncated]")
	if !found || len(body) != 300 {
		t.Fatalf("a single line must be cut at the budget, kept %d", len(body))
	}
}

func TestBuildMCPToolResultBoundsTextButKeepsStructuredContentWhole(t *testing.T) {
	saved := toolResultMaxChars
	toolResultMaxChars = 200
	defer func() { toolResultMaxChars = saved }()

	rows := make([]map[string]any, 0, 50)
	for i := 0; i < 50; i++ {
		rows = append(rows, map[string]any{"id": i, "name": strings.Repeat("n", 20)})
	}
	resp := map[string]any{"datas": rows}
	for _, format := range []rest.ResponseFormat{rest.FormatTOON, rest.FormatJSON} {
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		text, ok := mcp.AsTextContent(result.Content[0])
		if !ok || !strings.Contains(text.Text, "[truncated]") {
			t.Fatalf("%s: text was not bounded: %q", format, text.Text)
		}
		structured, ok := result.StructuredContent.(map[string]any)
		if !ok || len(structured["datas"].([]map[string]any)) != 50 {
			t.Fatalf("%s: structured content must stay whole, got %#v", format, result.StructuredContent)
		}
	}
}

func TestLoadToolResultMaxCharsReadsTheEnvironment(t *testing.T) {
	t.Setenv(ToolResultMaxCharsEnv, "1234")
	if got := loadToolResultMaxChars(); got != 1234 {
		t.Fatalf("got %d", got)
	}
	t.Setenv(ToolResultMaxCharsEnv, "0")
	if got := loadToolResultMaxChars(); got != 0 {
		t.Fatalf("0 must disable, got %d", got)
	}
	t.Setenv(ToolResultMaxCharsEnv, "lots")
	if got := loadToolResultMaxChars(); got != DefaultToolResultMaxChars {
		t.Fatalf("garbage must fall back to the default, got %d", got)
	}
}
