// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package agent_operator

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func Test_mcpCallToolResult_normalize(t *testing.T) {
	Convey("Test mcpCallToolResult normalize", t, func() {
		Convey("单个 text 块内容为 JSON 对象时直接返回该对象", func() {
			r := mcpCallToolResult{
				Content: []map[string]any{
					{"type": "text", "text": `{"name":"tester","ok":true}`},
				},
			}

			parsed := r.normalize()
			So(parsed["name"], ShouldEqual, "tester")
			So(parsed["ok"], ShouldEqual, true)
		})

		Convey("text 块非 JSON 时拼接文本落在 text 字段", func() {
			r := mcpCallToolResult{
				Content: []map[string]any{
					{"type": "text", "text": "line1"},
					{"type": "text", "text": "line2"},
				},
			}

			So(r.normalize()["text"], ShouldEqual, "line1\nline2")
		})

		Convey("text 块内容为 JSON 数组时落在 items 字段", func() {
			r := mcpCallToolResult{
				Content: []map[string]any{
					{"type": "text", "text": `[{"id":1},{"id":2}]`},
				},
			}

			items, ok := r.normalize()["items"].([]any)
			So(ok, ShouldBeTrue)
			So(len(items), ShouldEqual, 2)
		})

		Convey("text 块内容为 JSON 标量时落在 text 字段", func() {
			r := mcpCallToolResult{
				Content: []map[string]any{
					{"type": "text", "text": "42"},
				},
			}

			So(r.normalize()["text"], ShouldEqual, "42")
		})

		Convey("含非 text 块时原样挂在 content 字段", func() {
			r := mcpCallToolResult{
				Content: []map[string]any{
					{"type": "image", "data": "base64..."},
				},
			}

			So(r.normalize()["content"], ShouldResemble, r.Content)
		})

		Convey("content 为空时结果仍是对象", func() {
			r := mcpCallToolResult{}

			So(r.normalize()["content"], ShouldResemble, r.Content)
		})
	})
}
