// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// sort 得能从 MCP 参数一路绑到 driven 请求上，否则「最近的 N 条」这类问题模型只能
// 拉回全量自己排。
func TestHandleQueryObjectInstance_ForwardsSort(t *testing.T) {
	convey.Convey("handleQueryObjectInstance 透传 sort", t, func() {
		stub := &stubOntologyQuery{
			resp: &interfaces.QueryObjectInstancesResp{
				Data: []any{map[string]any{"id": "inst_1"}},
			},
		}

		handler := handleQueryObjectInstance(stub)
		req := mcpReq(map[string]any{
			"kn_id": "kn-001",
			"ot_id": "ot-001",
			"sort": []any{
				map[string]any{"field": "created_at", "direction": "desc"},
			},
			"limit":           10,
			"response_format": "json",
		})

		_, err := handler(context.Background(), req)
		convey.So(err, convey.ShouldBeNil)

		convey.So(stub.req, convey.ShouldNotBeNil)
		convey.So(len(stub.req.Sort), convey.ShouldEqual, 1)
		convey.So(stub.req.Sort[0].Field, convey.ShouldEqual, "created_at")
		convey.So(stub.req.Sort[0].Direction, convey.ShouldEqual, "desc")
	})
}

// 工具面只开放 sort。exclude_system_properties 与 ignoring_store_cache 是内部参数：
// 前者丢哪些系统字段取决于调用方后续要不要下钻，后者是索引异常时的逃生通道（慢一个
// 数量级），交给模型判断都会被误用。
func TestQueryObjectInstanceSchema_ExposesSortButNotInternalParams(t *testing.T) {
	convey.Convey("query_object_instance schema 只开放 sort", t, func() {
		input, _ := loadToolSchemas("query_object_instance")

		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		convey.So(json.Unmarshal(input, &schema), convey.ShouldBeNil)

		_, hasSort := schema.Properties["sort"]
		convey.So(hasSort, convey.ShouldBeTrue)

		_, hasExclude := schema.Properties["exclude_system_properties"]
		convey.So(hasExclude, convey.ShouldBeFalse)
		_, hasIgnoring := schema.Properties["ignoring_store_cache"]
		convey.So(hasIgnoring, convey.ShouldBeFalse)
		// need_total 由 driven adapter 无条件置 true，不是调用方的选项
		_, hasNeedTotal := schema.Properties["need_total"]
		convey.So(hasNeedTotal, convey.ShouldBeFalse)
	})
}

// 基线 schema 是中文，英文靠 locale 覆盖层补。新增字段漏加 locale 条目不会报错，
// 只会让英文客户端看到中文描述——这里把 sort 的三条钉住。
func TestQueryObjectInstanceSchema_SortDescriptionsAreLocalized(t *testing.T) {
	convey.Convey("sort 的描述有 en-US 覆盖", t, func() {
		bundle := loadMCPLocaleBundle("en-US")
		convey.So(bundle, convey.ShouldNotBeNil)

		replacements := bundle.schemaDescriptions["query_object_instance"]
		for _, path := range []string{
			"input_schema.properties.sort.description",
			"input_schema.properties.sort.items.properties.field.description",
			"input_schema.properties.sort.items.properties.direction.description",
			"output_schema.properties.total_count.description",
		} {
			convey.So(replacements[path], convey.ShouldNotBeBlank)
		}
	})
}
