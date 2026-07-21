// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/model"
)

func TestFunctionMetadataFromToolReadsInputsAndOutputs(t *testing.T) {
	resourceObject := map[string]interface{}{
		"function_input": map[string]interface{}{
			"code": "def handler(event):\n    return event\n",
			"inputs": []map[string]interface{}{
				{"name": "city", "type": "string", "description": "city name"},
				{"name": "days", "type": "number"},
			},
			"outputs": []map[string]interface{}{
				{"name": "summary", "type": "string"},
			},
		},
	}
	raw, err := json.Marshal(resourceObject)
	if err != nil {
		t.Fatal(err)
	}

	metadata := functionMetadataFromTool(&client.ToolDetail{
		MetadataType:   "function",
		ResourceObject: string(raw),
	})

	if metadata.Code == "" {
		t.Fatalf("expected code to be restored")
	}
	if len(metadata.Inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(metadata.Inputs))
	}
	if metadata.Inputs[0].Name != "city" || metadata.Inputs[0].Type != "string" {
		t.Fatalf("unexpected first input: %+v", metadata.Inputs[0])
	}
	if len(metadata.Outputs) != 1 || metadata.Outputs[0].Name != "summary" {
		t.Fatalf("unexpected outputs: %+v", metadata.Outputs)
	}
}

func TestFunctionMetadataFromToolReadsReturnedAPISpec(t *testing.T) {
	apiSpec := map[string]interface{}{
		"request_body": map[string]interface{}{
			"content": map[string]interface{}{
				"application/json": map[string]interface{}{
					"schema": map[string]interface{}{
						"properties": map[string]interface{}{
							"count": map[string]interface{}{"type": "number", "description": "Counter value"},
							"label": map[string]interface{}{"type": "string", "description": "Display label"},
						},
						"type": "object",
					},
				},
			},
		},
		"responses": []map[string]interface{}{
			{
				"status_code": "200",
				"content": map[string]interface{}{
					"application/json": map[string]interface{}{
						"schema": map[string]interface{}{
							"properties": map[string]interface{}{
								"result": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"total": map[string]interface{}{"type": "number", "description": "Calculated total"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(apiSpec)
	if err != nil {
		t.Fatal(err)
	}

	metadata := functionMetadataFromTool(&client.ToolDetail{
		MetadataType: "function",
		Metadata: &client.ToolMetadataInfo{
			APISpec: raw,
			FunctionContent: &struct {
				ScriptType      string   `json:"script_type"`
				Code            string   `json:"code"`
				Dependencies    []string `json:"dependencies"`
				DependenciesURL string   `json:"dependencies_url"`
			}{
				Code: "def handler(event):\n    return event\n",
			},
		},
	})

	assertParam(t, metadata.Inputs, "count", "number", "Counter value")
	assertParam(t, metadata.Inputs, "label", "string", "Display label")
	assertParam(t, metadata.Outputs, "total", "number", "Calculated total")
	if metadata.Code == "" {
		t.Fatalf("expected code to be restored")
	}
}

func assertParam(t *testing.T, params []model.FunctionParameterDef, name, paramType, description string) {
	t.Helper()
	for _, param := range params {
		if param.Name == name {
			if param.Type != paramType || param.Description != description {
				t.Fatalf("unexpected param %s: %+v", name, param)
			}
			return
		}
	}
	t.Fatalf("missing param %s in %+v", name, params)
}
