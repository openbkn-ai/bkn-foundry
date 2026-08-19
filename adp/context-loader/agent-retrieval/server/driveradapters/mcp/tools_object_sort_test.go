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

// sort must be able to tie all the way from the MCP parameters to the driven request, otherwise the "nearest N" problem model can only.
// Pull back the full amount and arrange it yourself.
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

// The tool surface only opens sort. exclude_system_properties and ignoring_store_cache are internal parameters:
// Which system fields are lost in the former depends on whether the caller wants to drill down later, while the latter is an escape channel when the index is abnormal (a bit slower)
// order of magnitude), it will be misused if left to model judgment.
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
		// need_total is unconditionally set to true by the driven adapter and is not an option on the caller's part.
		_, hasNeedTotal := schema.Properties["need_total"]
		convey.So(hasNeedTotal, convey.ShouldBeFalse)
	})
}

// The baseline schema is Chinese, and English is supplemented by the locale overlay. If you omit the locale entry for a new field, no error will be reported.
// Only English clients will see the Chinese description - pin the three items of sort here.
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
