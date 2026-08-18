// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"

	"github.com/smartystreets/goconvey/convey"
)

// containsHan reports whether the text holds a Han character, which in this
// catalog means untranslated default-locale text.
func containsHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// collectHan walks a decoded JSON schema and returns the dotted paths of every
// description or title that still holds Han characters.
func collectHan(node any, path string, found *[]string) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if key == "description" || key == "title" {
				if text, ok := child.(string); ok {
					if containsHan(text) {
						*found = append(*found, childPath+" => "+text)
					}
					continue
				}
			}
			collectHan(child, childPath, found)
		}
	case []any:
		for index, child := range value {
			collectHan(child, path, found)
			_ = index
		}
	}
}

// TestEnglishCatalogHasNoHanText is the acceptance guard for #826: under en-US
// the tool catalog a client sees must not mix in default-locale text.
//
// It reads the same bundle tools/list and /mcp/info read, so a new tool or a new
// schema description that nobody translated fails here rather than reaching an
// English deployment as mixed-language output.
func TestEnglishCatalogHasNoHanText(t *testing.T) {
	convey.Convey("the en-US MCP catalog should hold no default-locale text", t, func() {
		bundle := loadMCPLocaleBundle("en-US")

		convey.Convey("server instructions", func() {
			convey.So(containsHan(bundle.ServerInstructions()), convey.ShouldBeFalse)
			convey.So(containsHan(bundle.PTCServerInstructions()), convey.ShouldBeFalse)
		})

		convey.Convey("tool metadata and schemas", func() {
			var offenders []string
			for toolKey := range allToolMeta() {
				meta := bundle.ToolMeta(toolKey)
				for label, text := range map[string]string{
					"title":       meta.Title,
					"group_title": meta.GroupTitle,
					"description": meta.Description,
				} {
					if containsHan(text) {
						offenders = append(offenders, toolKey+"."+label+" => "+text)
					}
				}

				input, output := bundle.ToolSchemas(toolKey)
				for label, raw := range map[string]json.RawMessage{
					"input_schema":  input,
					"output_schema": output,
				} {
					if len(raw) == 0 {
						continue
					}
					var schema any
					convey.So(json.Unmarshal(raw, &schema), convey.ShouldBeNil)
					var found []string
					collectHan(schema, "", &found)
					for _, entry := range found {
						offenders = append(offenders, toolKey+"."+label+"."+entry)
					}
				}
			}
			convey.So(strings.Join(offenders, "\n"), convey.ShouldBeBlank)
		})
	})
}

// TestMCPInfoDeclaresItsLanguage pins the self-description side of the language
// contract. MCP defines no locale field, so /mcp/info is where an integrator
// learns that the transport header decides the catalog language — and its config
// example is the block they copy.
func TestMCPInfoDeclaresItsLanguage(t *testing.T) {
	convey.Convey("/mcp/info should state the language it answered in", t, func() {
		for _, locale := range []string{defaultMCPLocale, "en-US"} {
			info, err := BuildMCPInfoForLocale("https://example.invalid/mcp", locale)
			convey.So(err, convey.ShouldBeNil)
			convey.So(info.Language, convey.ShouldEqual, locale)
			convey.So(info.SupportedLanguages, convey.ShouldContain, "en-US")
			convey.So(info.SupportedLanguages, convey.ShouldContain, defaultMCPLocale)

			var example struct {
				MCPServers map[string]struct {
					Headers map[string]string `json:"headers"`
				} `json:"mcpServers"`
			}
			convey.So(json.Unmarshal(info.ClientConfigExample, &example), convey.ShouldBeNil)
			for _, server := range example.MCPServers {
				convey.So(server.Headers["Accept-Language"], convey.ShouldEqual, locale)
			}
		}
	})

	convey.Convey("an unsupported language should report the fallback it used", t, func() {
		info, err := BuildMCPInfoForLocale("https://example.invalid/mcp", "kl-KL")
		convey.So(err, convey.ShouldBeNil)
		convey.So(info.Language, convey.ShouldEqual, defaultMCPLocale)
	})
}
