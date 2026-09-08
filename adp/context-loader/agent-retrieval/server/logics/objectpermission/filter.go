// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package objectpermission applies ontology-query's effective property plan to
// Context Loader schema models. Physical resource tools intentionally do not
// use this package: their authorization boundary is Vega resource permission.
package objectpermission

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// FilterObjectTypes clones and filters object types using the authorization-safe
// schema result from ontology-query. A missing or unknown decision is denied.
func FilterObjectTypes(ctx context.Context, access interfaces.ObjectSchemaAccess, knID string,
	objectTypes []*interfaces.ObjectType) ([]*interfaces.ObjectType, error) {
	if access == nil {
		return objectTypes, nil
	}
	result := make([]*interfaces.ObjectType, 0, len(objectTypes))
	for _, objectType := range objectTypes {
		if objectType == nil {
			continue
		}
		// ontology-query can only build a property plan for a published resource
		// binding. Keep an unbound object discoverable, but expose no properties.
		if objectType.DataSource == nil || strings.TrimSpace(objectType.DataSource.ID) == "" ||
			(strings.TrimSpace(objectType.DataSource.Type) != "" && objectType.DataSource.Type != "resource") {
			result = append(result, filterObjectType(objectType, nil))
			continue
		}
		schema, err := access.GetObjectTypeSchema(ctx, knID, objectType.ID)
		if err != nil {
			return nil, err
		}
		if schema == nil {
			return nil, fmt.Errorf("ontology-query returned an empty schema for object type %q", objectType.ID)
		}
		result = append(result, filterObjectType(objectType, schema.EffectivePermissions))
	}
	return result, nil
}

func filterObjectType(source *interfaces.ObjectType,
	permissions map[string]interfaces.PropertyAccessLevel) *interfaces.ObjectType {
	filtered := *source
	filtered.EffectivePermissions = clonePermissions(permissions)
	filtered.DataProperties = make([]*interfaces.DataProperty, 0, len(source.DataProperties))
	for _, property := range source.DataProperties {
		if property == nil || !schemaVisible(permissions[property.Name]) {
			continue
		}
		copy := *property
		if permissions[property.Name] != interfaces.PropertyAccessFull {
			copy.ConditionOperations = nil
		}
		filtered.DataProperties = append(filtered.DataProperties, &copy)
	}
	filtered.PrimaryKeys = make([]string, 0, len(source.PrimaryKeys))
	for _, name := range source.PrimaryKeys {
		if schemaVisible(permissions[name]) {
			filtered.PrimaryKeys = append(filtered.PrimaryKeys, name)
		}
	}
	filtered.LogicProperties = make([]*interfaces.LogicPropertyDef, 0, len(source.LogicProperties))
	for _, property := range source.LogicProperties {
		if property != nil && logicPropertyVisible(property.Parameters, permissions) {
			copy := *property
			filtered.LogicProperties = append(filtered.LogicProperties, &copy)
		}
	}
	return &filtered
}

func schemaVisible(level interfaces.PropertyAccessLevel) bool {
	switch level {
	case interfaces.PropertyAccessSchema, interfaces.PropertyAccessMasked, interfaces.PropertyAccessFull:
		return true
	default:
		return false
	}
}

func logicPropertyVisible(parameters []interfaces.PropertyParameter,
	permissions map[string]interfaces.PropertyAccessLevel) bool {
	for _, parameter := range parameters {
		if parameter.ValueFrom != "property" {
			continue
		}
		name, ok := parameter.Value.(string)
		if !ok || name == "" || permissions[name] != interfaces.PropertyAccessFull {
			return false
		}
	}
	return true
}

func clonePermissions(source map[string]interfaces.PropertyAccessLevel) map[string]interfaces.PropertyAccessLevel {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]interfaces.PropertyAccessLevel, len(source))
	for name, level := range source {
		if schemaVisible(level) {
			result[name] = level
		}
	}
	return result
}

// TrimObjectTypesToIndexBackedOps keeps only operators whose availability
// cannot be inferred from the property type. REST and MCP both call this helper
// so their advertised schema stays byte-for-byte equivalent.
func TrimObjectTypesToIndexBackedOps(objectTypes []*interfaces.ObjectType) {
	for _, objectType := range objectTypes {
		if objectType == nil {
			continue
		}
		for _, property := range objectType.DataProperties {
			if property == nil {
				continue
			}
			var filtered []interfaces.KnOperationType
			for _, operation := range property.ConditionOperations {
				switch operation {
				case interfaces.KnOperationTypeMatch, interfaces.KnOperationTypeMultiMatch, interfaces.KnOperationTypeKnn:
					filtered = append(filtered, operation)
				}
			}
			property.ConditionOperations = filtered
		}
	}
}
