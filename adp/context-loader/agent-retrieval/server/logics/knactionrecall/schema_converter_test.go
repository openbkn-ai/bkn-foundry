package knactionrecall

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type mockLogger struct{}

func (m *mockLogger) WithContext(ctx context.Context) interfaces.Logger { return m }
func (m *mockLogger) Debug(v ...any)                                    {}
func (m *mockLogger) Info(v ...any)                                     {}
func (m *mockLogger) Warn(v ...any)                                     {}
func (m *mockLogger) Error(v ...any)                                    {}
func (m *mockLogger) Debugf(format string, v ...any)                    {}
func (m *mockLogger) Infof(format string, v ...any)                     {}
func (m *mockLogger) Warnf(format string, v ...any)                     {}
func (m *mockLogger) Errorf(format string, v ...any)                    {}

func TestConvertMCPSchemaToFunctionCall(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := common.SetLanguageToCtx(context.Background(), "en-US")

	// Case 1: Simple Schema
	inputJSON := `{
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		}
	}`
	var inputMap map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
		t.Fatalf("Failed to unmarshal test JSON: %v", err)
	}

	result, err := service.convertMCPSchemaToFunctionCall(ctx, inputMap)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if result["type"] != "object" {
		t.Errorf("Expected type object, got %v", result["type"])
	}

	// Case 2: With $defs
	inputJSON = `{
		"$defs": {
			"Person": {
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				}
			}
		},
		"properties": {
			"owner": {"$ref": "#/$defs/Person"}
		}
	}`
	if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
		t.Fatalf("Failed to unmarshal test JSON: %v", err)
	}
	result, err = service.convertMCPSchemaToFunctionCall(ctx, inputMap)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	props := result["properties"].(map[string]any)
	owner := props["owner"].(map[string]any)
	if owner["type"] != "object" {
		t.Errorf("Expected owner type object, got %v", owner["type"])
	}
	ownerProps := owner["properties"].(map[string]any)
	if _, ok := ownerProps["name"]; !ok {
		t.Errorf("Expected owner to have name property")
	}

	// Check $defs is removed
	if _, ok := result["$defs"]; ok {
		t.Errorf("Expected $defs to be removed")
	}
}

// TestConvertMCPSchemaToFunctionCall_BodyDefaultDescription Tests the default description logic of the body parameter when converting MCP Schema.
// Rule: When the body parameter exists in the first layer but description is missing, the "Request Body parameter" is automatically added.
func TestConvertMCPSchemaToFunctionCall_BodyDefaultDescription(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := common.SetLanguageToCtx(context.Background(), "en-US")

	// Case 1: body exists but is referenced through $ref, and the referenced schema has no description.
	// Expected: automatically add the default description "Request Body parameters".
	t.Run("body_without_description_via_ref", func(t *testing.T) {
		inputJSON := `{
			"$defs": {
				"UpdateEventStatusRequest": {
					"type": "object",
					"properties": {
						"status": {"type": "string"}
					}
				}
			},
			"type": "object",
			"properties": {
				"body": {"$ref": "#/$defs/UpdateEventStatusRequest"},
				"path": {
					"type": "object",
					"description": "URL 路径参数",
					"properties": {
						"event_id": {"type": "string"}
					}
				}
			}
		}`
		var inputMap map[string]any
		if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
			t.Fatalf("Failed to unmarshal test JSON: %v", err)
		}

		result, err := service.convertMCPSchemaToFunctionCall(ctx, inputMap)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		props := result["properties"].(map[string]any)
		body := props["body"].(map[string]any)

		// Verify that body receives the default English description.
		if desc, ok := body["description"].(string); !ok || desc != "Request body parameters." {
			t.Errorf("Expected body description 'Request body parameters.', got %v", body["description"])
		}

		// Verify path maintains original description.
		path := props["path"].(map[string]any)
		if desc, ok := path["description"].(string); !ok || desc != "URL 路径参数" {
			t.Errorf("Expected path description 'URL 路径参数', got %v", path["description"])
		}
	})

	// Case 2: body exists and has description.
	// Expectation: keep the original description and do not overwrite it.
	t.Run("body_with_existing_description", func(t *testing.T) {
		inputJSON := `{
			"type": "object",
			"properties": {
				"body": {
					"type": "object",
					"description": "自定义请求体描述",
					"properties": {
						"name": {"type": "string"}
					}
				}
			}
		}`
		var inputMap map[string]any
		if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
			t.Fatalf("Failed to unmarshal test JSON: %v", err)
		}

		result, err := service.convertMCPSchemaToFunctionCall(ctx, inputMap)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		props := result["properties"].(map[string]any)
		body := props["body"].(map[string]any)

		// Verify that the original description is retained.
		if desc, ok := body["description"].(string); !ok || desc != "自定义请求体描述" {
			t.Errorf("Expected body description '自定义请求体描述', got %v", body["description"])
		}
	})

	// Case 3: No body parameter.
	// Expectation: No processing, no error reporting.
	t.Run("no_body_property", func(t *testing.T) {
		inputJSON := `{
			"type": "object",
			"properties": {
				"query": {
					"type": "object",
					"properties": {
						"limit": {"type": "integer"}
					}
				}
			}
		}`
		var inputMap map[string]any
		if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
			t.Fatalf("Failed to unmarshal test JSON: %v", err)
		}

		result, err := service.convertMCPSchemaToFunctionCall(ctx, inputMap)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		props := result["properties"].(map[string]any)

		// Validate body does not exist.
		if _, ok := props["body"]; ok {
			t.Error("Expected no body property, but found one")
		}

		// Validate query exists.
		if _, ok := props["query"]; !ok {
			t.Error("Expected query property to exist")
		}
	})

	// Case 4: body is defined directly (not $ref) and has no description.
	// Expected: automatically add the default description "Request Body parameters".
	t.Run("body_direct_without_description", func(t *testing.T) {
		inputJSON := `{
			"type": "object",
			"properties": {
				"body": {
					"type": "object",
					"properties": {
						"name": {"type": "string"}
					}
				}
			}
		}`
		var inputMap map[string]any
		if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
			t.Fatalf("Failed to unmarshal test JSON: %v", err)
		}

		result, err := service.convertMCPSchemaToFunctionCall(ctx, inputMap)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		props := result["properties"].(map[string]any)
		body := props["body"].(map[string]any)

		// Verify that body receives the default English description.
		if desc, ok := body["description"].(string); !ok || desc != "Request body parameters." {
			t.Errorf("Expected body description 'Request body parameters.', got %v", body["description"])
		}
	})
}

func TestResolveMCPSchemaCircular(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	// Case 3: Circular Reference
	inputJSON := `{
		"$defs": {
			"Node": {
				"type": "object",
				"properties": {
					"child": {"$ref": "#/$defs/Node"}
				}
			}
		},
		"properties": {
			"root": {"$ref": "#/$defs/Node"}
		}
	}`
	var inputMap map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
		t.Fatalf("Failed to unmarshal test JSON: %v", err)
	}

	result, err := service.convertMCPSchemaToFunctionCall(ctx, inputMap)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Should not crash and should prune
	props := result["properties"].(map[string]any)
	root := props["root"].(map[string]any)
	rootProps := root["properties"].(map[string]any)
	child := rootProps["child"].(map[string]any)

	// Child should be pruned (no properties) or recursively resolved up to depth limit
	// Since circular detection is immediate for same path in visitedRefs
	// Root visits Node. Node visits Child (Node).
	// If depth limit is 3, it might expand a bit.
	// But visitedRefs checks path.
	// resolveMCPSchema calls resolveMCPSchema for ref.
	// visitedRefs is passed.
	// root -> Node (visited["#/$defs/Node"] = true)
	// Node.properties.child -> ref "#/$defs/Node"
	// check visited -> true -> prune.
	// So child should be pruned.

	if _, ok := child["properties"]; ok {
		// If it's pruned, it shouldn't have properties
		t.Errorf("Expected circular reference to be pruned, but found properties")
	}
}

// TestConvertSchemaToFunctionCall_WithParameters Tests OpenAPI Schema conversion with parameters.
func TestConvertSchemaToFunctionCall_WithParameters(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	apiSpec := map[string]any{
		"parameters": []any{
			map[string]any{
				"name":        "id",
				"in":          "path",
				"required":    true,
				"description": "资源ID",
				"schema":      map[string]any{"type": "string"},
			},
			map[string]any{
				"name":        "limit",
				"in":          "query",
				"required":    false,
				"description": "返回数量限制",
				"schema":      map[string]any{"type": "integer"},
			},
			map[string]any{
				"name":        "X-Request-ID",
				"in":          "header",
				"required":    true,
				"description": "请求ID",
				"schema":      map[string]any{"type": "string"},
			},
		},
	}

	result, err := service.convertSchemaToFunctionCall(ctx, apiSpec)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result["type"] != "object" {
		t.Errorf("Expected type object, got %v", result["type"])
	}

	props := result["properties"].(map[string]any)

	// Check path parameters.
	if pathProps, ok := props["path"].(map[string]any); ok {
		pathParams := pathProps["properties"].(map[string]any)
		if _, ok := pathParams["id"]; !ok {
			t.Error("Expected path to have id parameter")
		}
	} else {
		t.Error("Expected path to exist in properties")
	}

	// Check query parameters.
	if queryProps, ok := props["query"].(map[string]any); ok {
		queryParams := queryProps["properties"].(map[string]any)
		if _, ok := queryParams["limit"]; !ok {
			t.Error("Expected query to have limit parameter")
		}
	} else {
		t.Error("Expected query to exist in properties")
	}

	// Check header parameters.
	if headerProps, ok := props["header"].(map[string]any); ok {
		headerParams := headerProps["properties"].(map[string]any)
		if _, ok := headerParams["X-Request-ID"]; !ok {
			t.Error("Expected header to have X-Request-ID parameter")
		}
	} else {
		t.Error("Expected header to exist in properties")
	}
}

// TestConvertSchemaToFunctionCall_WithRequestBody tests Schema conversion with request_body.
func TestConvertSchemaToFunctionCall_WithRequestBody(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	apiSpec := map[string]any{
		"request_body": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{
								"type":        "string",
								"description": "名称",
							},
							"age": map[string]any{
								"type":        "integer",
								"description": "年龄",
							},
						},
						"required": []any{"name"},
					},
				},
			},
		},
	}

	result, err := service.convertSchemaToFunctionCall(ctx, apiSpec)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	props := result["properties"].(map[string]any)

	// Check body parameter.
	if bodyProps, ok := props["body"].(map[string]any); ok {
		bodyParams := bodyProps["properties"].(map[string]any)
		if _, ok := bodyParams["name"]; !ok {
			t.Error("Expected body to have name parameter")
		}
		if _, ok := bodyParams["age"]; !ok {
			t.Error("Expected body to have age parameter")
		}
	} else {
		t.Error("Expected body to exist in properties")
	}
}

// TestConvertSchemaToFunctionCall_Empty tests the empty Schema.
func TestConvertSchemaToFunctionCall_Empty(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	apiSpec := map[string]any{}

	result, err := service.convertSchemaToFunctionCall(ctx, apiSpec)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result["type"] != "object" {
		t.Errorf("Expected type object, got %v", result["type"])
	}

	props := result["properties"].(map[string]any)
	// An empty Schema should have at least one body field.
	if _, ok := props["body"]; !ok {
		t.Error("Expected body to exist in properties for empty schema")
	}
}

// TestMapFixedParams_AllLocations tests fixed parameters mapped to all locations.
func TestMapFixedParams_AllLocations(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	parameters := map[string]any{
		"id":           "123",
		"limit":        10,
		"X-Request-ID": "req-001",
		"name":         "test",
	}

	apiSpec := map[string]any{
		"parameters": []any{
			map[string]any{"name": "id", "in": "path"},
			map[string]any{"name": "limit", "in": "query"},
			map[string]any{"name": "X-Request-ID", "in": "header"},
		},
	}

	result := service.mapFixedParams(ctx, parameters, apiSpec)

	// Check path parameters.
	if result.Path["id"] != "123" {
		t.Errorf("Expected path[id] = '123', got %v", result.Path["id"])
	}

	// Check query parameters.
	if result.Query["limit"] != 10 {
		t.Errorf("Expected query[limit] = 10, got %v", result.Query["limit"])
	}

	// Check header parameters.
	if result.Header["X-Request-ID"] != "req-001" {
		t.Errorf("Expected header[X-Request-ID] = 'req-001', got %v", result.Header["X-Request-ID"])
	}

	// Check for unmapped parameters into body.
	if result.Body["name"] != "test" {
		t.Errorf("Expected body[name] = 'test', got %v", result.Body["name"])
	}
}

// TestMapFixedParams_HeaderByNaming tests to determine header parameters through naming rules.
func TestMapFixedParams_HeaderByNaming(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	parameters := map[string]any{
		"x-custom-header": "value1",
		"Authorization":   "Bearer token",
		"normal-param":    "value2",
	}

	apiSpec := map[string]any{} // No parameters defined.

	result := service.mapFixedParams(ctx, parameters, apiSpec)

	// Parameters starting with x- should go into the header.
	if result.Header["x-custom-header"] != "value1" {
		t.Errorf("Expected header[x-custom-header] = 'value1', got %v", result.Header["x-custom-header"])
	}

	// Authorization should go into header.
	if result.Header["Authorization"] != "Bearer token" {
		t.Errorf("Expected header[Authorization] = 'Bearer token', got %v", result.Header["Authorization"])
	}

	// Normal parameters should go into body.
	if result.Body["normal-param"] != "value2" {
		t.Errorf("Expected body[normal-param] = 'value2', got %v", result.Body["normal-param"])
	}
}

// TestIsHeaderParam test header parameter judgment.
func TestIsHeaderParam(t *testing.T) {
	testCases := []struct {
		key      string
		expected bool
	}{
		{"x-custom-header", true},
		{"X-Request-ID", true},
		{"authorization", true},
		{"Authorization", true},
		{"content-type", true},
		{"Content-Type", true},
		{"normal-param", false},
		{"id", false},
		{"name", false},
	}

	for _, tc := range testCases {
		t.Run(tc.key, func(t *testing.T) {
			result := isHeaderParam(tc.key)
			if result != tc.expected {
				t.Errorf("isHeaderParam(%s) = %v, expected %v", tc.key, result, tc.expected)
			}
		})
	}
}

// TestBuildPropertyDefinition test property definition build.
func TestBuildPropertyDefinition(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	// Test basic types.
	schema := map[string]any{
		"type":        "string",
		"description": "测试描述",
	}
	result := service.buildPropertyDefinition(schema, nil)
	if result["type"] != "string" {
		t.Errorf("Expected type string, got %v", result["type"])
	}
	if result["description"] != "测试描述" {
		t.Errorf("Expected description '测试描述', got %v", result["description"])
	}

	// Test strip enumeration.
	schema = map[string]any{
		"type": "string",
		"enum": []any{"a", "b", "c"},
	}
	result = service.buildPropertyDefinition(schema, nil)
	if result["enum"] == nil {
		t.Error("Expected enum to be preserved")
	}

	// Test objects with properties.
	schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
	result = service.buildPropertyDefinition(schema, nil)
	if result["type"] != "object" {
		t.Errorf("Expected type object, got %v", result["type"])
	}
	if result["properties"] == nil {
		t.Error("Expected properties to be preserved")
	}

	// Test array type.
	schema = map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "string",
		},
	}
	result = service.buildPropertyDefinition(schema, nil)
	if result["items"] == nil {
		t.Error("Expected items to be preserved for array type")
	}

	// Test parameter level description overrides schema description.
	schema = map[string]any{
		"type":        "string",
		"description": "schema描述",
	}
	result = service.buildPropertyDefinition(schema, "参数描述")
	if result["description"] != "参数描述" {
		t.Errorf("Expected param description to override schema description, got %v", result["description"])
	}
}

// TestPruneSchema test schema pruning.
func TestPruneSchema(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	// Test basic pruning.
	schema := map[string]any{
		"type":        "object",
		"description": "测试对象",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
	result := service.pruneSchema(schema)
	if result["type"] != "object" {
		t.Errorf("Expected type object, got %v", result["type"])
	}
	if result["description"] != "测试对象" {
		t.Errorf("Expected description '测试对象', got %v", result["description"])
	}
	if _, hasProps := result["properties"]; hasProps {
		t.Error("Expected properties to be removed after pruning")
	}

	// Test array type pruning.
	schema = map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string"},
			},
		},
	}
	result = service.pruneSchema(schema)
	if result["type"] != "array" {
		t.Errorf("Expected type array, got %v", result["type"])
	}
	if result["items"] == nil {
		t.Error("Expected items to be preserved for array")
	}
	items := result["items"].(map[string]any)
	if _, hasProps := items["properties"]; hasProps {
		t.Error("Expected items properties to be removed after pruning")
	}

	// Test untyped schema.
	schema = map[string]any{
		"description": "无类型",
	}
	result = service.pruneSchema(schema)
	if result["type"] != "object" {
		t.Errorf("Expected default type object, got %v", result["type"])
	}
}

// ==================== Action Driver Schema converttest ====================.

// TestConvertToolSchemaToActionDriver_WithParameters Test Tool type path/query/header parameter unpacking and merging.
func TestConvertToolSchemaToActionDriver_WithParameters(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	apiSpec := map[string]any{
		"parameters": []any{
			map[string]any{
				"name":        "id",
				"in":          "path",
				"required":    true,
				"description": "资源ID",
				"schema":      map[string]any{"type": "string"},
			},
			map[string]any{
				"name":        "limit",
				"in":          "query",
				"required":    false,
				"description": "返回数量限制",
				"schema":      map[string]any{"type": "integer"},
			},
			map[string]any{
				"name":        "X-Request-ID",
				"in":          "header",
				"required":    true,
				"description": "请求ID",
				"schema":      map[string]any{"type": "string"},
			},
		},
	}

	result, err := service.convertToolSchemaToActionDriver(ctx, apiSpec)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Validate the top-level structure.
	if result["type"] != "object" {
		t.Errorf("Expected type object, got %v", result["type"])
	}

	props := result["properties"].(map[string]any)

	// Validate that dynamic_params and _instance_identities are included.
	if _, ok := props["dynamic_params"]; !ok {
		t.Fatal("Expected dynamic_params in properties")
	}
	if _, ok := props["_instance_identities"]; !ok {
		t.Fatal("Expected _instance_identities in properties")
	}

	// Verify that old header/path/query/body is not included.
	for _, oldKey := range []string{"header", "path", "query", "body"} {
		if _, ok := props[oldKey]; ok {
			t.Errorf("Should not contain old key '%s' in top-level properties", oldKey)
		}
	}

	// Verify dynamic_params contains all parameters (after unpacking)
	dp := props["dynamic_params"].(map[string]any)
	dpProps := dp["properties"].(map[string]any)
	if _, ok := dpProps["id"]; !ok {
		t.Error("Expected dynamic_params to have id parameter")
	}
	if _, ok := dpProps["limit"]; !ok {
		t.Error("Expected dynamic_params to have limit parameter")
	}
	if _, ok := dpProps["X-Request-ID"]; !ok {
		t.Error("Expected dynamic_params to have X-Request-ID parameter")
	}

	// Verify required merge.
	if dpRequired, ok := dp["required"].([]string); ok {
		requiredSet := make(map[string]bool)
		for _, r := range dpRequired {
			requiredSet[r] = true
		}
		if !requiredSet["id"] {
			t.Error("Expected 'id' in required")
		}
		if !requiredSet["X-Request-ID"] {
			t.Error("Expected 'X-Request-ID' in required")
		}
	}
}

// TestConvertToolSchemaToActionDriver_WithRequestBody test body parameters are unpacked and merged into dynamic_params.
func TestConvertToolSchemaToActionDriver_WithRequestBody(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	apiSpec := map[string]any{
		"request_body": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{
								"type":        "string",
								"description": "名称",
							},
							"age": map[string]any{
								"type":        "integer",
								"description": "年龄",
							},
						},
						"required": []any{"name"},
					},
				},
			},
		},
	}

	result, err := service.convertToolSchemaToActionDriver(ctx, apiSpec)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	props := result["properties"].(map[string]any)
	dp := props["dynamic_params"].(map[string]any)
	dpProps := dp["properties"].(map[string]any)

	if _, ok := dpProps["name"]; !ok {
		t.Error("Expected dynamic_params to have name parameter from body")
	}
	if _, ok := dpProps["age"]; !ok {
		t.Error("Expected dynamic_params to have age parameter from body")
	}

	// Validate required merge from body.
	if dpRequired, ok := dp["required"].([]string); ok {
		found := false
		for _, r := range dpRequired {
			if r == "name" {
				found = true
				break
			}
		}
		if !found {
			t.Error("Expected 'name' in required from body")
		}
	}
}

// TestConvertToolSchemaToActionDriver_NameConflict returns an error when testing fields with the same name from different locations.
func TestConvertToolSchemaToActionDriver_NameConflict(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	apiSpec := map[string]any{
		"parameters": []any{
			map[string]any{
				"name":   "id",
				"in":     "path",
				"schema": map[string]any{"type": "string"},
			},
			map[string]any{
				"name":   "id",
				"in":     "query", // Same name but different locations.
				"schema": map[string]any{"type": "string"},
			},
		},
	}

	_, err := service.convertToolSchemaToActionDriver(ctx, apiSpec)
	if err == nil {
		t.Fatal("Expected error for name conflict, got nil")
	}
}

// TestConvertToolSchemaToActionDriver_Empty tests empty schema.
func TestConvertToolSchemaToActionDriver_Empty(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	apiSpec := map[string]any{}

	result, err := service.convertToolSchemaToActionDriver(ctx, apiSpec)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result["type"] != "object" {
		t.Errorf("Expected type object, got %v", result["type"])
	}

	props := result["properties"].(map[string]any)
	if _, ok := props["dynamic_params"]; !ok {
		t.Error("Expected dynamic_params even for empty schema")
	}
	if _, ok := props["_instance_identities"]; !ok {
		t.Error("Expected _instance_identities even for empty schema")
	}
}

// TestConvertMCPSchemaToActionDriver tests MCP schema converted to action driven structure.
func TestConvertMCPSchemaToActionDriver(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	ctx := context.Background()

	inputJSON := `{
		"type": "object",
		"properties": {
			"disease_id": {"type": "string", "description": "疾病ID"},
			"include_drugs": {"type": "boolean"}
		},
		"required": ["disease_id"]
	}`
	var inputMap map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
		t.Fatalf("Failed to unmarshal test JSON: %v", err)
	}

	result, err := service.convertMCPSchemaToActionDriver(ctx, inputMap)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Validate the top-level structure.
	if result["type"] != "object" {
		t.Errorf("Expected type object, got %v", result["type"])
	}

	props := result["properties"].(map[string]any)

	// Validate that dynamic_params and _instance_identities are included.
	if _, ok := props["dynamic_params"]; !ok {
		t.Fatal("Expected dynamic_params in properties")
	}
	if _, ok := props["_instance_identities"]; !ok {
		t.Fatal("Expected _instance_identities in properties")
	}

	// Verify dynamic_params contains original MCP schema attributes.
	dp := props["dynamic_params"].(map[string]any)
	dpProps := dp["properties"].(map[string]any)
	if _, ok := dpProps["disease_id"]; !ok {
		t.Error("Expected dynamic_params to have disease_id")
	}
	if _, ok := dpProps["include_drugs"]; !ok {
		t.Error("Expected dynamic_params to have include_drugs")
	}

	// Validate required pass through.
	if dpRequired, ok := dp["required"].([]any); ok {
		found := false
		for _, r := range dpRequired {
			if r == "disease_id" {
				found = true
			}
		}
		if !found {
			t.Error("Expected 'disease_id' in required")
		}
	}
}

// TestWrapActionDriverParameters test auxiliary method constructs the top-level structure.
func TestWrapActionDriverParameters(t *testing.T) {
	service := &knActionRecallServiceImpl{
		logger: &mockLogger{},
	}

	dynamicParamsSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"foo": map[string]any{"type": "string"}},
	}

	result := service.wrapActionDriverParameters(context.Background(), dynamicParamsSchema)

	if result["type"] != "object" {
		t.Errorf("Expected type object, got %v", result["type"])
	}

	props := result["properties"].(map[string]any)
	if _, ok := props["dynamic_params"]; !ok {
		t.Error("Expected dynamic_params")
	}
	if _, ok := props["_instance_identities"]; !ok {
		t.Error("Expected _instance_identities")
	}

	ii := props["_instance_identities"].(map[string]any)
	if ii["type"] != "array" {
		t.Errorf("Expected _instance_identities type array, got %v", ii["type"])
	}
}

func TestActionDriverSchemaDescriptionsUseRequestLocale(t *testing.T) {
	service := &knActionRecallServiceImpl{logger: &mockLogger{}}
	for _, tt := range []struct {
		locale           string
		dynamicParam     string
		instanceIDs      string
		instanceIdentity string
	}{
		{
			locale:           "zh-CN",
			dynamicParam:     "行动执行动态参数。",
			instanceIDs:      "目标实例标识列表。",
			instanceIdentity: "包含动态属性键值对的实例标识对象。",
		},
		{
			locale:           "en-US",
			dynamicParam:     "Action execution dynamic parameters.",
			instanceIDs:      "Target instance identities.",
			instanceIdentity: "An instance identity object containing dynamic property key-value pairs.",
		},
	} {
		t.Run(tt.locale, func(t *testing.T) {
			ctx := common.SetLanguageToCtx(context.Background(), tt.locale)
			result, err := service.convertMCPSchemaToActionDriver(ctx, map[string]any{
				"type": "object",
				"properties": map[string]any{
					"region": map[string]any{"type": "string"},
				},
			})
			if err != nil {
				t.Fatalf("convert MCP schema: %v", err)
			}
			properties := result["properties"].(map[string]any)
			dynamicParams := properties["dynamic_params"].(map[string]any)
			if description := dynamicParams["description"].(string); !strings.HasPrefix(description, tt.dynamicParam) {
				t.Fatalf("dynamic_params description = %q, want prefix %q", description, tt.dynamicParam)
			}
			instanceIDs := properties["_instance_identities"].(map[string]any)
			if description := instanceIDs["description"].(string); !strings.HasPrefix(description, tt.instanceIDs) {
				t.Fatalf("_instance_identities description = %q, want prefix %q", description, tt.instanceIDs)
			}
			identity := instanceIDs["items"].(map[string]any)
			if description := identity["description"].(string); description != tt.instanceIdentity {
				t.Fatalf("instance identity description = %q, want %q", description, tt.instanceIdentity)
			}
		})
	}
}

// TestBuildActionDriverAPIURL test action driver URL formatting.
func TestBuildActionDriverAPIURL(t *testing.T) {
	service := &knActionRecallServiceImpl{
		config: &config.Config{
			OntologyQuery: config.PrivateBaseConfig{
				PrivateProtocol: "http",
				PrivateHost:     "ontology-query",
				PrivatePort:     13018,
			},
		},
	}

	url := service.buildActionDriverAPIURL("kn_abc", "at_xyz")
	expected := "http://ontology-query:13018/api/ontology-query/in/v1/knowledge-networks/kn_abc/action-types/at_xyz/execute"
	if url != expected {
		t.Errorf("URL mismatch\nExpected: %s\nActual:   %s", expected, url)
	}
}

// TestBuildActionDriverFixedParams test action driver fixed_params constructor.
func TestBuildActionDriverFixedParams(t *testing.T) {
	instanceIdentities := []map[string]any{{"id": "obj-001"}}
	parameters := map[string]any{"namespace": "default", "pod_name": "test-pod"}

	fixedParams := interfaces.ActionDriverFixedParams{
		DynamicParams:      parameters,
		InstanceIdentities: instanceIdentities,
	}

	if fixedParams.DynamicParams["namespace"] != "default" {
		t.Errorf("Expected namespace=default, got %v", fixedParams.DynamicParams["namespace"])
	}
	if fixedParams.DynamicParams["pod_name"] != "test-pod" {
		t.Errorf("Expected pod_name=test-pod, got %v", fixedParams.DynamicParams["pod_name"])
	}
	if len(fixedParams.InstanceIdentities) != 1 {
		t.Fatalf("Expected 1 instance identity, got %d", len(fixedParams.InstanceIdentities))
	}
	if fixedParams.InstanceIdentities[0]["id"] != "obj-001" {
		t.Errorf("Expected id=obj-001, got %v", fixedParams.InstanceIdentities[0]["id"])
	}
}
