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

// toolResultText 把工具返回里的文本拼起来，用于断言错误信息点到了具体字段名。
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

// 必填项缺失要点名，一句笼统的 "required" 会让模型猜是哪个漏了、然后随便补一个。
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

// path_length 是 int，0 分不清「没传」和「传了 0」。下游对 0 不报错、只回空子图，
// 调用方会把「参数没填对」读成「什么都没连上」——这是最坏的一种失败。
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

// 工具面只开放探索参数；两个内部参数与 need_total 不该出现在 schema 里。
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
		// bkn_context 是 offerBKNContext 在装配时给业务工具统一追加的，不在基线文件里
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

// path_length 的上下界写死在 schema 里，模型才可能一次填对；漏了就得等下游 400。
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
		// 下游 validateSubgraphSearchRequest 拦 >3，schema 必须与它一致
		convey.So(*in.Properties.PathLength.Maximum, convey.ShouldEqual, 3)
	})
}

// 基线 schema 是中文，英文靠 locale 覆盖层补。漏加不会报错，只会让英文客户端
// 看到中文描述——尤其是 isolated_objects 那条「这是有效结论不是失败」的提示。
func TestExploreSubgraphSchema_DescriptionsAreLocalized(t *testing.T) {
	convey.Convey("explore_subgraph 的描述有 en-US 覆盖", t, func() {
		bundle := loadMCPLocaleBundle("en-US")
		convey.So(bundle, convey.ShouldNotBeNil)

		replacements := bundle.schemaDescriptions["explore_subgraph"]
		convey.So(len(replacements), convey.ShouldBeGreaterThan, 0)

		input, output := loadToolSchemas("explore_subgraph")
		var wrapper map[string]any
		convey.So(json.Unmarshal(mustMarshalToolSchema(input, output), &wrapper), convey.ShouldBeNil)

		// 基线里每一条 description 都要有对应的覆盖条目，否则英文客户端静默回落中文
		for _, path := range collectDescriptionPaths(wrapper, nil) {
			// bkn_context 由 offerBKNContext 在装配时注入，不属于本文件的基线
			if strings.Contains(path, "bkn_context") {
				continue
			}
			convey.So(replacements[path], convey.ShouldNotBeBlank)
		}
	})
}

// collectDescriptionPaths 收集 schema 里所有 description 的点分路径，形如
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
