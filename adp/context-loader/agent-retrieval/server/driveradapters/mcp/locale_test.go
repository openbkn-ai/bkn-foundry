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

		name, description := bundle.ToolMeta(toolKeySearchSchema)
		convey.So(name, convey.ShouldEqual, "search_schema")
		convey.So(description, convey.ShouldContainSubstring, "Explore schema")

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
		name, _ := bundle.ToolMeta(toolKeyRunSQL)
		convey.So(name, convey.ShouldEqual, toolKeyRunSQL)
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
