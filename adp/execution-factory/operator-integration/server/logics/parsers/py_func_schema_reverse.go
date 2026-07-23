// Package parsers 函数元数据解析
// @file py_func_schema_reverse.go
// @description: 从函数工具的 OpenAPI 规格反解出参数定义
package parsers

import (
	"github.com/getkin/kin-openapi/openapi3"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/utils"
)

// 函数工具的参数定义在落库时被展开成 OpenAPI 规格,存进单列 f_api_spec。
// 读回时调用方需要的是原始的 ParameterDef 形状（含 required 与嵌套的 sub_parameters）,
// 因此这里做一次反向解析。规格由 createRequestBody / createResponses 生成,
// 结构固定,不必处理任意 OpenAPI 文档。

const (
	applicationJSON = "application/json"
	statusOK        = "200"
	resultProperty  = "result"
	itemsProperty   = "items"
)

// FunctionParamsFromAPISpec 从函数工具的 API 规格还原出输入、输出参数定义。
// 规格缺失或结构不符时返回空切片,由调用方按「没有参数」处理。
func FunctionParamsFromAPISpec(apiSpec string) (inputs, outputs []*interfaces.ParameterDef) {
	if apiSpec == "" {
		return nil, nil
	}
	spec := utils.JSONToObject[interfaces.APISpec](apiSpec)
	return inputsFromRequestBody(spec.RequestBody), outputsFromResponses(spec.Responses)
}

// inputsFromRequestBody 输入参数来自请求体 schema 的 properties。
func inputsFromRequestBody(requestBody *interfaces.RequestBody) []*interfaces.ParameterDef {
	if requestBody == nil {
		return nil
	}
	content, ok := requestBody.Content[applicationJSON]
	if !ok || content == nil || content.Schema == nil {
		return nil
	}
	return paramsFromSchema(content.Schema.Value)
}

// outputsFromResponses 输出参数嵌在 200 响应的 result 属性下。
func outputsFromResponses(responses []*interfaces.Response) []*interfaces.ParameterDef {
	var response *interfaces.Response
	for _, item := range responses {
		if item != nil && item.StatusCode == statusOK {
			response = item
			break
		}
	}
	if response == nil {
		return nil
	}
	content, ok := response.Content[applicationJSON]
	if !ok || content == nil || content.Schema == nil {
		return nil
	}
	root := content.Schema.Value
	if root == nil || root.Properties == nil {
		return nil
	}
	result, ok := root.Properties[resultProperty]
	if !ok || result == nil {
		return nil
	}
	return paramsFromSchema(result.Value)
}

// paramsFromSchema 把一个 object schema 的每个属性还原成一条参数定义。
func paramsFromSchema(schema *openapi3.Schema) []*interfaces.ParameterDef {
	if schema == nil || len(schema.Properties) == 0 {
		return nil
	}
	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}
	params := make([]*interfaces.ParameterDef, 0, len(schema.Properties))
	for name, ref := range schema.Properties {
		if ref == nil {
			continue
		}
		params = append(params, paramFromSchema(name, ref.Value, required[name]))
	}
	sortParamsByName(params)
	return params
}

// paramFromSchema 还原单条参数,并按类型递归展开嵌套结构。
func paramFromSchema(name string, schema *openapi3.Schema, required bool) *interfaces.ParameterDef {
	param := &interfaces.ParameterDef{Name: name, Required: required}
	if schema == nil {
		return param
	}
	param.Type = mapOpenAPIToType(schema.Type)
	param.Description = schema.Description
	param.Default = schema.Default
	param.Enum = schema.Enum
	param.Example = schema.Example

	switch param.Type {
	case interfaces.ParameterTypeObject:
		// object 的属性即子参数
		param.SubParameters = paramsFromSchema(schema)
	case interfaces.ParameterTypeArray:
		// array 的元素结构存放在 items,约定还原成单个名为 items 的子参数
		if schema.Items != nil && schema.Items.Value != nil {
			param.SubParameters = []*interfaces.ParameterDef{
				paramFromSchema(itemsProperty, schema.Items.Value, true),
			}
		}
	}
	return param
}

// mapOpenAPIToType 是 mapTypeToOpenAPI 的逆映射。
// OpenAPI 的 integer 与 number 在参数定义里统一为 number。
func mapOpenAPIToType(types *openapi3.Types) interfaces.ParameterType {
	if types == nil || len(*types) == 0 {
		return interfaces.ParameterTypeString
	}
	switch (*types)[0] {
	case openapi3.TypeInteger, openapi3.TypeNumber:
		return interfaces.ParameterTypeNumber
	case openapi3.TypeBoolean:
		return interfaces.ParameterTypeBoolean
	case openapi3.TypeArray:
		return interfaces.ParameterTypeArray
	case openapi3.TypeObject:
		return interfaces.ParameterTypeObject
	default:
		return interfaces.ParameterTypeString
	}
}

// sortParamsByName 让顺序稳定。schema 的 properties 是 map,遍历顺序随机,
// 不排序的话同一个工具每次读取返回的参数顺序都不同。
func sortParamsByName(params []*interfaces.ParameterDef) {
	for i := 1; i < len(params); i++ {
		for j := i; j > 0 && params[j].Name < params[j-1].Name; j-- {
			params[j], params[j-1] = params[j-1], params[j]
		}
	}
}
