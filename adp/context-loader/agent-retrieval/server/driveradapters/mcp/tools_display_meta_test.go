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

// 展示元数据的价值全在「客户端不用自己维护一张工具名到中文名的表」。所以每条
// 断言都对着序列化后的 JSON 走，而不是对着 Go 结构体——结构体填对了但没进
// wire 的情况，正是这类字段最容易出的问题。

func TestToolsListCarriesDisplayMetadata(t *testing.T) {
	noExtensions(t)

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

		want := loadToolMeta(wire.Name)
		if wire.Title != want.Title {
			t.Fatalf("tool %q advertises title %q, tools_meta.json says %q", wire.Name, wire.Title, want.Title)
		}
		if wire.Title == "" {
			t.Fatalf("tool %q has no title — a client would have to keep its own display-name table, which is what this field exists to remove", wire.Name)
		}
		if got := wire.Meta[toolMetaKeyGroup]; got != want.Group {
			t.Fatalf("tool %q advertises group %v, tools_meta.json says %q", wire.Name, got, want.Group)
		}
		if got := wire.Meta[toolMetaKeyGroupTitle]; got != want.GroupTitle {
			t.Fatalf("tool %q advertises group_title %v, tools_meta.json says %q", wire.Name, got, want.GroupTitle)
		}
		// JSON 数字解到 any 上是 float64，比较前先归一。
		if got, _ := wire.Meta[toolMetaKeyOrder].(float64); int(got) != want.Order {
			t.Fatalf("tool %q advertises order %v, tools_meta.json says %d", wire.Name, wire.Meta[toolMetaKeyOrder], want.Order)
		}
	}
}

// 每个核心工具都得声明齐这四项，且 order 互不重复——重复的 order 会让两个工具
// 在前端之间换位置，跟没有排序一样。
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

// 本地化文件只翻译，不重新建模：它可以给 title/group_title，但不该自带 group
// 或 order——那两项一旦分语言就意味着中英文两套目录结构。
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
	}
}

// /mcp/info 的用途是「不握手就看清能力面」，展示元数据两边说法不一致，前端按
// 哪一份渲染就成了掷硬币。
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
