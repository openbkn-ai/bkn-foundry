// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knactionrecall

import (
	"context"
	"fmt"
	"strings"

	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const (
	// MaxSchemaDepth is the maximum $ref reference recursion depth, used to prevent infinite recursion caused by excessively deep nesting and circular references.
	// Purpose:
	// 1. Limit non-cyclic deeply nested references (such as A -> B -> C -> D)
	// 2. As the second line of defense for circular references (circular reference detection is triggered first)
	// Recommended value: 2-3 layers.
	// - 2 layers: suitable for simple scenarios (such as tree structures)
	// - 3 layers: suitable for complex scenarios (multi-layer nesting)
	MaxSchemaDepth = 3

	// responseStatusOK is the response status code preferred when deriving the output schema.
	responseStatusOK = "200"
)

// ==================== Universal Schema reference parser ====================.
// NOTE: Unify the $ref parsing logic of OpenAPI (#/components/schemas/) and MCP (#/$defs/)
// The core recursive logic, loop detection, depth control, and pruning strategies of the two are completely consistent.
// Only the reference path format and search location are different, and reuse is achieved through RefResolver function parameterization.

// RefResolver reference resolver function type.
// Purpose: Find and return the referenced Schema definition based on the $ref path.
// OpenAPI: Find from apiSpec["components"]["schemas"]
// MCP: Find from inputSchema["$defs"]
type RefResolver func(refPath string) (map[string]any, error)

// resolveSchemaWithResolver general Schema parsing function (supports $ref reference, cycle detection and depth control)
// Parameter:
// - ctx: context
// - schema: Schema to be parsed.
// - refResolver: Reference resolver (find definition based on $ref path)
// - visitedRefs: visited reference paths (for cycle detection)
// - currentDepth: current recursion depth.
//
// Returns: the parsed Schema ($ref is inlined)
func (s *knActionRecallServiceImpl) resolveSchemaWithResolver(
	ctx context.Context,
	schema map[string]any,
	refResolver RefResolver,
	visitedRefs map[string]bool,
	currentDepth int,
) (map[string]any, error) {
	if schema == nil {
		return map[string]any{"type": "string"}, nil
	}

	// Copy the schema to avoid modifying the original map.
	resolved := make(map[string]any)
	for k, v := range schema {
		resolved[k] = v
	}

	// 1. Handle $ref reference.
	if refPath, ok := resolved["$ref"].(string); ok {
		// 1.1 Circular reference detection.
		if visitedRefs[refPath] {
			s.logger.WithContext(ctx).Debugf("[SchemaResolver] Circular reference detected for %s, pruning", refPath)
			refSchema, err := refResolver(refPath)
			if err != nil {
				return map[string]any{"type": "object"}, nil
			}
			return s.pruneSchema(refSchema), nil
		}

		// 1.2 Depth limit detection.
		if currentDepth >= MaxSchemaDepth {
			s.logger.WithContext(ctx).Debugf("[SchemaResolver] Max depth reached for %s (depth: %d), pruning", refPath, currentDepth)
			refSchema, err := refResolver(refPath)
			if err != nil {
				return map[string]any{"type": "object"}, nil
			}
			return s.pruneSchema(refSchema), nil
		}

		// 1.3 Mark as visited.
		visitedRefs[refPath] = true
		defer func() { delete(visitedRefs, refPath) }()

		// 1.4 Get the reference definition and parse it recursively (depth +1)
		refSchema, err := refResolver(refPath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve $ref %s: %w", refPath, err)
		}
		return s.resolveSchemaWithResolver(ctx, refSchema, refResolver, visitedRefs, currentDepth+1)
	}

	// 2. Process properties (recursively parse each property, the depth remains unchanged)
	if props, ok := resolved["properties"].(map[string]any); ok {
		newProps := make(map[string]any)
		for propName, propDef := range props {
			if propMap, ok := propDef.(map[string]any); ok {
				resolvedProp, err := s.resolveSchemaWithResolver(ctx, propMap, refResolver, visitedRefs, currentDepth)
				if err != nil {
					s.logger.WithContext(ctx).Warnf("[SchemaResolver] Failed to resolve property %s: %v", propName, err)
					newProps[propName] = propDef // Downgrade: keep original value.
				} else {
					newProps[propName] = resolvedProp
				}
			} else {
				newProps[propName] = propDef
			}
		}
		resolved["properties"] = newProps
	}

	// 3. Process array items (recursive analysis, unchanged depth)
	if resolved["type"] == "array" {
		if items, ok := resolved["items"].(map[string]any); ok {
			resolvedItems, err := s.resolveSchemaWithResolver(ctx, items, refResolver, visitedRefs, currentDepth)
			if err != nil {
				s.logger.WithContext(ctx).Warnf("[SchemaResolver] Failed to resolve array items: %v", err)
			} else {
				resolved["items"] = resolvedItems
			}
		}
	}

	return resolved, nil
}

// convertSchemaToFunctionCall Convert OpenAPI Schema to OpenAI Function Call Schema.
// Improvement: Keep hierarchical structure (header/path/query/body) instead of flattening.
//
//nolint:unparam // Keep the interface consistent; the error return is for future extension.
func (s *knActionRecallServiceImpl) convertSchemaToFunctionCall(ctx context.Context, apiSpec map[string]any) (map[string]any, error) {
	// Use hierarchical structure: header/path/query/body.
	properties := map[string]any{
		"header": map[string]any{
			"type":        "object",
			"description": infraerrors.LocalizedDetail(ctx, "ActionParamHeader"),
			"properties":  make(map[string]any),
		},
		"path": map[string]any{
			"type":        "object",
			"description": infraerrors.LocalizedDetail(ctx, "ActionParamPath"),
			"properties":  make(map[string]any),
		},
		"query": map[string]any{
			"type":        "object",
			"description": infraerrors.LocalizedDetail(ctx, "ActionParamQuery"),
			"properties":  make(map[string]any),
		},
		"body": map[string]any{
			"type":        "object",
			"description": infraerrors.LocalizedDetail(ctx, "ActionParamBody"),
			"properties":  make(map[string]any),
		},
	}

	// Required parameters for each position.
	requiredByLocation := map[string][]string{
		"header": {},
		"path":   {},
		"query":  {},
		"body":   {},
	}

	// Visited records used for circular-reference detection.
	visitedRefs := make(map[string]bool)

	// 1. Handle parameters (path/query/header).
	if params, ok := apiSpec["parameters"].([]any); ok {
		for _, paramItem := range params {
			param, ok := paramItem.(map[string]any)
			if !ok {
				continue
			}

			paramName, _ := param["name"].(string)
			if paramName == "" {
				continue
			}

			paramLocation, _ := param["in"].(string) // path/query/header
			if paramLocation == "" {
				continue
			}

			// Resolve the parameter schema with $ref and depth support.
			paramSchema, err := s.resolveSchema(ctx, param["schema"], apiSpec, visitedRefs, 0)
			if err != nil {
				s.logger.WithContext(ctx).Warnf("[KnActionRecall#convertSchema] Failed to resolve param schema for %s: %v", paramName, err)
				continue
			}

			// Build the parameter definition.
			propDef := s.buildPropertyDefinition(paramSchema, param["description"])

			// Place the definition under its parameter location.
			if locationProps, ok := properties[paramLocation].(map[string]any); ok {
				if props, ok := locationProps["properties"].(map[string]any); ok {
					props[paramName] = propDef
				}
			}

			// Collect required parameters.
			if isRequired, ok := param["required"].(bool); ok && isRequired {
				requiredByLocation[paramLocation] = append(requiredByLocation[paramLocation], paramName)
			}
		}
	}

	// 2. Process request_body parameters.
	if requestBody, ok := apiSpec["request_body"].(map[string]any); ok {
		if content, ok := requestBody["content"].(map[string]any); ok {
			if appJSON, ok := content["application/json"].(map[string]any); ok {
				if schema, ok := appJSON["schema"].(map[string]any); ok {
					// Resolve the body schema with $ref and depth support.
					bodySchema, err := s.resolveSchema(ctx, schema, apiSpec, visitedRefs, 0)
					if err != nil {
						s.logger.WithContext(ctx).Warnf("[KnActionRecall#convertSchema] Failed to resolve body schema: %v", err)
						// Add a generic body parameter as a fallback.
						if bodyProps, ok := properties["body"].(map[string]any); ok {
							if props, ok := bodyProps["properties"].(map[string]any); ok {
								props["request_body"] = map[string]any{
									"type":        "object",
									"description": infraerrors.LocalizedDetail(ctx, "ActionParamBody"),
								}
							}
						}
					} else {
						// Expand the body schema properties.
						if bodyProps, ok := properties["body"].(map[string]any); ok {
							if props, ok := bodyProps["properties"].(map[string]any); ok {
								s.mergeSchemaProperties(ctx, props, bodySchema, apiSpec, visitedRefs, 0)
							}
							// Merge required fields.
							if bodyRequired, ok := bodySchema["required"].([]any); ok {
								for _, req := range bodyRequired {
									if reqStr, ok := req.(string); ok {
										requiredByLocation["body"] = append(requiredByLocation["body"], reqStr)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// 3. Set required fields for each parameter location.
	for location, required := range requiredByLocation {
		if len(required) > 0 {
			if locationProps, ok := properties[location].(map[string]any); ok {
				locationProps["required"] = required
			}
		}
	}

	// 4. Remove empty parameter locations.
	result := map[string]any{
		"type":       "object",
		"properties": make(map[string]any),
	}

	resultProps := result["properties"].(map[string]any)
	for location, locationProps := range properties {
		if props, ok := locationProps.(map[string]any)["properties"].(map[string]any); ok {
			if len(props) > 0 {
				resultProps[location] = locationProps
			}
		}
	}

	// Return an empty body structure when no parameters are available.
	if len(resultProps) == 0 {
		resultProps["body"] = map[string]any{
			"type":        "object",
			"description": infraerrors.LocalizedDetail(ctx, "ActionParamBody"),
			"properties":  make(map[string]any),
		}
	}

	return result, nil
}

// resolveSchema resolves a schema with $ref, cycle detection, and depth control.
// It prunes by depth:
// - Each $ref resolution increases depth by one.
// - Resolving properties keeps the same depth.
// - At the maximum depth it retains type and description but removes properties.
// currentDepth controls expansion depth for cyclic references.
func (s *knActionRecallServiceImpl) resolveSchema(
	ctx context.Context,
	schema any,
	apiSpec map[string]any,
	visitedRefs map[string]bool,
	currentDepth int,
) (map[string]any, error) {
	if schema == nil {
		return map[string]any{"type": "string"}, nil
	}

	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return map[string]any{"type": "string"}, nil
	}

	// If there is type directly and there is no $ref, return directly.
	if _, hasType := schemaMap["type"]; hasType && schemaMap["$ref"] == nil {
		// If there are properties, it needs to be processed recursively (the depth remains unchanged)
		if props, ok := schemaMap["properties"].(map[string]any); ok {
			resolvedProps := make(map[string]any)
			for propName, propDef := range props {
				resolvedProp, err := s.resolveSchema(ctx, propDef, apiSpec, visitedRefs, currentDepth)
				if err != nil {
					s.logger.WithContext(ctx).Warnf("[KnActionRecall#resolveSchema] Failed to resolve property %s: %v", propName, err)
					continue
				}
				resolvedProps[propName] = resolvedProp
			}
			schemaMap["properties"] = resolvedProps
		}
		// Handle array.items without changing depth.
		if schemaMap["type"] == "array" {
			if items, ok := schemaMap["items"].(map[string]any); ok {
				resolvedItems, err := s.resolveSchema(ctx, items, apiSpec, visitedRefs, currentDepth)
				if err != nil {
					s.logger.WithContext(ctx).Warnf("[KnActionRecall#resolveSchema] Failed to resolve array items: %v", err)
				} else {
					schemaMap["items"] = resolvedItems
				}
			}
		}
		return schemaMap, nil
	}

	// Handling $ref references.
	if refPath, ok := schemaMap["$ref"].(string); ok {
		// Check for circular references (must be done before deep checking to avoid infinite recursion)
		if visitedRefs[refPath] {
			// Circular reference detected, perform pruning.
			s.logger.WithContext(ctx).Debugf("[KnActionRecall#resolveSchema] Circular reference detected for %s (depth: %d), pruning", refPath, currentDepth)
			// Get basic information for the referenced schema, then prune it.
			referencedSchema, err := s.getReferencedSchema(refPath, apiSpec)
			if err != nil {
				s.logger.WithContext(ctx).Warnf("[KnActionRecall#resolveSchema] Failed to get referenced schema for pruning: %v", err)
				return map[string]any{"type": "object"}, nil
			}
			return s.pruneSchema(referencedSchema), nil
		}

		// Check if maximum depth is reached.
		if currentDepth >= MaxSchemaDepth {
			s.logger.WithContext(ctx).Debugf("[KnActionRecall#resolveSchema] Max depth reached for %s (depth: %d), pruning", refPath, currentDepth)
			// Get basic information for the referenced schema, then prune it.
			referencedSchema, err := s.getReferencedSchema(refPath, apiSpec)
			if err != nil {
				s.logger.WithContext(ctx).Warnf("[KnActionRecall#resolveSchema] Failed to get referenced schema for pruning: %v", err)
				return map[string]any{"type": "object"}, nil
			}
			return s.pruneSchema(referencedSchema), nil
		}

		// Mark as visited.
		wasVisited := visitedRefs[refPath]
		visitedRefs[refPath] = true
		defer func() {
			// When returning recursively, if this is the first visit, clean up the mark.
			if !wasVisited {
				delete(visitedRefs, refPath)
			}
		}()

		// Parse $ref paths (depth +1)
		resolvedSchema, err := s.resolveDollarRef(ctx, refPath, apiSpec, visitedRefs, currentDepth+1)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve $ref %s: %w", refPath, err)
		}

		return resolvedSchema, nil
	}

	// If there are properties, recursive processing (the depth remains unchanged, at the same level)
	if props, ok := schemaMap["properties"].(map[string]any); ok {
		resolvedProps := make(map[string]any)
		for propName, propDef := range props {
			resolvedProp, err := s.resolveSchema(ctx, propDef, apiSpec, visitedRefs, currentDepth)
			if err != nil {
				s.logger.WithContext(ctx).Warnf("[KnActionRecall#resolveSchema] Failed to resolve property %s: %v", propName, err)
				continue
			}
			resolvedProps[propName] = resolvedProp
		}
		schemaMap["properties"] = resolvedProps
	}

	// Handle array.items without changing depth.
	if schemaMap["type"] == "array" {
		if items, ok := schemaMap["items"].(map[string]any); ok {
			resolvedItems, err := s.resolveSchema(ctx, items, apiSpec, visitedRefs, currentDepth)
			if err != nil {
				s.logger.WithContext(ctx).Warnf("[KnActionRecall#resolveSchema] Failed to resolve array items: %v", err)
			} else {
				schemaMap["items"] = resolvedItems
			}
		}
	}

	return schemaMap, nil
}

// getReferencedSchema gets the referenced schema definition (does not parse, only gets basic information)
func (s *knActionRecallServiceImpl) getReferencedSchema(refPath string, apiSpec map[string]any) (map[string]any, error) {
	// Parse $ref path format: #/components/schemas/SchemaName.
	if !strings.HasPrefix(refPath, "#/components/schemas/") {
		return nil, fmt.Errorf("unsupported $ref path format: %s (only #/components/schemas/* is supported)", refPath)
	}

	schemaName := strings.TrimPrefix(refPath, "#/components/schemas/")
	if schemaName == "" {
		return nil, fmt.Errorf("empty schema name in $ref: %s", refPath)
	}

	// Find from components.schemas.
	components, ok := apiSpec["components"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("components not found in api_spec")
	}

	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("components.schemas not found in api_spec")
	}

	schema, ok := schemas[schemaName].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema %s not found in components.schemas", schemaName)
	}

	return schema, nil
}

// pruneSchema pruning function: when reaching the maximum depth, retain the type and original description, and remove properties.
// Core strategy: Do not add circular reference instructions to save tokens.
func (s *knActionRecallServiceImpl) pruneSchema(schema map[string]any) map[string]any {
	result := make(map[string]any)

	// Preserve type information.
	if schemaType, ok := schema["type"].(string); ok && schemaType != "" {
		result["type"] = schemaType
	} else {
		result["type"] = "object" // Default type.
	}

	// Keep the original description (if it exists, do not modify it, do not add a circular reference description)
	if desc, ok := schema["description"].(string); ok && desc != "" {
		result["description"] = desc
	}

	// If it is an array, retain the items structure but do not expand properties.
	if result["type"] == "array" {
		if items, ok := schema["items"].(map[string]any); ok {
			// Recursive pruning items.
			result["items"] = s.pruneSchema(items)
		}
	}

	// Do not include properties (avoid continuing recursion)
	// Do not add circular reference instructions (save tokens)

	return result
}

// resolveDollarRef resolves the $ref reference (complete implementation, supports circular reference detection and depth control)
func (s *knActionRecallServiceImpl) resolveDollarRef(
	ctx context.Context,
	refPath string,
	apiSpec map[string]any,
	visitedRefs map[string]bool,
	currentDepth int,
) (map[string]any, error) {
	// Get the referenced schema.
	schema, err := s.getReferencedSchema(refPath, apiSpec)
	if err != nil {
		return nil, err
	}

	// Recursive parsing (may contain nested $refs, passing depth information)
	return s.resolveSchema(ctx, schema, apiSpec, visitedRefs, currentDepth)
}

// buildPropertyDefinition build property definition.
func (s *knActionRecallServiceImpl) buildPropertyDefinition(schema map[string]any, description any) map[string]any {
	propDef := make(map[string]any)

	// Type.
	if propType, ok := schema["type"].(string); ok && propType != "" {
		propDef["type"] = propType
	} else {
		propDef["type"] = "string" // Default type.
	}

	// Description (preferably use parameter-level description, followed by description in schema)
	if desc, ok := description.(string); ok && desc != "" {
		propDef["description"] = desc
	} else if desc, ok := schema["description"].(string); ok && desc != "" {
		propDef["description"] = desc
	}

	// enumeration.
	if enum, ok := schema["enum"].([]any); ok {
		propDef["enum"] = enum
	}

	// If the schema has properties, retain the nested structure.
	if props, ok := schema["properties"].(map[string]any); ok {
		propDef["properties"] = props
		propDef["type"] = "object"
	}

	// If schema is an array, retain the items structure.
	if schema["type"] == "array" {
		if items, ok := schema["items"].(map[string]any); ok {
			propDef["items"] = items
		}
	}

	return propDef
}

// mergeSchemaProperties merges schema properties into target properties.
func (s *knActionRecallServiceImpl) mergeSchemaProperties(
	ctx context.Context,
	targetProps, schema, apiSpec map[string]any,
	visitedRefs map[string]bool,
	currentDepth int,
) {
	if props, ok := schema["properties"].(map[string]any); ok {
		for propName, propDef := range props {
			resolvedProp, err := s.resolveSchema(ctx, propDef, apiSpec, visitedRefs, currentDepth)
			if err != nil {
				s.logger.WithContext(ctx).Warnf("[KnActionRecall#mergeSchemaProperties] Failed to resolve property %s: %v", propName, err)
				continue
			}
			targetProps[propName] = s.buildPropertyDefinition(resolvedProp, nil)
		}
	}
}

// mapFixedParams maps fixed parameters to header/path/query/body.
func (s *knActionRecallServiceImpl) mapFixedParams(
	_ context.Context,
	parameters, apiSpec map[string]any,
) interfaces.KnFixedParams {
	fixedParams := interfaces.KnFixedParams{
		Header: make(map[string]any),
		Path:   make(map[string]any),
		Query:  make(map[string]any),
		Body:   make(map[string]any),
	}

	// Create a mapping table from parameter names to positions.
	paramLocationMap := make(map[string]string)
	if params, ok := apiSpec["parameters"].([]any); ok {
		for _, paramItem := range params {
			if param, ok := paramItem.(map[string]any); ok {
				if name, ok := param["name"].(string); ok {
					if in, ok := param["in"].(string); ok {
						paramLocationMap[name] = in
					}
				}
			}
		}
	}

	// Classification parameters according to mapping table.
	for key, value := range parameters {
		location := paramLocationMap[key]
		switch location {
		case "header":
			fixedParams.Header[key] = value
		case "path":
			fixedParams.Path[key] = value
		case "query":
			fixedParams.Query[key] = value
		case "body":
			fixedParams.Body[key] = value
		default:
			// No mapping found, use naming rules to determine.
			if isHeaderParam(key) {
				fixedParams.Header[key] = value
			} else {
				// Default is placed in body.
				fixedParams.Body[key] = value
			}
		}
	}

	return fixedParams
}

// isHeaderParam determines whether it is a header parameter (based on naming rules)
func isHeaderParam(key string) bool {
	// Common header parameter name patterns.
	headerPatterns := []string{
		"x-", "X-",
		"authorization", "Authorization",
		"content-type", "Content-Type",
	}

	for _, pattern := range headerPatterns {
		if len(key) >= len(pattern) && key[:len(pattern)] == pattern {
			return true
		}
	}

	return false
}

// convertMCPSchemaToFunctionCall Converts MCP JSON Schema to OpenAI Function Call Schema.
// NOTE: Use the generic resolveSchemaWithResolver function to parameterize $defs search logic via RefResolver.
func (s *knActionRecallServiceImpl) convertMCPSchemaToFunctionCall(ctx context.Context, inputSchema map[string]any) (map[string]any, error) {
	// OpenAI function call schema expects the root node to have type=object and properties.
	// The MCP schema is usually already a JSON Schema, but may contain $defs.
	// We need to parse the $defs and make sure the root structure meets OpenAI requirements.

	visitedRefs := make(map[string]bool)

	// Extract rootDefs ($defs)
	rootDefs := make(map[string]any)
	if defs, ok := inputSchema["$defs"].(map[string]any); ok {
		rootDefs = defs
	}

	// Build an MCP-specific reference resolver. MCP references use #/$defs/SchemaName.
	mcpRefResolver := func(refPath string) (map[string]any, error) {
		prefix := "#/$defs/"
		if !strings.HasPrefix(refPath, prefix) {
			return nil, fmt.Errorf("unsupported MCP $ref path format: %s (only #/$defs/* is supported)", refPath)
		}
		name := strings.TrimPrefix(refPath, prefix)
		if def, ok := rootDefs[name].(map[string]any); ok {
			return def, nil
		}
		return nil, fmt.Errorf("MCP schema definition not found: %s", name)
	}

	// Resolve the schema with the generic resolver.
	resolvedSchema, err := s.resolveSchemaWithResolver(ctx, inputSchema, mcpRefResolver, visitedRefs, 0)
	if err != nil {
		return nil, err
	}

	// Add a default description for the first-level body parameter when its
	// referenced schema does not define one.
	if props, ok := resolvedSchema["properties"].(map[string]any); ok {
		if bodyProp, ok := props["body"].(map[string]any); ok {
			if _, hasDesc := bodyProp["description"]; !hasDesc {
				bodyProp["description"] = infraerrors.LocalizedDetail(ctx, "ActionParamBody")
			}
		}
	}

	// Ensure the root has type=object.
	if _, ok := resolvedSchema["type"]; !ok {
		resolvedSchema["type"] = "object"
	}

	// Remove $defs because all definitions have been resolved inline.
	delete(resolvedSchema, "$defs")

	return resolvedSchema, nil
}

// ==================== Action Driver Schema conversion method ====================.

// convertToolSchemaToActionDriver Convert Tool OpenAPI Schema to action driver dynamic_params.
// Remove the header/path/query/body wrappers and merge all parameters into flat dynamic_params.properties.
// If fields with the same name come from different locations, an error will be returned.
func (s *knActionRecallServiceImpl) convertToolSchemaToActionDriver(ctx context.Context, apiSpec map[string]any) (map[string]any, error) {
	// Merged dynamic_params properties and required.
	dynamicProperties := make(map[string]any)
	dynamicRequired := []string{}

	// Record the mapping of parameter names to locations for conflict detection.
	paramLocationMap := make(map[string]string)

	// Visited records used for circular-reference detection.
	visitedRefs := make(map[string]bool)

	// 1. Handle parameters (path/query/header).
	if params, ok := apiSpec["parameters"].([]any); ok {
		for _, paramItem := range params {
			param, ok := paramItem.(map[string]any)
			if !ok {
				continue
			}

			paramName, _ := param["name"].(string)
			if paramName == "" {
				continue
			}

			paramLocation, _ := param["in"].(string)
			if paramLocation == "" {
				continue
			}

			// Conflict detection: fields with the same name come from different locations.
			if existingLocation, exists := paramLocationMap[paramName]; exists {
				if existingLocation != paramLocation {
					errMsg := fmt.Sprintf("parameter %q is duplicated across locations (existing: %s, current: %s); cannot build action driver tool",
						paramName, existingLocation, paramLocation)
					s.logger.WithContext(ctx).Errorf("[KnActionRecall#convertToolSchemaToActionDriver] %s", errMsg)
					return nil, fmt.Errorf("%s", errMsg)
				}
			}
			paramLocationMap[paramName] = paramLocation

			// Parseparameter schema.
			paramSchema, err := s.resolveSchema(ctx, param["schema"], apiSpec, visitedRefs, 0)
			if err != nil {
				s.logger.WithContext(ctx).Warnf("[KnActionRecall#convertToolSchemaToActionDriver] Failed to resolve param schema for %s: %v", paramName, err)
				continue
			}

			// Build parameter definitions and put them directly into dynamic_params.properties.
			propDef := s.buildPropertyDefinition(paramSchema, param["description"])
			dynamicProperties[paramName] = propDef

			// Collect required parameters.
			if isRequired, ok := param["required"].(bool); ok && isRequired {
				dynamicRequired = append(dynamicRequired, paramName)
			}
		}
	}

	// 2. Process request_body (body parameter) — remove the body shell and expand to dynamic_params.
	if requestBody, ok := apiSpec["request_body"].(map[string]any); ok {
		if content, ok := requestBody["content"].(map[string]any); ok {
			if appJSON, ok := content["application/json"].(map[string]any); ok {
				if schema, ok := appJSON["schema"].(map[string]any); ok {
					bodySchema, err := s.resolveSchema(ctx, schema, apiSpec, visitedRefs, 0)
					if err != nil {
						s.logger.WithContext(ctx).Warnf("[KnActionRecall#convertToolSchemaToActionDriver] Failed to resolve body schema: %v", err)
					} else {
						// Expand the properties of body schema to dynamic_params.
						if bodyProps, ok := bodySchema["properties"].(map[string]any); ok {
							for propName, propDef := range bodyProps {
								// conflict detection.
								if existingLocation, exists := paramLocationMap[propName]; exists {
									errMsg := fmt.Sprintf("parameter %q is duplicated across locations (existing: %s, current: body); cannot build action driver tool",
										propName, existingLocation)
									s.logger.WithContext(ctx).Errorf("[KnActionRecall#convertToolSchemaToActionDriver] %s", errMsg)
									return nil, fmt.Errorf("%s", errMsg)
								}
								paramLocationMap[propName] = "body"

								resolvedProp, resolveErr := s.resolveSchema(ctx, propDef, apiSpec, visitedRefs, 0)
								if resolveErr != nil {
									s.logger.WithContext(ctx).Warnf("[KnActionRecall#convertToolSchemaToActionDriver] Failed to resolve body property %s: %v", propName, resolveErr)
									continue
								}
								dynamicProperties[propName] = s.buildPropertyDefinition(resolvedProp, nil)
							}
						}

						// Merge body required — only add keys that actually exist in dynamicProperties.
						if bodyRequired, ok := bodySchema["required"].([]any); ok {
							for _, req := range bodyRequired {
								if reqStr, ok := req.(string); ok {
									if _, exists := dynamicProperties[reqStr]; exists {
										dynamicRequired = append(dynamicRequired, reqStr)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// 3. construct dynamic_params schema.
	dynamicParamsSchema := map[string]any{
		"type":        "object",
		"description": infraerrors.LocalizedDetail(ctx, "ActionDynamicParams"),
		"properties":  dynamicProperties,
	}
	if len(dynamicRequired) > 0 {
		dynamicParamsSchema["required"] = dynamicRequired
	}

	// 4. Packaging top-level action-driven structure.
	return s.wrapActionDriverParameters(ctx, dynamicParamsSchema), nil
}

// convertMCPSchemaToActionDriver Converts MCP Schema to action-driven request structure.
// Use the parsed MCP input_schema directly as the schema of dynamic_params.
func (s *knActionRecallServiceImpl) convertMCPSchemaToActionDriver(ctx context.Context, inputSchema map[string]any) (map[string]any, error) {
	resolvedSchema, err := s.resolveMCPSchema(ctx, inputSchema)
	if err != nil {
		return nil, err
	}

	// Construct dynamic_params schema: use the parsed MCP schema as dynamic_params.
	dynamicParamsSchema := map[string]any{
		"type":        "object",
		"description": infraerrors.LocalizedDetail(ctx, "ActionDynamicParams"),
	}
	if props, ok := resolvedSchema["properties"].(map[string]any); ok {
		dynamicParamsSchema["properties"] = props
	} else {
		dynamicParamsSchema["properties"] = make(map[string]any)
	}
	if required, ok := resolvedSchema["required"]; ok {
		dynamicParamsSchema["required"] = required
	}

	// Packaging top-level action-driven structure.
	return s.wrapActionDriverParameters(ctx, dynamicParamsSchema), nil
}

// wrapActionDriverParameters uniformly wraps the top-level action driver request parameter structure.
// The outermost layer is fixed to dynamic_params + _instance_identities.
func (s *knActionRecallServiceImpl) wrapActionDriverParameters(ctx context.Context, dynamicParamsSchema map[string]any) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dynamic_params": dynamicParamsSchema,
			"_instance_identities": map[string]any{
				"type":        "array",
				"description": infraerrors.LocalizedDetail(ctx, "ActionInstanceIdentities"),
				"items": map[string]any{
					"type":                 "object",
					"description":          infraerrors.LocalizedDetail(ctx, "ActionInstanceIdentity"),
					"additionalProperties": map[string]any{},
				},
			},
		},
	}
}

// resolveMCPSchema resolves an MCP schema: it inlines #/$defs/* references, defaults the
// type to object and drops $defs. Input and output schemas share this one path so the
// reference resolution is not written twice.
func (s *knActionRecallServiceImpl) resolveMCPSchema(ctx context.Context, schema map[string]any) (map[string]any, error) {
	visitedRefs := make(map[string]bool)

	rootDefs := make(map[string]any)
	if defs, ok := schema["$defs"].(map[string]any); ok {
		rootDefs = defs
	}

	mcpRefResolver := func(refPath string) (map[string]any, error) {
		prefix := "#/$defs/"
		if !strings.HasPrefix(refPath, prefix) {
			return nil, fmt.Errorf("unsupported MCP $ref path format: %s (only #/$defs/* is supported)", refPath)
		}
		name := strings.TrimPrefix(refPath, prefix)
		if def, ok := rootDefs[name].(map[string]any); ok {
			return def, nil
		}
		return nil, fmt.Errorf("MCP schema definition not found: %s", name)
	}

	resolved, err := s.resolveSchemaWithResolver(ctx, schema, mcpRefResolver, visitedRefs, 0)
	if err != nil {
		return nil, err
	}

	if _, ok := resolved["type"]; !ok {
		resolved["type"] = "object"
	}
	delete(resolved, "$defs")

	return resolved, nil
}

// extractToolOutputSchema derives the shape of the action result from the tool's OpenAPI spec.
//
// It takes the application/json schema of a 2xx response (200 first) and expands $ref through
// the same resolver used for input parameters. The response body is used as-is, including the
// {stdout, stderr, result, metrics} envelope of a function tool: ontology-query stores the whole
// body as the execution result (see ExecuteTool in agent_operator_access.go), so unwrapping the
// envelope here would describe a level that the result never has.
//
// Returns nil when nothing usable is found so the caller omits the field entirely: an empty
// object reads as "this action returns nothing" when the truth is "the output shape is unknown".
func (s *knActionRecallServiceImpl) extractToolOutputSchema(ctx context.Context, apiSpec map[string]any) map[string]any {
	response := s.pickSuccessResponse(apiSpec)
	if response == nil {
		return nil
	}

	content, ok := response["content"].(map[string]any)
	if !ok {
		return nil
	}
	appJSON, ok := content["application/json"].(map[string]any)
	if !ok {
		s.logger.WithContext(ctx).Debugf("[KnActionRecall#extractToolOutputSchema] Success response has no application/json content")
		return nil
	}
	rawSchema, ok := appJSON["schema"].(map[string]any)
	if !ok {
		return nil
	}

	resolved, err := s.resolveSchema(ctx, rawSchema, apiSpec, make(map[string]bool), 0)
	if err != nil {
		s.logger.WithContext(ctx).Warnf("[KnActionRecall#extractToolOutputSchema] Failed to resolve output schema: %v", err)
		return nil
	}

	return s.finalizeOutputSchema(ctx, resolved)
}

// pickSuccessResponse picks the response definition that describes the output: 200 first,
// then any other 2xx.
func (s *knActionRecallServiceImpl) pickSuccessResponse(apiSpec map[string]any) map[string]any {
	responses, ok := apiSpec["responses"].([]any)
	if !ok {
		return nil
	}

	var fallback map[string]any
	for _, item := range responses {
		response, ok := item.(map[string]any)
		if !ok {
			continue
		}
		statusCode, _ := response["status_code"].(string)
		if statusCode == responseStatusOK {
			return response
		}
		if fallback == nil && strings.HasPrefix(statusCode, "2") {
			fallback = response
		}
	}
	return fallback
}

// finalizeOutputSchema fills in the default type and description, and returns nil when the
// schema carries no usable shape.
func (s *knActionRecallServiceImpl) finalizeOutputSchema(ctx context.Context, schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return nil
	}

	_, hasType := schema["type"]
	_, hasProps := schema["properties"]
	if !hasType && !hasProps {
		return nil
	}
	if !hasType {
		schema["type"] = "object"
	}

	// An object without a single property says nothing; omit it rather than return it.
	if schema["type"] == "object" {
		if props, ok := schema["properties"].(map[string]any); !ok || len(props) == 0 {
			return nil
		}
	}

	if _, ok := schema["description"].(string); !ok {
		schema["description"] = infraerrors.LocalizedDetail(ctx, "ActionOutputSchema")
	}

	return schema
}
