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
		"schemas/locales/zh-CN/instructions.txt":         &fstest.MapFile{Data: []byte("Chinese baseline instructions")},
		"schemas/locales/zh-CN/instructions_compact.txt": &fstest.MapFile{Data: []byte("Chinese baseline compact instructions")},
		"schemas/locales/zh-CN/ptc_instructions.txt":     &fstest.MapFile{Data: []byte("Chinese baseline PTC instructions")},
	}

	bundle := buildMCPLocaleBundleFromFS(resources, "en-US")

	if got := bundle.ServerInstructions(); got != "Chinese baseline instructions" {
		t.Fatalf("instructions = %q, want baseline instructions", got)
	}
	if got := bundle.CompactServerInstructions(); got != "Chinese baseline compact instructions" {
		t.Fatalf("compact instructions = %q, want baseline instructions", got)
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
		"schemas/locales/zh-CN/instructions_compact.txt": &fstest.MapFile{Data: []byte("Chinese baseline compact instructions")},
		"schemas/locales/zh-CN/ptc_instructions.txt":     &fstest.MapFile{Data: []byte("Chinese baseline PTC instructions")},
		"schemas/locales/en-US/instructions.txt":         &fstest.MapFile{Data: []byte("English instructions")},
		"schemas/locales/en-US/instructions_compact.txt": &fstest.MapFile{Data: []byte("English compact instructions")},
		"schemas/locales/en-US/ptc_instructions.txt":     &fstest.MapFile{Data: []byte("English PTC instructions")},
		"schemas/locales/en-US/tools_meta.json":          &fstest.MapFile{Data: []byte(`{`)},
		"schemas/locales/en-US/schema_descriptions.json": &fstest.MapFile{Data: []byte(`{`)},
	}

	bundle := buildMCPLocaleBundleFromFS(resources, "en-US")

	if got := bundle.ServerInstructions(); got != "English instructions" {
		t.Fatalf("instructions = %q, want localized instructions", got)
	}
	if got := bundle.CompactServerInstructions(); got != "English compact instructions" {
		t.Fatalf("compact instructions = %q, want localized instructions", got)
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
				"conversation_mode=new",
				"conversation_mode=continue",
				"conversation_id 在整个对话过程中保持不变",
				"interaction_id 在当前用户问题开始后、回复完成前保持不变",
				"bkn_context: { conversation_id, interaction_id }",
				"回复用户前，调用 bkn_finish_interaction",
				"下一个用户问题会开始新的 Interaction",
			},
			exploration: "探索顺序：",
		},
		{
			locale: "en-US",
			lifecycle: []string{
				"For each new user question",
				"conversation_mode=new",
				"conversation_mode=continue",
				"conversation_id remains unchanged throughout the conversation",
				"interaction_id remains unchanged from the start of the current user question until the reply is complete",
				"bkn_context: { conversation_id, interaction_id }",
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

// #1545: run_cypher needs only the knowledge-network grant while run_sql needs
// the caller's own grant on the resource, so aggregation that can be written
// on the model is routed to run_cypher first, in both locales and in both the
// server instructions and the PTC digest, and run_sql is described as the
// fallback that needs a resource grant.
func TestServerInstructionsRouteModelAggregationToCypherFirst(t *testing.T) {
	tests := []struct {
		locale       string
		expected     []string
		forbidden    []string
		ptcExpected  []string
		ptcForbidden []string
	}{
		{
			locale: "zh-CN",
			expected: []string{
				"能用对象类、关系类和逻辑属性说清楚的，先用 run_cypher",
				"run_sql 按调用者自己对底层资源的授权（view_detail）执行",
				"聚合类问题（如「每个 X 的 Y 总数/排名」）先用 run_cypher",
			},
			forbidden: []string{"直接走 run_sql"},
			ptcExpected: []string{
				"**优先写一条 `run_cypher`**",
				"才写 `run_sql`，它按调用者自己对底层资源",
				`print("答案:", run_cypher(`,
			},
			ptcForbidden: []string{"**优先写一条 `run_sql`**", `print("答案:", run_sql(`},
		},
		{
			locale: "en-US",
			expected: []string{
				"use run_cypher first whenever the question can be stated in object types, relation types and logical properties",
				"run_sql runs under the caller's own grant on the underlying resource (view_detail)",
				"write them on the model with run_cypher first",
			},
			forbidden: []string{"go directly to run_sql", "Use run_sql for joins, aggregation"},
			ptcExpected: []string{
				"prefer run_cypher whenever the question can be stated in object types, relation types and logical properties",
				"Use run_sql only for what the Cypher subset cannot express",
				"it runs under the caller's own grant on the underlying resource",
			},
			ptcForbidden: []string{"Prefer run_sql for database-side aggregation"},
		},
	}
	for _, test := range tests {
		t.Run(test.locale, func(t *testing.T) {
			bundle := loadMCPLocaleBundle(test.locale)
			for resource, text := range map[string]string{
				"instructions.txt":      bundle.ServerInstructions(),
				"ptc_digest_suffix.txt": bundle.PTCResource("ptc_digest_suffix.txt"),
			} {
				expected, forbidden := test.expected, test.forbidden
				if resource == "ptc_digest_suffix.txt" {
					expected, forbidden = test.ptcExpected, test.ptcForbidden
				}
				for _, want := range expected {
					if !strings.Contains(text, want) {
						t.Fatalf("%s for %s omits %q", resource, test.locale, want)
					}
				}
				for _, stale := range forbidden {
					if strings.Contains(text, stale) {
						t.Fatalf("%s for %s still says %q", resource, test.locale, stale)
					}
				}
			}
		})
	}
}

func TestServerInstructionsRequireRecomputableMembershipThresholdAndDetail(t *testing.T) {
	tests := []struct {
		locale string
		want   []string
	}{
		{locale: "zh-CN", want: []string{"成员资格、多条件阈值", "总数和明细", "不能先用 query_object_instance 拉回多页数据，再在 Agent 上下文里自行筛选、计数或去重"}},
		{locale: "en-US", want: []string{"membership, multi-condition thresholds", "both the total and the detail rows", "Do not page data through query_object_instance and then filter, count, or deduplicate it in Agent context"}},
	}
	for _, test := range tests {
		t.Run(test.locale, func(t *testing.T) {
			instructions := loadMCPLocaleBundle(test.locale).ServerInstructions()
			for _, want := range test.want {
				if !strings.Contains(instructions, want) {
					t.Fatalf("instructions for %s omit %q", test.locale, want)
				}
			}
		})
	}
}

func TestStartInteractionDescriptionGuidesStableAgentIdentity(t *testing.T) {
	tests := []struct {
		locale string
		want   string
	}{
		{
			locale: "zh-CN",
			want:   "为当前用户问题开始一次受管 Interaction。传入完整问题、当前 Agent 名称和 conversation_mode：没有当前受管 Conversation 时用 new 且不传 conversation_id；否则用 continue 并传入上次返回的 conversation_id。宿主提供会话映射时，以其为准。返回本轮 bkn_context 所需的两个 ID。",
		},
		{
			locale: "en-US",
			want:   "Start a managed Interaction for the current user question. Provide the complete question, the current Agent name, and conversation_mode: use new without conversation_id when there is no current managed Conversation; otherwise use continue with the conversation_id returned by the prior start. A host-provided conversation mapping remains authoritative. It returns the two IDs required in bkn_context for this turn.",
		},
	}

	for _, test := range tests {
		t.Run(test.locale, func(t *testing.T) {
			if got := loadMCPLocaleBundle(test.locale).ToolMeta("bkn_start_interaction").Description; got != test.want {
				t.Fatalf("start description for %s = %q, want %q", test.locale, got, test.want)
			}
		})
	}
}

func TestFinishInteractionDescriptionShowsTopLevelCompletedExample(t *testing.T) {
	tests := []struct {
		locale string
		want   string
	}{
		{
			locale: "zh-CN",
			want:   `在回复用户前结束当前 Interaction。顶层传入 interaction_id 和 outcome；outcome=completed 时还必须传入最终 answer，其他结果可传 reason。Conversation 可继续使用。示例：{"interaction_id":"int_...","outcome":"completed","answer":"..."}`,
		},
		{
			locale: "en-US",
			want:   `Finish the current Interaction before replying to the user. Pass interaction_id and outcome as top-level fields; outcome=completed also requires the final answer. Other outcomes may include reason. The Conversation remains available. Example: {"interaction_id":"int_...","outcome":"completed","answer":"..."}`,
		},
	}

	for _, test := range tests {
		t.Run(test.locale, func(t *testing.T) {
			if got := loadMCPLocaleBundle(test.locale).ToolMeta("bkn_finish_interaction").Description; got != test.want {
				t.Fatalf("finish description for %s = %q, want %q", test.locale, got, test.want)
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

// compactInstructionsBudget is the design ceiling for the compact profile's
// instructions (bkn-docs issue-1175 design, section 7): hosts that read server
// instructions load them into every conversation, so they stay short.
const compactInstructionsBudget = 2300

func TestShippedCompactInstructionsStayWithinBudget(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			bundle := buildMCPLocaleBundle(locale)
			compact := bundle.CompactServerInstructions()
			if strings.TrimSpace(compact) == "" {
				t.Fatal("compact instructions are empty")
			}
			if strings.Contains(compact, "Copyright") {
				t.Fatal("license header leaked into compact instructions")
			}
			if compact == bundle.ServerInstructions() {
				t.Fatal("compact instructions are the full-profile instructions")
			}
			if size := len(compact); size > compactInstructionsBudget {
				t.Fatalf("compact instructions are %d bytes, over the %d-byte budget", size, compactInstructionsBudget)
			}
		})
	}
}

// A tool without an output schema must stay without one after translation:
// wrapping and unwrapping it would otherwise turn the absent schema into the
// JSON literal null, which tools/list then publishes as "outputSchema": null.
func TestOverlayKeepsAnAbsentOutputSchemaAbsent(t *testing.T) {
	bundle := buildMCPLocaleBundle("en-US")
	input := json.RawMessage(`{"type":"object","properties":{"at_id":{"type":"string","description":"行动类 ID"}}}`)
	translated, output := bundle.OverlaySchemas(toolKeyGetActionInfo, input, nil)
	if len(output) != 0 {
		t.Fatalf("output schema = %s, want none", output)
	}
	if strings.Contains(string(translated), "行动类") {
		t.Fatalf("input schema was not translated: %s", translated)
	}
}
