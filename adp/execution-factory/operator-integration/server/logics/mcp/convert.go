package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Result describes a schema conversion result.
type Result struct {
	Success bool           `json:"success"`
	Data    map[string]any `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// SimpleConverter converts the simplified OpenAPI representation used by this service.
type SimpleConverter struct{}

// SimpleOpenAPI is the simplified OpenAPI representation used by this service.
type SimpleOpenAPI struct {
	Parameters   []Parameter  `json:"parameters,omitempty"`
	RequestBody  *RequestBody `json:"request_body,omitempty"`
	Responses    []Response   `json:"responses,omitempty"`
	Components   *Components  `json:"components,omitempty"`
	Callbacks    any          `json:"callbacks,omitempty"`
	Security     any          `json:"security,omitempty"`
	Tags         any          `json:"tags,omitempty"`
	ExternalDocs any          `json:"external_docs,omitempty"`
}

// Parameter defines an OpenAPI parameter.
type Parameter struct {
	Name        string         `json:"name"`
	In          string         `json:"in"`
	Description string         `json:"description,omitempty"`
	Required    bool           `json:"required"`
	Schema      map[string]any `json:"schema"`
}

// RequestBody defines an OpenAPI request body.
type RequestBody struct {
	Description string             `json:"description,omitempty"`
	Content     map[string]Content `json:"content"`
	Required    bool               `json:"required"`
}

// Content defines an OpenAPI media type payload.
type Content struct {
	Schema   map[string]any            `json:"schema"`
	Examples map[string]map[string]any `json:"examples,omitempty"`
}

// Response defines an OpenAPI response.
type Response struct {
	StatusCode  string             `json:"status_code"`
	Description string             `json:"description"`
	Content     map[string]Content `json:"content"`
}

// Components contains reusable OpenAPI schemas.
type Components struct {
	Schemas map[string]map[string]any `json:"schemas"`
}

// NewSimpleConverter creates a simplified OpenAPI converter.
func NewSimpleConverter() *SimpleConverter {
	return &SimpleConverter{}
}

// ConvertFromBytes converts a JSON-encoded simplified OpenAPI document.
func (c *SimpleConverter) ConvertFromBytes(data []byte) *Result {
	var simpleOpenAPI SimpleOpenAPI
	if err := json.Unmarshal(data, &simpleOpenAPI); err != nil {
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("failed to parse JSON: %v", err),
		}
	}

	return c.convertSimpleOpenAPI(&simpleOpenAPI)
}

// ConvertFromString converts a JSON string containing a simplified OpenAPI document.
func (c *SimpleConverter) ConvertFromString(jsonStr string) *Result {
	return c.ConvertFromBytes([]byte(jsonStr))
}

// convertSimpleOpenAPI converts a simplified OpenAPI document into an MCP input schema.
func (c *SimpleConverter) convertSimpleOpenAPI(simple *SimpleOpenAPI) *Result {
	// Build top-level parameter groups.
	properties := map[string]any{}

	headers := c.extractParameters(simple.Parameters, "header", "HTTP header parameters")
	if headers != nil {
		properties["header"] = headers
	}

	query := c.extractParameters(simple.Parameters, "query", "URL query parameters")
	if query != nil {
		properties["query"] = query
	}

	path := c.extractParameters(simple.Parameters, "path", "URL path parameters")
	if path != nil {
		properties["path"] = path
	}

	body := c.extractRequestBody(simple.RequestBody)
	if body != nil {
		properties["body"] = body
	}

	// Build the standard JSON Schema shape.
	result := map[string]any{
		"type":       "object",
		"properties": properties,
	}

	// Add $defs instead of OpenAPI components.
	if simple.Components != nil && len(simple.Components.Schemas) > 0 {
		// Convert components.schemas into $defs.
		defs := make(map[string]any)
		for name, schema := range simple.Components.Schemas {
			// Rewrite schema references recursively.
			defs[name] = c.processSchemaRefs(schema)
		}
		result["$defs"] = defs
	}

	return &Result{
		Success: true,
		Data:    result,
	}
}

// extractParameters extracts parameters of the requested location.
// inType is one of header, query, or path; description labels the parameter group.
func (c *SimpleConverter) extractParameters(params []Parameter, inType, description string) map[string]any {
	props := map[string]any{}
	required := []string{}

	for _, param := range params {
		if param.In == inType {
			schemaObj := c.convertSchema(param.Schema)
			if param.Description != "" {
				schemaObj["description"] = param.Description
			}
			props[param.Name] = schemaObj
			if param.Required {
				required = append(required, param.Name)
			}
		}
	}

	if len(props) == 0 {
		return nil
	}

	obj := map[string]any{
		"type":        "object",
		"description": description,
		"properties":  props,
	}

	if len(required) > 0 {
		obj["required"] = required
	}

	return obj
}

// extractRequestBody extracts the JSON request body schema.
func (c *SimpleConverter) extractRequestBody(reqBody *RequestBody) map[string]any {
	if reqBody == nil {
		return nil
	}

	// Read the JSON media type.
	if content, ok := reqBody.Content["application/json"]; ok {
		schema := c.convertSchema(content.Schema)
		// Add a default description when the source schema has none.
		if _, hasDesc := schema["description"]; !hasDesc {
			schema["description"] = "Request body parameters"
		}
		return schema
	}

	return nil
}

// convertSchema converts a schema and rewrites its references.
func (c *SimpleConverter) convertSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return map[string]any{
			"type": "object",
		}
	}
	// Rewrite $ref values throughout the schema.
	return c.processSchemaRefs(schema)
}

// processSchemaRefs recursively rewrites components/schemas references to $defs.
func (c *SimpleConverter) processSchemaRefs(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	result := make(map[string]any)
	for key, value := range schema {
		switch v := value.(type) {
		case string:
			// Rewrite reference paths only for $ref fields.
			if key == "$ref" {
				// Replace #/components/schemas/ with #/$defs/.
				refStr := c.convertRefPath(v)
				result[key] = refStr
			} else {
				result[key] = v
			}
		case map[string]any:
			// Process nested objects recursively.
			result[key] = c.processSchemaRefs(v)
		case []any:
			// Process every object contained in an array.
			processedArray := make([]any, len(v))
			for i, item := range v {
				if itemMap, ok := item.(map[string]any); ok {
					processedArray[i] = c.processSchemaRefs(itemMap)
				} else {
					processedArray[i] = item
				}
			}
			result[key] = processedArray
		default:
			result[key] = v
		}
	}
	return result
}

// convertRefPath rewrites an OpenAPI component reference for JSON Schema.
func (c *SimpleConverter) convertRefPath(refPath string) string {
	// Replace components/schemas with $defs for local references.
	if refPath != "" && refPath[0] == '#' {
		// Keep non-component path segments unchanged.
		refPath = strings.ReplaceAll(refPath, "/components/schemas/", "/$defs/")
	}
	return refPath
}

// ToJSONString serializes a successful conversion result.
func (c *SimpleConverter) ToJSONString(result *Result) (string, error) {
	if !result.Success {
		return "", fmt.Errorf("conversion failed: %s", result.Error)
	}

	out, err := json.MarshalIndent(result.Data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize JSON: %v", err)
	}

	return string(out), nil
}

// GetSchemaInfo returns a compact schema summary.
func (c *SimpleConverter) GetSchemaInfo(result *Result) map[string]any {
	if !result.Success {
		return map[string]any{
			"error": result.Error,
		}
	}

	info := map[string]any{
		"type":        result.Data["type"],
		"description": result.Data["description"],
	}

	if properties, ok := result.Data["properties"].(map[string]any); ok {
		info["property_count"] = len(properties)
		info["properties"] = make([]string, 0, len(properties))
		for k := range properties {
			info["properties"] = append(info["properties"].([]string), k)
		}
	}

	if defs, ok := result.Data["$defs"].(map[string]any); ok {
		info["schema_count"] = len(defs)
		schemaList := make([]string, 0, len(defs))
		for k := range defs {
			schemaList = append(schemaList, k)
		}
		info["schemas"] = schemaList
	} else {
		// Return stable empty values when the schema has no $defs.
		info["schema_count"] = 0
		info["schemas"] = []string{}
	}

	return info
}
