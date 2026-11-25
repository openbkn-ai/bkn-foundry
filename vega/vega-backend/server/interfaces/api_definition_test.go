// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
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
	require.NoError(t, jsoncodec.Unmarshal([]byte(source), &input))
	require.NoError(t, jsoncodec.Unmarshal([]byte(source), &original))

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
	require.NoError(t, jsoncodec.Unmarshal(encoded, &roundTrip))
	assert.Equal(t, definition, roundTrip, "动态 Schema、Body 和条件中的 null 与精确数值应无损往返")

	var output map[string]any
	require.NoError(t, jsoncodec.Unmarshal(encoded, &output))
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
			require.NoError(t, jsoncodec.Unmarshal([]byte(source), &definition))
			encoded, err := json.Marshal(definition)
			require.NoError(t, err)
			var expected, actual map[string]any
			require.NoError(t, jsoncodec.Unmarshal([]byte(source), &expected))
			require.NoError(t, jsoncodec.Unmarshal(encoded, &actual))
			assert.Equal(t, expected, actual, "省略可选字段，保留 false Schema 与任意 JSON 数据")
		})
	}
}

func TestHTTPResponsesDefinitionForStatus(t *testing.T) {
	exact := &interfaces.HTTPResponseDefinition{Schema: false}
	class := &interfaces.HTTPResponseDefinition{Schema: true}
	fallback := &interfaces.HTTPResponseDefinition{Schema: map[string]any{"type": "object"}}
	responses := &interfaces.HTTPResponsesDefinition{Statuses: map[string]*interfaces.HTTPResponseDefinition{"200": exact, "204": nil, "2XX": class, "default": fallback}}
	assert.Same(t, exact, responses.ForStatus(200), "精确项包括 false Schema 时不回退")
	assert.Nil(t, responses.ForStatus(204), "显式空项不回退")
	assert.Same(t, class, responses.ForStatus(201))
	assert.Same(t, fallback, responses.ForStatus(503))
	delete(responses.Statuses, "default")
	assert.Nil(t, responses.ForStatus(503))
	var absent *interfaces.HTTPResponsesDefinition
	assert.Nil(t, absent.ForStatus(200))
}

func TestAPIDefinitionSharedHTTPEntryPoints(t *testing.T) {
	const source = `{
		"request":{"method":"POST","path":"/items","parameters":[{"name":"sequence","value":9007199254740993}],"body":{"value":9007199254740993.123456789}},
		"response":{"content_type":"application/json","statuses":{
			"200":{"description":"items","schema":{"type":"array","items":{"type":"array","prefixItems":[{"type":"number","minimum":9007199254740993.123456789},{"type":"integer"},{"type":["string","null"]}]}},"result_rows":"","result_total":"$.count"},
			"206":{"description":"nested","schema":{"type":"object"},"result_rows":"$.items","result_total":""},
			"default":{"description":"rejected","schema":false}
		}},
		"success_rule":{"http_status":[200],"body_filter_condition":{"field":"optional","operation":"eq","value":null}},
		"variables":{
			"sequence":{"value":"9007199254740993","sensitive":false},
			"empty":{"value":"","sensitive":false},
			"literal":{"value":"{{ timeout }}","sensitive":false},
			"request_settings":{"value":{"limit":9007199254740993.123456789,"items":[null,false,0]},"sensitive":false}
		},
		"paging_rule":{"mode":"page","default_limit":2,"max_limit":5,"page_base":0}
	}`
	var input, original map[string]any
	require.NoError(t, jsoncodec.Unmarshal([]byte(source), &input))
	input["future_api"] = true
	require.NoError(t, jsoncodec.Unmarshal([]byte(source), &original))
	original["future_api"] = true
	var decoded interfaces.APIDefinition
	require.NoError(t, common.DecodeMapToStruct(input, &decoded))
	assert.Equal(t, original, input, "公共 mapstructure 解码保持调用方输入，并忽略未知字段")
	assert.Equal(t, "POST", decoded.Request.Method)
	assert.Equal(t, "application/json", decoded.Response.ContentType)
	assert.Equal(t, false, decoded.Response.Statuses["default"].Schema)
	require.NotNil(t, decoded.Response.Statuses["200"].ResultRows)
	require.NotNil(t, decoded.Response.Statuses["200"].ResultRows)
	assert.Empty(t, *decoded.Response.Statuses["200"].ResultRows)
	require.NotNil(t, decoded.Response.Statuses["206"].ResultRows)
	assert.Equal(t, "$.items", *decoded.Response.Statuses["206"].ResultRows)
	assert.Equal(t, new("$.count"), decoded.Response.Statuses["200"].ResultTotal)
	assert.Equal(t, new(""), decoded.Response.Statuses["206"].ResultTotal)
	assert.Equal(t, json.Number("9007199254740993"), decoded.Request.Parameters[0]["value"])
	assert.Equal(t, json.Number("9007199254740993.123456789"), decoded.Request.Body["value"])
	assert.Equal(t, "9007199254740993", decoded.Variables["sequence"].Value)
	assert.False(t, decoded.Variables["sequence"].Sensitive)
	assert.Equal(t, "", decoded.Variables["empty"].Value)
	assert.Equal(t, "{{ timeout }}", decoded.Variables["literal"].Value)
	assert.Equal(t, map[string]any{
		"limit": json.Number("9007199254740993.123456789"),
		"items": []any{nil, false, json.Number("0")},
	}, decoded.Variables["request_settings"].Value)
	assert.Contains(t, decoded.SuccessRule.BodyFilterCondition, "value")
	assert.Nil(t, decoded.SuccessRule.BodyFilterCondition["value"])
	require.NotNil(t, decoded.PagingRule.PageBase)
	assert.Zero(t, *decoded.PagingRule.PageBase)

	var request interfaces.ResourceRequest
	require.NoError(t, common.BindJSON(strings.NewReader(`{"name":"items","category":"api","schema_definition":[{"name":"amount","type":"float"},{"name":"id","type":"integer"},{"name":"optional","type":"string"}],"api_definition":`+source+`}`), &request))
	require.NotNil(t, request.APIDefinition)
	require.Len(t, request.SchemaDefinition, 3)
	assert.Equal(t, []string{"amount", "id", "optional"}, []string{request.SchemaDefinition[0].Name, request.SchemaDefinition[1].Name, request.SchemaDefinition[2].Name})
	assert.Equal(t, &decoded, request.APIDefinition, "实际 Resource 绑定和公共 mapstructure 解码共用同一 HTTP 结构")
	encoded, err := json.Marshal(request.APIDefinition)
	require.NoError(t, err)
	var shape map[string]any
	require.NoError(t, jsoncodec.Unmarshal(encoded, &shape))
	assert.Equal(t, original["variables"], shape["variables"])
	assert.NotContains(t, shape, "operation")
	assert.NotContains(t, shape, "HTTPOperationDefinition")
	assert.NotContains(t, shape, "future_api")
	response := shape["response"].(map[string]any)
	assert.Equal(t, "application/json", response["content_type"])
	assert.Contains(t, response, "statuses")
	assert.NotContains(t, response, "200")
	assert.NotContains(t, shape["paging_rule"], "response")
	assert.NotContains(t, response["statuses"].(map[string]any)["200"], "content")
	assert.Equal(t, "", response["statuses"].(map[string]any)["200"].(map[string]any)["result_rows"])
	assert.NotContains(t, response["statuses"].(map[string]any)["200"], "result")
	assert.NotContains(t, response["statuses"].(map[string]any)["default"], "result_rows")
}

func TestAPIDefinitionUnmarshalJSON(t *testing.T) {
	t.Run("distinguishes explicit empty response paths from omitted paths in JSON v2", func(t *testing.T) {
		empty := ""
		missing := interfaces.HTTPResponseDefinition{Description: "rows", Schema: true}
		encoded, err := jsoncodec.Marshal(missing)
		require.NoError(t, err)
		for _, tc := range []struct {
			field string
			rows  *string
			total *string
		}{
			{field: "result_rows", rows: &empty},
			{field: "result_total", total: &empty},
		} {
			t.Run(tc.field, func(t *testing.T) {
				explicit := missing
				explicit.ResultRows = tc.rows
				explicit.ResultTotal = tc.total
				withPath, err := jsoncodec.Marshal(explicit)
				require.NoError(t, err)
				assert.Contains(t, string(withPath), `"`+tc.field+`":""`)
				assert.NotContains(t, string(encoded), `"`+tc.field+`"`)
				assert.False(t, common.JSONNumbersEqual(explicit, missing))
			})
		}
	})

	t.Run("preserves public HTTP shapes and exact resource construction values", func(t *testing.T) {
		var definition interfaces.APIDefinition
		source := `{"request":{"method":"POST","path":"read","body":{"type":"object","properties":{"amount":{"type":"number","value":12345678901234567890.123456789}}}},"response":{"content_type":"application/json","statuses":{"200":{"description":"rows","schema":true,"result_rows":""},"default":{"description":"rejected","schema":false}}},"success_rule":{"http_status":[200],"body_filter_condition":{"field":"optional","operation":"eq","value":null}},"paging_rule":{"mode":"page","default_limit":1,"max_limit":10,"page_base":0}}`
		require.NoError(t, json.Unmarshal([]byte(source), &definition))
		properties := definition.Request.Body["properties"].(map[string]any)
		assert.Equal(t, json.Number("12345678901234567890.123456789"), properties["amount"].(map[string]any)["value"])
		require.NotNil(t, definition.PagingRule.PageBase)
		assert.Zero(t, *definition.PagingRule.PageBase)
		assert.Equal(t, "application/json", definition.Response.ContentType)
		require.NotNil(t, definition.Response.Statuses["200"].ResultRows)
		assert.Empty(t, *definition.Response.Statuses["200"].ResultRows)
		assert.Equal(t, false, definition.Response.Statuses["default"].Schema)
		assert.Contains(t, definition.SuccessRule.BodyFilterCondition, "value")
		assert.Nil(t, definition.SuccessRule.BodyFilterCondition["value"])
		encoded, err := json.Marshal(definition)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), `12345678901234567890.123456789`)
		assert.Contains(t, string(encoded), `"value":null`)
		assert.Contains(t, string(encoded), `"page_base":0`)
		assert.Contains(t, string(encoded), `"result_rows":""`)
		var shape map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(encoded, &shape))
		assert.Contains(t, shape, "request")
		assert.Contains(t, shape, "response")
		assert.Contains(t, shape, "success_rule")
		assert.NotContains(t, shape, "HTTPOperationDefinition")
		assert.NotContains(t, shape, "operation")
	})
	t.Run("ignores unknown members at every level", func(t *testing.T) {
		const input = `{
			"input_schema": {},
			"request": {"method":"GET","path":"read","url":"https://example.invalid"},
			"response": {"content_type":"application/json","statuses":{"200":{"description":"rows","schema":true,"result":{"pointer":"/rows"}}}},
			"paging_rule": {"mode":"offset","default_limit":2,"max_limit":5,"response":{"total":"$.total"}}
		}`
		const known = `{
			"request": {"method":"GET","path":"read"},
			"response": {"content_type":"application/json","statuses":{"200":{"description":"rows","schema":true}}},
			"paging_rule": {"mode":"offset","default_limit":2,"max_limit":5}
		}`
		var actual, expected interfaces.APIDefinition
		require.NoError(t, json.Unmarshal([]byte(input), &actual))
		require.NoError(t, json.Unmarshal([]byte(known), &expected))
		assert.Equal(t, expected, actual)
	})
	for name, source := range map[string]string{
		"invalid result_total type": `{"response":{"statuses":{"200":{"result_total":false}}}}`,
		"invalid header type":       `{"request":{"method":"GET","path":"read","headers":{"Authorization":false}}}`,
		"invalid result_rows type":  `{"response":{"content_type":"application/json","statuses":{"200":{"description":"rows","schema":true,"result_rows":false}}}}`,
		"invalid paging limit":      `{"paging_rule":{"default_limit":false}}`,
		"non-object definition":     `[]`,
		"duplicate member":          `{"request":{"path":"read","path":"other"}}`,
		"multiple JSON values":      `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			original := interfaces.APIDefinition{HTTPOperationDefinition: interfaces.HTTPOperationDefinition{Request: &interfaces.HTTPRequestDefinition{Method: "GET", Path: "unchanged"}}}
			err := json.Unmarshal([]byte(source), &original)
			require.Error(t, err)
			assert.Equal(t, "unchanged", original.Request.Path)
		})
	}
	t.Run("public HTTP zero values and status semantics are left to consumers", func(t *testing.T) {
		var definition interfaces.APIDefinition
		require.NoError(t, json.Unmarshal([]byte(`{"request":{"method":"GET","path":"read","headers":{"Authorization":null}},"response":{"content_type":"application/json","statuses":{"default":{"description":"missing schema","result_rows":null,"result_total":null}}},"success_rule":{"http_status":[200,200.5,"200",true,null],"body_filter_condition":null},"paging_rule":null}`), &definition))
		assert.Equal(t, "", definition.Request.Headers["Authorization"])
		assert.Nil(t, definition.Response.Statuses["default"].Schema)
		assert.Nil(t, definition.Response.Statuses["default"].ResultRows)
		assert.Nil(t, definition.Response.Statuses["default"].ResultTotal)
		encoded, err := json.Marshal(definition)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), `"result_total"`)
		assert.NotContains(t, string(encoded), `"result_has_more"`)
		assert.NotContains(t, string(encoded), `"result_rows"`, "未配置及null路径不输出占位字段")
		assert.Equal(t, []any{json.Number("200"), json.Number("200.5"), "200", true, nil}, definition.SuccessRule.HTTPStatus)
		assert.Nil(t, definition.SuccessRule.BodyFilterCondition)
		assert.Nil(t, definition.PagingRule)
	})
}
