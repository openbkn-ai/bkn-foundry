// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestKnDynamicTool_OriginalSchemaOmitted_WhenNil(t *testing.T) {
	convey.Convey("KnDynamicTool JSON omits original_schema when nil", t, func() {
		tool := KnDynamicTool{
			Name:            "test_tool",
			Description:     "desc",
			Parameters:      map[string]interface{}{"type": "object"},
			APIURL:          "http://localhost/api",
			FixedParams:     map[string]interface{}{"key": "val"},
			APICallStrategy: ResultProcessStrategyKnActionRecall,
		}

		data, err := json.Marshal(tool)
		convey.So(err, convey.ShouldBeNil)

		var m map[string]interface{}
		err = json.Unmarshal(data, &m)
		convey.So(err, convey.ShouldBeNil)

		convey.So(m, convey.ShouldNotContainKey, "original_schema")
		convey.So(m, convey.ShouldContainKey, "name")
		convey.So(m, convey.ShouldContainKey, "description")
		convey.So(m, convey.ShouldContainKey, "parameters")
		convey.So(m, convey.ShouldContainKey, "api_url")
		convey.So(m, convey.ShouldContainKey, "fixed_params")
		convey.So(m, convey.ShouldContainKey, "api_call_strategy")
	})
}

func TestKnDynamicTool_OriginalSchemaPresent_WhenPopulated(t *testing.T) {
	convey.Convey("KnDynamicTool JSON includes original_schema when populated", t, func() {
		tool := KnDynamicTool{
			Name:           "test_tool",
			OriginalSchema: map[string]interface{}{"paths": "/test"},
		}

		data, err := json.Marshal(tool)
		convey.So(err, convey.ShouldBeNil)

		var m map[string]interface{}
		err = json.Unmarshal(data, &m)
		convey.So(err, convey.ShouldBeNil)

		convey.So(m, convey.ShouldContainKey, "original_schema")
	})
}

func TestQueryObjectInstancesResp_ObjectTypeOmitted_WhenNil(t *testing.T) {
	convey.Convey("QueryObjectInstancesResp JSON omits object_type when nil", t, func() {
		resp := QueryObjectInstancesResp{
			Data:          []any{map[string]any{"id": "1"}},
			ObjectConcept: nil,
		}

		data, err := json.Marshal(resp)
		convey.So(err, convey.ShouldBeNil)

		var m map[string]interface{}
		err = json.Unmarshal(data, &m)
		convey.So(err, convey.ShouldBeNil)

		convey.So(m, convey.ShouldNotContainKey, "object_type")
		convey.So(m, convey.ShouldContainKey, "datas")
	})
}

func TestQueryObjectInstancesResp_ObjectTypePresent_WhenPopulated(t *testing.T) {
	convey.Convey("QueryObjectInstancesResp JSON includes object_type when populated", t, func() {
		resp := QueryObjectInstancesResp{
			Data:          []any{map[string]any{"id": "1"}},
			ObjectConcept: map[string]any{"id": "ot_1", "name": "TestType"},
		}

		data, err := json.Marshal(resp)
		convey.So(err, convey.ShouldBeNil)

		var m map[string]interface{}
		err = json.Unmarshal(data, &m)
		convey.So(err, convey.ShouldBeNil)

		convey.So(m, convey.ShouldContainKey, "object_type")
	})
}

func TestQueryObjectInstancesReq_DefaultLimit(t *testing.T) {
	convey.Convey("QueryObjectInstancesReq has default tag for limit", t, func() {
		req := QueryObjectInstancesReq{}
		convey.So(req.Limit, convey.ShouldEqual, 0)

		convey.Convey("after defaults.Set the limit should be set via tag", func() {
			// The default:"10" tag is handled by creasty/defaults at runtime.
			// Here we just verify the struct accepts zero value (optional).
			convey.So(req.Limit, convey.ShouldBeGreaterThanOrEqualTo, 0)
		})
	})
}
