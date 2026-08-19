// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// The value of displaying metadata is that "the client does not need to maintain a table from tool name to Chinese name". So every.
// The assertions are all directed against the serialized JSON, not the Go structure - the structure is filled in correctly but not entered.
// In the case of wire, this type of field is most prone to problems.

func TestToolsListCarriesDisplayMetadata(t *testing.T) {
	noExtensions(t)

	// Get the expected value according to the current locale of the process, instead of the Hengdu Chinese benchmark file: on the local development machine.
	// LANG=en_US is the norm. Hard-coding the benchmark file will make this assertion falsely red.
	locale := loadMCPLocaleBundle(mcpLocaleFromEnv())
	for _, tool := range assembledTools(t) {
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal tool %q: %v", tool.Name, err)
		}
		var wire struct {
			Name  string         `json:"name"`
			Title string         `json:"title"`
			Meta  map[string]any `json:"_meta"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatalf("decode tool %q: %v", tool.Name, err)
		}

		want := locale.ToolMeta(wire.Name)
		if wire.Title != want.Title {
			t.Fatalf("tool %q advertises title %q, tool metadata says %q", wire.Name, wire.Title, want.Title)
		}
		if wire.Title == "" {
			t.Fatalf("tool %q has no title — a client would have to keep its own display-name table, which is what this field exists to remove", wire.Name)
		}
		if got := wire.Meta[toolMetaKeyGroup]; got != want.Group {
			t.Fatalf("tool %q advertises group %v, tool metadata says %q", wire.Name, got, want.Group)
		}
		if got := wire.Meta[toolMetaKeyGroupTitle]; got != want.GroupTitle {
			t.Fatalf("tool %q advertises group_title %v, tool metadata says %q", wire.Name, got, want.GroupTitle)
		}
		// JSON numbers converted to any are float64, and should be normalized before comparison.
		if got, _ := wire.Meta[toolMetaKeyOrder].(float64); int(got) != want.Order {
			t.Fatalf("tool %q advertises order %v, tool metadata says %d", wire.Name, wire.Meta[toolMetaKeyOrder], want.Order)
		}
	}
}

// Each core tool must declare these four items, and the orders must not repeat each other - repeated orders will cause the two tools to.
// Changing positions between front ends is the same as without sorting.
func TestEveryCoreToolDeclaresDisplayMetadata(t *testing.T) {
	seenOrder := map[int]string{}
	for key, meta := range allToolMeta() {
		if meta.Title == "" {
			t.Errorf("tool %q has no title in tools_meta.json", key)
		}
		if meta.Group == "" || meta.GroupTitle == "" {
			t.Errorf("tool %q has no group in tools_meta.json", key)
		}
		if meta.Order == 0 {
			t.Errorf("tool %q has no order in tools_meta.json (0 means \"unset\" on the wire, so it is not a usable position)", key)
		}
		if prev, dup := seenOrder[meta.Order]; dup {
			t.Errorf("tools %q and %q share order %d — they would swap places between renders", prev, key, meta.Order)
		}
		seenOrder[meta.Order] = key
	}
}

// The localization file is only translated, not remodeled: it can give title/group_title, but it should not bring its own group.
// Or order - once those two items are divided into languages, they mean two sets of directory structures in Chinese and English.
func TestLocalizedToolMetaTranslatesWithoutRemodelling(t *testing.T) {
	raw, err := schemasFS.ReadFile("schemas/locales/en-US/tools_meta.json")
	if err != nil {
		t.Fatalf("read localized tool metadata: %v", err)
	}
	var meta map[string]ToolMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("decode localized tool metadata: %v", err)
	}
	base := allToolMeta()
	for key := range base {
		if _, ok := meta[key]; !ok {
			t.Errorf("tool %q has no en-US entry — it would advertise its zh-CN title in an English catalogue", key)
		}
	}
	for key, m := range meta {
		if _, ok := base[key]; !ok {
			t.Errorf("localized tool metadata has %q, which tools_meta.json does not", key)
			continue
		}
		if m.Title == "" {
			t.Errorf("localized tool %q has no title — it would fall back to the zh-CN one", key)
		}
		if m.Group != "" || m.Order != 0 {
			t.Errorf("localized tool %q declares group/order; those belong to tools_meta.json alone", key)
		}
		// Name is the identifier carried by tools/call. Localization files were written for it, the same tool works in Chinese and.
		// There will be two names on the English deployment. According to the client written on one side, it cannot be called directly on the other side.
		if m.Name != "" {
			t.Errorf("localized tool %q declares a name; the wire identifier must not depend on the deployment's language", key)
		}
	}
}

// The purpose of /mcp/info is to "see the capabilities clearly without shaking hands". It shows that the two sides of the metadata are inconsistent. The front-end press.
// Which render becomes a coin toss.
func TestMCPInfoDisplayMetadataMatchesToolsList(t *testing.T) {
	noExtensions(t)

	info, err := BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	listed := map[string]mcp.Tool{}
	for _, tool := range assembledTools(t) {
		listed[tool.Name] = tool
	}

	for _, tool := range info.Tools {
		got, ok := listed[tool.Name]
		if !ok {
			t.Fatalf("/mcp/info advertises %q, which tools/list does not", tool.Name)
		}
		if tool.Title != got.Title {
			t.Fatalf("tool %q: /mcp/info title %q, tools/list title %q", tool.Name, tool.Title, got.Title)
		}
		var group, groupTitle string
		var order int
		if got.Meta != nil {
			group, _ = got.Meta.AdditionalFields[toolMetaKeyGroup].(string)
			groupTitle, _ = got.Meta.AdditionalFields[toolMetaKeyGroupTitle].(string)
			order, _ = got.Meta.AdditionalFields[toolMetaKeyOrder].(int)
		}
		if tool.Group != group || tool.GroupTitle != groupTitle || tool.Order != order {
			t.Fatalf("tool %q: /mcp/info says (%q, %q, %d), tools/list says (%q, %q, %d)",
				tool.Name, tool.Group, tool.GroupTitle, tool.Order, group, groupTitle, order)
		}
	}
}

// Three registration paths - toolBuilder, lifecycle adaptation layer, enterprise socket - only the first one passed before.
// locale, the other two directly read the Chinese benchmark file. The flaw only appears on non-Chinese deployments: in a directory.
// 14 English names and 4 Chinese names. This test assembles the entire directory according to the non-default locale. Any.
// If a path is missed, it will be red.
func TestEveryRegistrationPathHonoursTheLocale(t *testing.T) {
	noExtensions(t)
	t.Setenv("MCP_LOCALE", "en-US")

	raw, err := schemasFS.ReadFile("schemas/locales/en-US/tools_meta.json")
	if err != nil {
		t.Fatalf("read localized tool metadata: %v", err)
	}
	var localized map[string]ToolMeta
	if err := json.Unmarshal(raw, &localized); err != nil {
		t.Fatalf("decode localized tool metadata: %v", err)
	}

	for _, tool := range assembledTools(t) {
		want, ok := localized[tool.Name]
		if !ok {
			t.Errorf("tool %q has no en-US entry", tool.Name)
			continue
		}
		if tool.Title != want.Title {
			t.Errorf("tool %q advertises title %q under en-US, the localized file says %q — its registration path does not go through the locale bundle",
				tool.Name, tool.Title, want.Title)
		}
	}
}

// This pins locale handling for /mcp/info under a non-default locale.
// zh-CN has no overlay, so only en-US proves that the info endpoint receives
// and applies the same locale as tools/list.
func TestMCPInfoAgreesWithToolsListUnderANonDefaultLocale(t *testing.T) {
	noExtensions(t)
	t.Setenv("MCP_LOCALE", "en-US")

	info, err := BuildMCPInfoForLocale("https://example.invalid/mcp", "en-US")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	listed := map[string]mcp.Tool{}
	for _, tool := range assembledTools(t) {
		listed[tool.Name] = tool
	}
	if len(info.Tools) != len(listed) {
		t.Fatalf("/mcp/info 有 %d 个工具，tools/list 有 %d 个", len(info.Tools), len(listed))
	}

	for _, tool := range info.Tools {
		got, ok := listed[tool.Name]
		if !ok {
			t.Fatalf("/mcp/info advertises %q, which tools/list does not", tool.Name)
		}
		if tool.Description != got.Description {
			t.Errorf("tool %q description differs between the two endpoints:\n/mcp/info : %s\ntools/list: %s",
				tool.Name, tool.Description, got.Description)
		}
		if string(tool.InputSchema) != string(got.RawInputSchema) {
			t.Errorf("tool %q input schema differs between the two endpoints:\n/mcp/info : %s\ntools/list: %s",
				tool.Name, tool.InputSchema, got.RawInputSchema)
		}
		if string(tool.OutputSchema) != string(got.RawOutputSchema) {
			t.Errorf("tool %q output schema differs between the two endpoints:\n/mcp/info : %s\ntools/list: %s",
				tool.Name, tool.OutputSchema, got.RawOutputSchema)
		}
	}
}

// assembledTools returns the tools a client would see from tools/list.
func assembledTools(t *testing.T) []mcp.Tool {
	t.Helper()
	srv, b := newMCPServer(nil)

	all := make([]mcp.Tool, 0, len(srv.ListTools()))
	for _, st := range srv.ListTools() {
		all = append(all, st.Tool)
	}
	return b.filter(context.Background(), all)
}
