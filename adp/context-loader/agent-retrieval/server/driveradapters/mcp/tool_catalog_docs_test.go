// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The tool catalogues in the API reference and the user manuals are written by
// hand. Tools were added for months without anyone updating them, until the
// documents listed 17 of 30 tools. These tests tie every catalogue to the
// embedded tool metadata, so adding, renaming or removing a tool without
// updating the documents fails here.

// repoRoot is the repository root, seen from this package directory.
var repoRoot = filepath.Join("..", "..", "..", "..", "..", "..")

const (
	apiReferencePath  = "docs/api/context-loader/mcp.yaml"
	apiCatalogHeading = "### 工具目录"
)

var manualCatalogs = []struct {
	path    string
	heading string
}{
	{"help/zh/manual/context-loader.md", "### MCP 工具列表"},
	{"help/en/manual/context-loader.md", "### Available MCP Tools"},
}

// Values of the "精简入口" column in the API reference catalogue.
const (
	compactPublished = "直接公布"
	compactOnly      = "仅精简入口"
	compactGateway   = "经网关"
)

var backtickedName = regexp.MustCompile("`([a-z][a-z0-9_]*)`")

// documentedToolNames is every tool the service ships metadata for, checked to
// be the same in every locale.
func documentedToolNames(t *testing.T) []string {
	t.Helper()
	read := func(path string) []string {
		raw, err := schemasFS.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var meta map[string]json.RawMessage
		if err := json.Unmarshal(raw, &meta); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		names := make([]string, 0, len(meta))
		for name := range meta {
			names = append(names, name)
		}
		slices.Sort(names)
		return names
	}
	names := read("schemas/tools_meta.json")
	if len(names) == 0 {
		t.Fatal("tools_meta.json lists no tools")
	}
	english := read("schemas/locales/en-US/tools_meta.json")
	if !slices.Equal(names, english) {
		t.Fatalf("the locales list different tools:\n  zh-CN: %v\n  en-US: %v", names, english)
	}
	return names
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// tableUnder returns the rows of the first Markdown table after heading,
// excluding the header and separator rows, each split into trimmed cells.
func tableUnder(t *testing.T, label, text, heading string) [][]string {
	t.Helper()
	lines := strings.Split(text, "\n")
	start := slices.IndexFunc(lines, func(line string) bool { return strings.TrimSpace(line) == heading })
	if start < 0 {
		t.Fatalf("%s: heading %q not found", label, heading)
	}
	var rows [][]string
	inTable := false
	for _, line := range lines[start+1:] {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			if inTable {
				break
			}
			continue
		}
		inTable = true
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, cells)
	}
	if len(rows) < 3 {
		t.Fatalf("%s: no table under %q", label, heading)
	}
	return rows[2:]
}

// assertSameTools fails with what is missing and what is extra, the two
// things an author needs to fix.
func assertSameTools(t *testing.T, label string, want, got []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, name := range got {
		if seen[name] {
			t.Errorf("%s: %s is listed more than once", label, name)
		}
		seen[name] = true
	}
	var missing, extra []string
	for _, name := range want {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	for name := range seen {
		if !slices.Contains(want, name) {
			extra = append(extra, name)
		}
	}
	slices.Sort(extra)
	if len(missing) > 0 {
		t.Errorf("%s: tools missing from the catalogue: %v", label, missing)
	}
	if len(extra) > 0 {
		t.Errorf("%s: catalogue lists tools the service does not ship: %v", label, extra)
	}
}

func apiReferenceDescription(t *testing.T) string {
	t.Helper()
	var doc struct {
		Info struct {
			Description string `yaml:"description"`
		} `yaml:"info"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, apiReferencePath)), &doc); err != nil {
		t.Fatalf("decode %s: %v", apiReferencePath, err)
	}
	return doc.Info.Description
}

func TestAPIReferenceCatalogsEveryTool(t *testing.T) {
	rows := tableUnder(t, apiReferencePath, apiReferenceDescription(t), apiCatalogHeading)
	var listed []string
	for _, row := range rows {
		match := backtickedName.FindStringSubmatch(row[0])
		if match == nil || match[0] != row[0] {
			t.Fatalf("%s: first cell must be one tool name in backticks, got %q", apiReferencePath, row[0])
		}
		listed = append(listed, match[1])
	}
	assertSameTools(t, apiReferencePath, documentedToolNames(t), listed)
}

// The "精简入口" column has to say which tools /mcp-compact publishes, the
// list a host sees in its tools/list.
func TestAPIReferenceCatalogStatesTheCompactSurface(t *testing.T) {
	rows := tableUnder(t, apiReferencePath, apiReferenceDescription(t), apiCatalogHeading)
	const compactColumn = 3
	var published []string
	for _, row := range rows {
		if len(row) <= compactColumn {
			t.Fatalf("%s: row %v has no 精简入口 column", apiReferencePath, row)
		}
		name := strings.Trim(row[0], "`")
		cell := row[compactColumn]
		switch {
		case cell == compactPublished || cell == compactOnly:
			published = append(published, name)
		case strings.HasPrefix(cell, compactGateway):
		default:
			t.Errorf("%s: %s has 精简入口 %q; use %s, %s or %s",
				apiReferencePath, name, cell, compactPublished, compactGateway, compactOnly)
		}
		inGateway := slices.Contains(gatewayToolOrder, name)
		if (cell == compactOnly) != inGateway {
			t.Errorf("%s: %s is marked %q, but only the gateway tools are %s", apiReferencePath, name, cell, compactOnly)
		}
	}
	want := make([]string, 0, len(compactProfile.published))
	for name := range compactProfile.published {
		want = append(want, name)
	}
	slices.Sort(want)
	assertSameTools(t, apiReferencePath+" (/mcp-compact)", want, published)
}

func TestManualsCatalogEveryTool(t *testing.T) {
	want := documentedToolNames(t)
	for _, manual := range manualCatalogs {
		rows := tableUnder(t, manual.path, readRepoFile(t, manual.path), manual.heading)
		var listed []string
		for _, row := range rows {
			names := backtickedName.FindAllStringSubmatch(row[0], -1)
			if len(names) == 0 {
				t.Fatalf("%s: first cell names no tool: %q", manual.path, row[0])
			}
			for _, name := range names {
				listed = append(listed, name[1])
			}
		}
		assertSameTools(t, manual.path, want, listed)
	}
}

// The documents state how many tools /mcp-compact publishes. That number went
// stale once already (11 after the profile had grown to 14).
func TestDocumentsStateTheCompactToolCount(t *testing.T) {
	count := regexp.MustCompile(`公布(?:的)? ?(\d+) 个工具|固定 (\d+) 个|publishes (\d+) tools`)
	want := strconv.Itoa(len(compactProfile.published))
	for _, path := range []string{apiReferencePath, manualCatalogs[0].path, manualCatalogs[1].path} {
		matches := count.FindAllStringSubmatch(readRepoFile(t, path), -1)
		if len(matches) == 0 {
			t.Errorf("%s: states no /mcp-compact tool count", path)
		}
		for _, match := range matches {
			got := strings.Join(match[1:], "")
			if got != want {
				t.Errorf("%s: %q says %s tools, /mcp-compact publishes %s", path, match[0], got, want)
			}
		}
	}
}
