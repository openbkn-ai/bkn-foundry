// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_target

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// ExecutionSchema describes the request before any input value or instance is
// supplied. Discovery never scans applicability conditions to invent targets.
func (s *Resolver) ExecutionSchema(ctx context.Context, action *interfaces.ActionType, knID, branch string) (map[string]any, error) {
	if err := ValidateDefinition(action); err != nil {
		return nil, actionTargetError(ctx, err.Error())
	}
	properties := make(map[string]any)
	required := []string{}
	inputs := make(map[string]any)
	inputNames := []string{}
	for _, param := range action.Parameters {
		if param.ValueFrom == "input" {
			if err := addInputSchema(inputs, &inputNames, strings.Split(param.Name, "."), schemaForType(param.Type)); err != nil {
				return nil, actionTargetError(ctx, err.Error())
			}
		}
	}
	if len(inputs) > 0 {
		properties["dynamic_params"] = map[string]any{"type": "object", "properties": inputs, "required": inputNames, "additionalProperties": false}
		required = append(required, "dynamic_params")
	}
	getObject := func(id string) (interfaces.ObjectType, error) {
		object, exists, err := s.Models.GetObjectType(ctx, knID, branch, id)
		if err != nil {
			return object, err
		}
		if !exists {
			return object, actionTargetError(ctx, "action object type does not exist")
		}
		return object, nil
	}
	switch action.OperationConfig.Type {
	case "object":
		object, err := getObject(action.ObjectTypeID)
		if err != nil {
			return nil, err
		}
		if action.ActionType == "add" {
			fields := make(map[string]any)
			requiredFields := []string{}
			mapped := mappedObjectInputProperties(action.Parameters)
			for _, property := range object.DataProperties {
				if !mapped[property.Name] {
					continue
				}
				field := schemaForType(property.Type)
				field["title"] = property.DisplayName
				fields[property.Name] = field
				requiredFields = append(requiredFields, property.Name)
			}
			for name := range mapped {
				if _, exists := fields[name]; !exists {
					return nil, actionTargetError(ctx, fmt.Sprintf("execution parameter maps unknown object property %q", name))
				}
			}
			if len(requiredFields) > 0 {
				properties["object_input"] = map[string]any{"type": "object", "title": object.OTName, "properties": fields, "required": requiredFields, "additionalProperties": false}
				required = append(required, "object_input")
			}
		} else {
			identity, err := identitySchema(&object)
			if err != nil {
				return nil, actionTargetError(ctx, err.Error())
			}
			properties["_instance_identities"] = map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": identity}
			required = append(required, "_instance_identities")
		}
	case "relation":
		relation, exists, err := s.Models.GetRelationType(ctx, knID, branch, action.OperationConfig.RelationTypeID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, actionTargetError(ctx, "action relation type does not exist")
		}
		endpoints := make(map[string]any)
		for key, objectID := range map[string]string{"source_identity": relation.SourceObjectTypeID, "target_identity": relation.TargetObjectTypeID} {
			object, err := getObject(objectID)
			if err != nil {
				return nil, err
			}
			if len(object.PrimaryKeys) != 1 {
				return nil, actionTargetError(ctx, "relation endpoints require exactly one primary key")
			}
			schema, err := identitySchema(&object)
			if err != nil {
				return nil, actionTargetError(ctx, err.Error())
			}
			endpoints[key] = schema
		}
		properties["relation_target"] = map[string]any{"type": "object", "properties": endpoints, "required": []string{"source_identity", "target_identity"}, "additionalProperties": false}
		required = append(required, "relation_target")
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}, nil
}

func identitySchema(object *interfaces.ObjectType) (map[string]any, error) {
	if len(object.PrimaryKeys) == 0 {
		return nil, fmt.Errorf("object type %q has no primary keys", object.OTID)
	}
	fields := make(map[string]any)
	for _, key := range object.PrimaryKeys {
		for _, property := range object.DataProperties {
			if property.Name == key {
				field := schemaForType(property.Type)
				field["title"] = property.DisplayName
				fields[key] = field
				break
			}
		}
		if fields[key] == nil {
			return nil, fmt.Errorf("object type %q has an undefined primary key %q", object.OTID, key)
		}
	}
	return map[string]any{"type": "object", "title": object.OTName, "properties": fields, "required": object.PrimaryKeys, "additionalProperties": false}, nil
}

func schemaForType(value string) map[string]any {
	switch strings.ToLower(value) {
	case "int", "int8", "int16", "int32", "int64", "integer", "long", "short", "uint", "uint64":
		return map[string]any{"type": "integer"}
	case "float", "float32", "float64", "double", "decimal", "number":
		return map[string]any{"type": "number"}
	case "bool", "boolean":
		return map[string]any{"type": "boolean"}
	case "array":
		return map[string]any{"type": "array", "items": map[string]any{}}
	case "object":
		return map[string]any{"type": "object", "additionalProperties": true}
	case "date":
		return map[string]any{"type": "string", "format": "date"}
	case "datetime":
		return map[string]any{"type": "string", "format": "date-time"}
	default:
		return map[string]any{"type": "string"}
	}
}

// addInputSchema follows the same dotted paths as parameter binding.
func addInputSchema(properties map[string]any, required *[]string, path []string, leaf map[string]any) error {
	key := path[0]
	if key == "" || key == "__proto__" || key == "constructor" || key == "prototype" {
		return fmt.Errorf("invalid dynamic parameter path")
	}
	if len(path) == 1 {
		if _, exists := properties[key]; exists {
			return fmt.Errorf("overlapping dynamic parameter paths")
		}
		properties[key] = leaf
	} else {
		child, exists := properties[key].(map[string]any)
		if !exists {
			child = map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "additionalProperties": false}
			properties[key] = child
		}
		nested, ok := child["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("overlapping dynamic parameter paths")
		}
		childRequired, _ := child["required"].([]string)
		if err := addInputSchema(nested, &childRequired, path[1:], leaf); err != nil {
			return err
		}
		child["required"] = childRequired
	}
	for _, item := range *required {
		if item == key {
			return nil
		}
	}
	*required = append(*required, key)
	return nil
}
