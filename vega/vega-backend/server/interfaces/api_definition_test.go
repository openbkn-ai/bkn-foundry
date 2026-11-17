// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestHTTPOperationDefinitionMapDecode(t *testing.T) {
	const source = `{
		"request": {
			"method": "POST", "path": "/login", "Method": "GET",
			"headers": {"X-Tenant": "{{ tenant }}"},
			"parameters": [{"name": "amount", "value": 9007199254740993.123456789, "default_value": null}],
			"body": {"type": "object", "properties": {
				"content": {"type": "string", "value": "business data"},
				"nullable": {"value": null},
				"amount": {"value": 9007199254740993.123456789}
			}},
			"future_request": true
		},
		"response": {
			"content_type": "application/json",
			"headers": {"X-Common": "ignored"},
			"statuses": {
				"200": {
					"description": "ok",
					"schema": {"type": ["object", "null"], "const": {"value": null}, "minimum": 9007199254740993.123456789},
					"headers": {"X-Count": {"example": 9007199254740993}},
					"future_response": true
				},
				"default": {
					"description": "reject", "schema": false,
					"content": {"application/json": {"schema": true}}, "content_type": null
				}
			}
		},
		"success_rule": {
			"http_status": [200, 200.5, "200", true, null],
			"body_filter_condition": {"operation": "and", "sub_conditions": [
				{"field": "value", "operation": "eq", "value": null},
				{"field": "amount", "operation": "eq", "value": 9007199254740993.123456789}
			]},
			"future_rule": true
		},
		"future_login": true
	}`
	var input, original map[string]any
	require.NoError(t, common.UnmarshalPreciseJSON([]byte(source), &input))
	require.NoError(t, common.UnmarshalPreciseJSON([]byte(source), &original))

	var definition interfaces.HTTPOperationDefinition
	require.NoError(t, common.DecodeMapToStruct(input, &definition))
	require.NotNil(t, definition.Request)
	require.NotNil(t, definition.Response)
	require.NotNil(t, definition.SuccessRule)
	require.NotNil(t, definition.Response.Statuses["200"])
	require.NotNil(t, definition.Response.Statuses["default"])
	assert.Equal(t, original, input, "解码不得修改调用方配置")

	request := input["request"].(map[string]any)
	assert.Equal(t, "POST", definition.Request.Method, "固定字段精确匹配大小写")
	assert.Equal(t, "/login", definition.Request.Path)
	assert.Equal(t, map[string]string{"X-Tenant": "{{ tenant }}"}, definition.Request.Headers)
	assert.Equal(t, request["body"], definition.Request.Body)
	assert.Equal(t, json.Number("9007199254740993.123456789"), definition.Request.Parameters[0]["value"])
	assert.Contains(t, definition.Request.Parameters[0], "default_value")
	assert.Nil(t, definition.Request.Parameters[0]["default_value"])
	response := input["response"].(map[string]any)["statuses"].(map[string]any)["200"].(map[string]any)
	assert.Equal(t, response["schema"], definition.Response.Statuses["200"].Schema)
	assert.Equal(t, response["headers"], definition.Response.Statuses["200"].Headers)
	assert.Equal(t, false, definition.Response.Statuses["default"].Schema, "忽略旧字段不得覆盖 false Schema")
	assert.Nil(t, definition.Response.Statuses["default"].Headers, "集合级未知 headers 不继承到状态项")

	rule := input["success_rule"].(map[string]any)
	assert.Equal(t, rule["http_status"], definition.SuccessRule.HTTPStatus, "保留原始非法类型供消费者校验")
	assert.Equal(t, rule["body_filter_condition"], definition.SuccessRule.BodyFilterCondition)
	assert.Contains(t, definition.SuccessRule.BodyFilterCondition["sub_conditions"].([]any)[0], "value")

	encoded, err := json.Marshal(definition)
	require.NoError(t, err)
	var roundTrip interfaces.HTTPOperationDefinition
	require.NoError(t, common.UnmarshalPreciseJSON(encoded, &roundTrip))
	assert.Equal(t, definition, roundTrip, "动态 Schema、Body 和条件中的 null 与精确数值应无损往返")

	var output map[string]any
	require.NoError(t, common.UnmarshalPreciseJSON(encoded, &output))
	assert.NotContains(t, output, "future_login")
	assert.NotContains(t, output["request"], "future_request")
	outputResponse := output["response"].(map[string]any)
	assert.NotContains(t, outputResponse, "headers")
	statuses := outputResponse["statuses"].(map[string]any)
	assert.NotContains(t, statuses["200"], "future_response")
	assert.NotContains(t, statuses["default"], "content")
	assert.NotContains(t, statuses["default"], "content_type")
	assert.NotContains(t, output["success_rule"], "future_rule")
	assert.Contains(t, string(encoded), "9007199254740993.123456789")
}

func TestHTTPOperationDefinitionJSONRoundTrip(t *testing.T) {
	for _, schema := range []string{`false`, `true`, `{"const":null,"enum":[0,false,"",9007199254740993.123456789]}`} {
		t.Run(schema, func(t *testing.T) {
			source := `{"request":{"method":"GET","path":"/login"},` +
				`"response":{"content_type":"application/json","statuses":{"default":{"description":"response","schema":` + schema + `}}},` +
				`"success_rule":{"http_status":[200]}}`
			var definition interfaces.HTTPOperationDefinition
			require.NoError(t, common.UnmarshalPreciseJSON([]byte(source), &definition))
			encoded, err := json.Marshal(definition)
			require.NoError(t, err)
			var expected, actual map[string]any
			require.NoError(t, common.UnmarshalPreciseJSON([]byte(source), &expected))
			require.NoError(t, common.UnmarshalPreciseJSON(encoded, &actual))
			assert.Equal(t, expected, actual, "省略可选字段，保留 false Schema 与任意 JSON 数据")
		})
	}
}
