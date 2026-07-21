// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"encoding/json"
	"strings"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/client"
)

func openAPISpecFromToolMetadata(tool *client.ToolDetail) string {
	if tool == nil || tool.Metadata == nil {
		return ""
	}

	meta := tool.Metadata
	if len(meta.APISpec) > 0 {
		var raw interface{}
		if err := json.Unmarshal(meta.APISpec, &raw); err == nil {
			if doc, ok := raw.(map[string]interface{}); ok {
				if _, hasPaths := doc["paths"]; hasPaths {
					b, marshalErr := json.Marshal(doc)
					if marshalErr == nil {
						return string(b)
					}
				}
			}
		}
	}

	path := meta.Path
	if path == "" {
		path = "/"
	}
	method := strings.ToLower(meta.Method)
	if method == "" {
		method = "get"
	}

	summary := meta.Summary
	if summary == "" {
		summary = tool.Name
	}
	description := meta.Description
	if description == "" {
		description = summary
	}
	serverURL := meta.ServerURL
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}

	operation := map[string]interface{}{
		"summary":     summary,
		"description": description,
	}

	var apiSpec map[string]interface{}
	if len(meta.APISpec) > 0 {
		_ = json.Unmarshal(meta.APISpec, &apiSpec)
	}
	if apiSpec != nil {
		if params, ok := apiSpec["parameters"]; ok {
			operation["parameters"] = params
		}
		if requestBody, ok := apiSpec["request_body"]; ok {
			if rbMap, ok := requestBody.(map[string]interface{}); ok {
				operation["requestBody"] = map[string]interface{}{
					"description": rbMap["description"],
					"required":    rbMap["required"],
					"content":     rbMap["content"],
				}
			}
		}
		if responses, ok := apiSpec["responses"].([]interface{}); ok && len(responses) > 0 {
			respMap := map[string]interface{}{}
			for _, item := range responses {
				entry, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				code, _ := entry["status_code"].(string)
				if code == "" {
					continue
				}
				respMap[code] = map[string]interface{}{
					"description": entry["description"],
					"content":     entry["content"],
				}
			}
			if len(respMap) > 0 {
				operation["responses"] = respMap
			}
		}
		if components, ok := apiSpec["components"]; ok {
			if compMap, ok := components.(map[string]interface{}); ok {
				if schemas, ok := compMap["schemas"]; ok {
					doc := map[string]interface{}{
						"openapi": "3.0.3",
						"info": map[string]interface{}{
							"title":       summary,
							"description": description,
							"version":     "1.0.0",
						},
						"servers": []map[string]string{{"url": serverURL}},
						"paths": map[string]interface{}{
							path: map[string]interface{}{
								method: operation,
							},
						},
						"components": map[string]interface{}{
							"schemas": schemas,
						},
					}
					b, err := json.Marshal(doc)
					if err == nil {
						return string(b)
					}
				}
			}
		}
	}

	if _, ok := operation["responses"]; !ok {
		operation["responses"] = map[string]interface{}{
			"200": map[string]interface{}{
				"description": "OK",
				"content": map[string]interface{}{
					"application/json": map[string]interface{}{
						"schema": map[string]string{"type": "object"},
					},
				},
			},
		}
	}

	doc := map[string]interface{}{
		"openapi": "3.0.3",
		"info": map[string]interface{}{
			"title":       summary,
			"description": description,
			"version":     "1.0.0",
		},
		"servers": []map[string]string{{"url": serverURL}},
		"paths": map[string]interface{}{
			path: map[string]interface{}{
				method: operation,
			},
		},
	}

	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}

	return string(b)
}

func endpointFromToolMetadata(tool *client.ToolDetail) *struct {
	Method string
	Path   string
} {
	if tool == nil || tool.Metadata == nil {
		return nil
	}

	method := strings.ToUpper(tool.Metadata.Method)
	path := tool.Metadata.Path
	if method == "" && path == "" {
		return nil
	}

	return &struct {
		Method string
		Path   string
	}{Method: method, Path: path}
}
