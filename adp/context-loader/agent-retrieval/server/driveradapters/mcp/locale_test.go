package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/smartystreets/goconvey/convey"
)

func TestMCPLocaleBundle(t *testing.T) {
	convey.Convey("MCP locale bundle should localize instructions, tool meta, and schema descriptions", t, func() {
		bundle := loadMCPLocaleBundle("en-US")

		convey.So(bundle.ServerInstructions(), convey.ShouldContainSubstring, "Context Loader knowledge network tools")
		convey.So(bundle.PTCServerInstructions(), convey.ShouldContainSubstring, "This endpoint provides two execution tools")

		meta := bundle.ToolMeta(toolKeySearchSchema)
		convey.So(meta.Name, convey.ShouldEqual, "search_schema")
		convey.So(meta.Description, convey.ShouldContainSubstring, "Explore schema")
		convey.So(meta.Title, convey.ShouldEqual, "Explore Schema")
		convey.So(meta.GroupTitle, convey.ShouldEqual, "Networks & Schema")
		// Group and order belong only to the baseline file. Locales can translate
		// presentation text, but must not alter grouping or ordering.
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

		convey.So(bundle.ServerInstructions(), convey.ShouldEqual, mustReadMCPInstructions(schemasFS, defaultMCPLocale))
		convey.So(bundle.ToolMeta(toolKeyRunSQL).Name, convey.ShouldEqual, toolKeyRunSQL)
	})

	convey.Convey("localized schema description overlays should match existing schema paths", t, func() {
		bundle := loadMCPLocaleBundle("en-US")

		for toolKey, replacements := range bundle.schemaDescriptions {
			input, output := bundle.ToolSchemas(toolKey)
			wrapped, err := marshalToolSchema(input, output)
			convey.So(err, convey.ShouldBeNil)
			var schema map[string]any
			convey.So(json.Unmarshal(wrapped, &schema), convey.ShouldBeNil)

			for path, expected := range replacements {
				actual, ok := getNestedString(schema, strings.Split(path, "."))
				convey.So(ok, convey.ShouldBeTrue)
				convey.So(actual, convey.ShouldEqual, expected)
			}
		}
	})
}

func TestMCPLocaleBundleFallsBackWhenOverlayResourcesAreUnavailable(t *testing.T) {
	resources := fstest.MapFS{
		"schemas/locales/zh-CN/instructions.txt":     &fstest.MapFile{Data: []byte("Chinese baseline instructions")},
		"schemas/locales/zh-CN/ptc_instructions.txt": &fstest.MapFile{Data: []byte("Chinese baseline PTC instructions")},
	}

	bundle := buildMCPLocaleBundleFromFS(resources, "en-US")

	if got := bundle.ServerInstructions(); got != "Chinese baseline instructions" {
		t.Fatalf("instructions = %q, want baseline instructions", got)
	}
	if got := bundle.PTCServerInstructions(); got != "Chinese baseline PTC instructions" {
		t.Fatalf("PTC instructions = %q, want baseline instructions", got)
	}
	if bundle.toolMeta != nil {
		t.Fatalf("tool metadata = %#v, want no overlay", bundle.toolMeta)
	}
	if bundle.schemaDescriptions != nil {
		t.Fatalf("schema descriptions = %#v, want no overlay", bundle.schemaDescriptions)
	}
}

func TestMCPResourceLicenseHeaderIsNotExposed(t *testing.T) {
	resources := fstest.MapFS{
		"schemas/locales/zh-CN/ptc_instructions.txt": &fstest.MapFile{Data: []byte(`/*
 * Copyright 2026 openbkn.ai
 *
 * Licensed under the Apache License, Version 2.0.
 */
PTC instructions`)},
	}

	if got := mustReadMCPResource(resources, defaultMCPLocale, "ptc_instructions.txt"); got != "PTC instructions" {
		t.Fatalf("resource = %q, want license header stripped", got)
	}
}

func TestMCPLocaleBundleFallsBackWhenOverlayResourcesAreMalformed(t *testing.T) {
	resources := fstest.MapFS{
		"schemas/locales/zh-CN/instructions.txt":         &fstest.MapFile{Data: []byte("Chinese baseline instructions")},
		"schemas/locales/zh-CN/ptc_instructions.txt":     &fstest.MapFile{Data: []byte("Chinese baseline PTC instructions")},
		"schemas/locales/en-US/instructions.txt":         &fstest.MapFile{Data: []byte("English instructions")},
		"schemas/locales/en-US/ptc_instructions.txt":     &fstest.MapFile{Data: []byte("English PTC instructions")},
		"schemas/locales/en-US/tools_meta.json":          &fstest.MapFile{Data: []byte(`{`)},
		"schemas/locales/en-US/schema_descriptions.json": &fstest.MapFile{Data: []byte(`{`)},
	}

	bundle := buildMCPLocaleBundleFromFS(resources, "en-US")

	if got := bundle.ServerInstructions(); got != "English instructions" {
		t.Fatalf("instructions = %q, want localized instructions", got)
	}
	if got := bundle.PTCServerInstructions(); got != "English PTC instructions" {
		t.Fatalf("PTC instructions = %q, want localized instructions", got)
	}
	if bundle.toolMeta != nil {
		t.Fatalf("tool metadata = %#v, want no overlay", bundle.toolMeta)
	}
	if bundle.schemaDescriptions != nil {
		t.Fatalf("schema descriptions = %#v, want no overlay", bundle.schemaDescriptions)
	}
}

func TestServerInstructionsLeadWithManagedInteractionLifecycle(t *testing.T) {
	tests := []struct {
		locale      string
		lifecycle   []string
		exploration string
	}{
		{
			locale: "zh-CN",
			lifecycle: []string{
				"处理每个新的用户问题时",
				"conversation_id 在整个对话过程中保持不变",
				"interaction_id 在当前用户问题开始后、回复完成前保持不变",
				"回复用户前，调用 bkn_finish_interaction",
				"下一个用户问题会开始新的 Interaction",
			},
			exploration: "探索顺序：",
		},
		{
			locale: "en-US",
			lifecycle: []string{
				"For each new user question",
				"conversation_id remains unchanged throughout the conversation",
				"interaction_id remains unchanged from the start of the current user question until the reply is complete",
				"Before replying to the user, call bkn_finish_interaction",
				"The next user question starts a new Interaction",
			},
			exploration: "Suggested exploration order:",
		},
	}

	for _, test := range tests {
		t.Run(test.locale, func(t *testing.T) {
			instructions := loadMCPLocaleBundle(test.locale).ServerInstructions()
			for _, expected := range test.lifecycle {
				if !strings.Contains(instructions, expected) {
					t.Fatalf("instructions for %s omit %q:\n%s", test.locale, expected, instructions)
				}
			}
			if lifecycleAt, explorationAt := strings.Index(instructions, test.lifecycle[0]), strings.Index(instructions, test.exploration); lifecycleAt < 0 || explorationAt < 0 || lifecycleAt >= explorationAt {
				t.Fatalf("managed lifecycle must precede query guidance for %s:\n%s", test.locale, instructions)
			}
		})
	}
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
