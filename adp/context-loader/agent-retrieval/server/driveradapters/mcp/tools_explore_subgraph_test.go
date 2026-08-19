// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// toolResultText concatenates the text returned by the tool and is used to assert that the error message points to a specific field name.
func toolResultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

type stubSubgraphService struct {
	exploreReq  *interfaces.ExploreSubgraphReq
	exploreResp *interfaces.ExploreSubgraphResp
	called      bool
}

func (s *stubSubgraphService) QueryInstanceSubgraph(_ context.Context, _ *interfaces.QueryInstanceSubgraphReq) (*interfaces.QueryInstanceSubgraphResp, error) {
	return nil, nil
}

func (s *stubSubgraphService) ExploreSubgraph(_ context.Context, req *interfaces.ExploreSubgraphReq) (*interfaces.ExploreSubgraphResp, error) {
	s.called = true
	s.exploreReq = req
	if s.exploreResp != nil {
		return s.exploreResp, nil
	}
	return &interfaces.ExploreSubgraphResp{Objects: map[string]any{}}, nil
}

func TestHandleExploreSubgraph_ForwardsExplorationParams(t *testing.T) {
	convey.Convey("探索参数原样透传", t, func() {
		stub := &stubSubgraphService{}
		handler := handleExploreSubgraph(stub)

		_, err := handler(context.Background(), mcpReq(map[string]any{
			"kn_id":                   "kn-001",
			"source_object_type_id":   "purchase_order",
			"direction":               "bidirectional",
			"path_length":             2,
			"concept_groups":          []any{"cg-1"},
			"include_incomplete_path": true,
			"limit":                   25,
			"sort":                    []any{map[string]any{"field": "created_at", "direction": "desc"}},
			"response_format":         "json",
		}))
		convey.So(err, convey.ShouldBeNil)

		req := stub.exploreReq
		convey.So(req, convey.ShouldNotBeNil)
		convey.So(req.SourceObjectTypeID, convey.ShouldEqual, "purchase_order")
		convey.So(req.Direction, convey.ShouldEqual, "bidirectional")
		convey.So(req.PathLength, convey.ShouldEqual, 2)
		convey.So(req.ConceptGroups, convey.ShouldResemble, []string{"cg-1"})
		convey.So(req.IncludeIncompletePath, convey.ShouldBeTrue)
		convey.So(req.Limit, convey.ShouldEqual, 25)
		convey.So(len(req.Sort), convey.ShouldEqual, 1)
		convey.So(req.Sort[0].Field, convey.ShouldEqual, "created_at")
	})
}

// If the required fields are missing, please name them. A general "required" will let the model guess which one is missing and then fill it in at will.
func TestHandleExploreSubgraph_NamesTheMissingRequiredField(t *testing.T) {
	base := map[string]any{
		"kn_id":                 "kn-001",
		"source_object_type_id": "ot-1",
		"direction":             "forward",
		"path_length":           1,
		"response_format":       "json",
	}
	for _, missing := range []string{"kn_id", "source_object_type_id", "direction"} {
		convey.Convey("缺 "+missing+" 时错误信息点名", t, func() {
			stub := &stubSubgraphService{}
			args := map[string]any{}
			for k, v := range base {
				if k != missing {
					args[k] = v
				}
			}
			result, err := handleExploreSubgraph(stub)(context.Background(), mcpReq(args))
			convey.So(err, convey.ShouldBeNil)
			convey.So(stub.called, convey.ShouldBeFalse)
			convey.So(toolResultText(result), convey.ShouldContainSubstring, missing)
		})
	}
}

// path_length is int, 0 cannot distinguish between "not passed" and "0 passed". The downstream does not report an error for 0, but only returns an empty subgraph.
// The caller will read "the parameters are not filled in correctly" as "nothing is connected" - this is the worst kind of failure.
func TestHandleExploreSubgraph_RejectsZeroPathLength(t *testing.T) {
	convey.Convey("path_length 为 0 直接拒绝，不去下游拿空子图", t, func() {
		stub := &stubSubgraphService{}
		result, err := handleExploreSubgraph(stub)(context.Background(), mcpReq(map[string]any{
			"kn_id":                 "kn-001",
			"source_object_type_id": "ot-1",
			"direction":             "forward",
			"response_format":       "json",
		}))
		convey.So(err, convey.ShouldBeNil)
		convey.So(stub.called, convey.ShouldBeFalse)
		convey.So(toolResultText(result), convey.ShouldContainSubstring, "path_length")
	})
}

// The tool surface only allows exploration parameters; the two internal parameters and need_total should not appear in the schema.
func TestExploreSubgraphSchema_ShapeAndRequiredFields(t *testing.T) {
	convey.Convey("explore_subgraph schema 形状正确", t, func() {
		input, output := loadToolSchemas("explore_subgraph")

		var in struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		convey.So(json.Unmarshal(input, &in), convey.ShouldBeNil)

		for _, key := range []string{"source_object_type_id", "direction", "path_length",
			"condition", "concept_groups", "include_incomplete_path", "limit", "sort", "search_after"} {
			_, ok := in.Properties[key]
			convey.So(ok, convey.ShouldBeTrue)
		}
		for _, key := range []string{"exclude_system_properties", "ignoring_store_cache", "need_total",
			"relation_type_paths"} {
			_, ok := in.Properties[key]
			convey.So(ok, convey.ShouldBeFalse)
		}
		// bkn_context is added by offerBKNContext to the business tools during assembly and is not in the baseline file.
		convey.So(in.Required, convey.ShouldResemble,
			[]string{"kn_id", "source_object_type_id", "direction", "path_length", "bkn_context"})

		var out struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		convey.So(json.Unmarshal(output, &out), convey.ShouldBeNil)
		for _, key := range []string{"objects", "isolated_objects", "relation_paths", "total_count"} {
			_, ok := out.Properties[key]
			convey.So(ok, convey.ShouldBeTrue)
		}
	})
}

// The upper and lower bounds of path_length are written firmly in the schema, so that the model can fill it in correctly at the first time; if it is missing, you have to wait for 400 downstream.
func TestExploreSubgraphSchema_PinsPathLengthBounds(t *testing.T) {
	convey.Convey("path_length 带 1-3 的上下界", t, func() {
		input, _ := loadToolSchemas("explore_subgraph")
		var in struct {
			Properties struct {
				PathLength struct {
					Minimum *int `json:"minimum"`
					Maximum *int `json:"maximum"`
				} `json:"path_length"`
			} `json:"properties"`
		}
		convey.So(json.Unmarshal(input, &in), convey.ShouldBeNil)
		convey.So(in.Properties.PathLength.Minimum, convey.ShouldNotBeNil)
		convey.So(*in.Properties.PathLength.Minimum, convey.ShouldEqual, 1)
		convey.So(in.Properties.PathLength.Maximum, convey.ShouldNotBeNil)
		// Downstream validateSubgraphSearchRequest is >3, the schema must be consistent with it.
		convey.So(*in.Properties.PathLength.Maximum, convey.ShouldEqual, 3)
	})
}

// The baseline schema is Chinese, and English is supplemented by the locale overlay. If you omit the addition, no error will be reported, only the English client will.
// See the Chinese description - especially the prompt "This is a valid conclusion, not a failure" in isolated_objects.
func TestExploreSubgraphSchema_DescriptionsAreLocalized(t *testing.T) {
	convey.Convey("explore_subgraph 的描述有 en-US 覆盖", t, func() {
		bundle := loadMCPLocaleBundle("en-US")
		convey.So(bundle, convey.ShouldNotBeNil)

		replacements := bundle.schemaDescriptions["explore_subgraph"]
		convey.So(len(replacements), convey.ShouldBeGreaterThan, 0)

		input, output := loadToolSchemas("explore_subgraph")
		wrapped, err := marshalToolSchema(input, output)
		convey.So(err, convey.ShouldBeNil)
		var wrapper map[string]any
		convey.So(json.Unmarshal(wrapped, &wrapper), convey.ShouldBeNil)

		// Each description in the baseline must have a corresponding coverage entry, otherwise the English client will silently fall back to Chinese.
		for _, path := range collectDescriptionPaths(wrapper, nil) {
			// bkn_context is injected by offerBKNContext at assembly time and is not part of the baseline of this file.
			if strings.Contains(path, "bkn_context") {
				continue
			}
			convey.So(replacements[path], convey.ShouldNotBeBlank)
		}
	})
}

// collectDescriptionPaths collects the dotted paths of all descriptions in the schema, in the form.
// input_schema.properties.direction.description。
func collectDescriptionPaths(node any, prefix []string) []string {
	obj, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	paths := []string{}
	for key, value := range obj {
		next := append(append([]string{}, prefix...), key)
		if key == "description" {
			if _, isString := value.(string); isString {
				paths = append(paths, strings.Join(next, "."))
			}
			continue
		}
		paths = append(paths, collectDescriptionPaths(value, next)...)
	}
	return paths
}
