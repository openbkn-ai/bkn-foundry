// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package row_filter compiles bkn-safe's restricted policy language into the
// common ontology condition tree. It owns no query execution, so every
// instance-reading exit can share the same validation.
package row_filter

import (
	"fmt"
	"strings"

	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
	dtype "ontology-query/interfaces/data_type"
)

func Compile(predicate interfaces.RowFilterPredicate,
	objectType interfaces.ObjectType) (*cond.CondCfg, []string, bool, error) {
	properties := make(map[string]cond.DataProperty, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		properties[property.Name] = property
	}
	fields := map[string]struct{}{}
	compiled, noResults, err := compilePredicate(predicate, properties, fields)
	if err != nil {
		return nil, nil, false, err
	}
	fieldNames := make([]string, 0, len(fields))
	for name := range fields {
		fieldNames = append(fieldNames, name)
	}
	return compiled, fieldNames, noResults, nil
}

func compilePredicate(predicate interfaces.RowFilterPredicate,
	properties map[string]cond.DataProperty, fields map[string]struct{}) (*cond.CondCfg, bool, error) {
	switch predicate.Kind {
	case "true":
		return nil, false, nil
	case "false":
		return nil, true, nil
	case "in":
		property, exists := properties[predicate.Property]
		if !exists || strings.TrimSpace(property.MappedField.Name) == "" {
			return nil, false, fmt.Errorf("row-filter property %q is not a mapped data property", predicate.Property)
		}
		values, err := valuesForProperty(predicate.Values, property)
		if err != nil {
			return nil, false, fmt.Errorf("row-filter property %q: %w", predicate.Property, err)
		}
		fields[property.Name] = struct{}{}
		return &cond.CondCfg{Name: property.Name, Operation: cond.OperationIn,
			ValueOptCfg: cond.ValueOptCfg{Value: values}}, false, nil
	case "or":
		children := make([]*cond.CondCfg, 0, len(predicate.Predicates))
		for _, child := range predicate.Predicates {
			compiled, noResults, err := compilePredicate(child, properties, fields)
			if err != nil {
				return nil, false, err
			}
			if compiled == nil && !noResults {
				return nil, false, nil
			}
			if !noResults {
				children = append(children, compiled)
			}
		}
		if len(children) == 0 {
			return nil, true, nil
		}
		if len(children) == 1 {
			return children[0], false, nil
		}
		return &cond.CondCfg{Operation: cond.OperationOr, SubConds: children}, false, nil
	default:
		return nil, false, fmt.Errorf("row-filter predicate %q is unsupported", predicate.Kind)
	}
}

func valuesForProperty(values []interfaces.RowFilterValue, property cond.DataProperty) ([]any, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("has no values")
	}
	result := make([]any, 0, len(values))
	for _, value := range values {
		switch value.Type {
		case "string":
			if value.String == nil || !typeAllows(property.Type, value.Type) {
				return nil, fmt.Errorf("string value does not match property type")
			}
			result = append(result, *value.String)
		case "integer":
			if value.Integer == nil || !typeAllows(property.Type, value.Type) {
				return nil, fmt.Errorf("integer value does not match property type")
			}
			result = append(result, *value.Integer)
		case "boolean":
			if value.Boolean == nil || !typeAllows(property.Type, value.Type) {
				return nil, fmt.Errorf("boolean value does not match property type")
			}
			result = append(result, *value.Boolean)
		default:
			return nil, fmt.Errorf("unsupported value type")
		}
	}
	return result, nil
}

func typeAllows(propertyType, valueType string) bool {
	switch valueType {
	case "string":
		return dtype.SimpleTypeMapping[propertyType] == dtype.SimpleChar || dtype.DataType_IsString(propertyType)
	case "integer":
		return dtype.SimpleTypeMapping[propertyType] == dtype.SimpleInt || dtype.DataType_IsNumber(propertyType)
	case "boolean":
		return propertyType == dtype.DATATYPE_BOOLEAN || dtype.SimpleTypeMapping[propertyType] == dtype.SimpleBool
	default:
		return false
	}
}

func And(user, row *cond.CondCfg) *cond.CondCfg {
	if user == nil {
		return row
	}
	if row == nil {
		return user
	}
	return &cond.CondCfg{Operation: cond.OperationAnd, SubConds: []*cond.CondCfg{user, row}}
}
