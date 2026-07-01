// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"encoding/json"

	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/client"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/model"
)

func functionCodeFromToolMetadata(tool *client.ToolDetail) string {
	metadata := functionMetadataFromTool(tool)
	return metadata.Code
}

type functionToolMetadata struct {
	Code    string
	Inputs  []model.FunctionParameterDef
	Outputs []model.FunctionParameterDef
}

func functionMetadataFromTool(tool *client.ToolDetail) functionToolMetadata {
	metadata := functionToolMetadata{}
	if tool == nil {
		return metadata
	}

	if tool.MetadataType == "function" && tool.Metadata != nil && len(tool.Metadata.APISpec) > 0 {
		if parsed := parseFunctionMetadataObject(tool.Metadata.APISpec); parsed.Code != "" || len(parsed.Inputs) > 0 || len(parsed.Outputs) > 0 {
			metadata = mergeFunctionMetadata(metadata, parsed)
		}
		if parsed := parseFunctionMetadataFromAPISpec(tool.Metadata.APISpec); len(parsed.Inputs) > 0 || len(parsed.Outputs) > 0 {
			metadata = mergeFunctionMetadata(metadata, parsed)
		}
	}

	if tool.MetadataType == "function" && tool.Metadata != nil && tool.Metadata.FunctionContent != nil {
		metadata = mergeFunctionMetadata(metadata, functionToolMetadata{
			Code: tool.Metadata.FunctionContent.Code,
		})
	}

	if tool.ResourceObject != "" {
		if parsed := parseFunctionMetadataObject([]byte(tool.ResourceObject)); parsed.Code != "" || len(parsed.Inputs) > 0 || len(parsed.Outputs) > 0 {
			metadata = mergeFunctionMetadata(metadata, parsed)
		}
	}

	return metadata
}

func parseFunctionMetadataFromAPISpec(raw []byte) functionToolMetadata {
	var apiSpec map[string]interface{}
	if err := json.Unmarshal(raw, &apiSpec); err != nil {
		return functionToolMetadata{}
	}

	return functionToolMetadata{
		Inputs:  functionParamsFromSchemaProperties(schemaPropertiesAt(apiSpec, "request_body", "content", "application/json", "schema", "properties")),
		Outputs: functionOutputParamsFromResponses(apiSpec["responses"]),
	}
}

func functionOutputParamsFromResponses(raw interface{}) []model.FunctionParameterDef {
	responses, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	for _, item := range responses {
		response, ok := item.(map[string]interface{})
		if !ok || stringValue(response["status_code"]) != "200" {
			continue
		}
		properties := schemaPropertiesAt(response, "content", "application/json", "schema", "properties", "result", "properties")
		if len(properties) == 0 {
			properties = schemaPropertiesAt(response, "content", "application/json", "schema", "properties")
		}
		return functionParamsFromSchemaProperties(properties)
	}

	return nil
}

func schemaPropertiesAt(data map[string]interface{}, path ...string) map[string]interface{} {
	var current interface{} = data
	for _, key := range path {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current = object[key]
	}

	properties, _ := current.(map[string]interface{})
	return properties
}

func mergeFunctionMetadata(base, next functionToolMetadata) functionToolMetadata {
	if base.Code == "" {
		base.Code = next.Code
	}
	if len(base.Inputs) == 0 {
		base.Inputs = next.Inputs
	}
	if len(base.Outputs) == 0 {
		base.Outputs = next.Outputs
	}
	return base
}

func parseFunctionMetadataObject(raw []byte) functionToolMetadata {
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return functionToolMetadata{}
	}

	metadata := functionToolMetadata{
		Code:    stringValue(data["code"]),
		Inputs:  functionParamsFromAny(data["inputs"]),
		Outputs: functionParamsFromAny(data["outputs"]),
	}

	if functionInput, ok := data["function_input"].(map[string]interface{}); ok {
		metadata = mergeFunctionMetadata(metadata, functionToolMetadata{
			Code:    stringValue(functionInput["code"]),
			Inputs:  functionParamsFromAny(functionInput["inputs"]),
			Outputs: functionParamsFromAny(functionInput["outputs"]),
		})
	}

	return metadata
}

func functionParamsFromAny(raw interface{}) []model.FunctionParameterDef {
	items, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	params := make([]model.FunctionParameterDef, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name := stringValue(object["name"])
		if name == "" {
			continue
		}
		params = append(params, model.FunctionParameterDef{
			Name:        name,
			Type:        stringValue(object["type"]),
			Description: stringValue(object["description"]),
		})
	}

	return params
}

func functionParamsFromSchemaProperties(properties map[string]interface{}) []model.FunctionParameterDef {
	if len(properties) == 0 {
		return nil
	}

	params := make([]model.FunctionParameterDef, 0, len(properties))
	for name, raw := range properties {
		schema, _ := raw.(map[string]interface{})
		params = append(params, model.FunctionParameterDef{
			Name:        name,
			Type:        stringValue(schema["type"]),
			Description: stringValue(schema["description"]),
		})
	}

	return params
}

func stringValue(raw interface{}) string {
	value, _ := raw.(string)
	return value
}
