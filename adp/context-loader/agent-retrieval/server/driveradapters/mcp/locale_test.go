package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestMCPLocaleBundle(t *testing.T) {
	convey.Convey("MCP locale bundle should localize instructions, tool meta, and schema descriptions", t, func() {
		bundle := loadMCPLocaleBundle("en-US")

		convey.So(bundle.ServerInstructions(), convey.ShouldContainSubstring, "Context Loader knowledge network tools")

		meta := bundle.ToolMeta(toolKeySearchSchema)
		convey.So(meta.Name, convey.ShouldEqual, "search_schema")
		convey.So(meta.Description, convey.ShouldContainSubstring, "Explore schema")
		convey.So(meta.Title, convey.ShouldEqual, "Explore Schema")
		convey.So(meta.GroupTitle, convey.ShouldEqual, "Networks & Schema")
		// group / order 只在基准文件里声明，本地化文件不重复一遍——翻译改不动
		// 分组归属和排序。
		convey.So(meta.Group, convey.ShouldEqual, "discovery")
		convey.So(meta.Order, convey.ShouldEqual, 130)

		input, _ := bundle.ToolSchemas(toolKeySearchSchema)
		var schema map[string]any
		convey.So(json.Unmarshal(input, &schema), convey.ShouldBeNil)
		properties := schema["properties"].(map[string]any)
		query := properties["query"].(map[string]any)
		convey.So(query["description"], convey.ShouldEqual, "Natural-language question or keywords to search.")
	})

	convey.Convey("unknown MCP locale should fall back to the default bundle", t, func() {
		bundle := loadMCPLocaleBundle("fr-FR")

		convey.So(bundle.ServerInstructions(), convey.ShouldEqual, serverInstructions)
		convey.So(bundle.ToolMeta(toolKeyRunSQL).Name, convey.ShouldEqual, toolKeyRunSQL)
	})

	convey.Convey("localized schema description overlays should match existing schema paths", t, func() {
		bundle := loadMCPLocaleBundle("en-US")

		for toolKey, replacements := range bundle.schemaDescriptions {
			input, output := bundle.ToolSchemas(toolKey)
			var schema map[string]any
			convey.So(json.Unmarshal(mustMarshalToolSchema(input, output), &schema), convey.ShouldBeNil)

			for path, expected := range replacements {
				actual, ok := getNestedString(schema, strings.Split(path, "."))
				convey.So(ok, convey.ShouldBeTrue)
				convey.So(actual, convey.ShouldEqual, expected)
			}
		}
	})
}

func getNestedString(root map[string]any, path []string) (string, bool) {
	var current any = root
	for _, segment := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = obj[segment]
		if !ok {
			return "", false
		}
	}
	value, ok := current.(string)
	return value, ok
}
