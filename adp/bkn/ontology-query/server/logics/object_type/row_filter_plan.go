// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"fmt"
	"strings"

	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
	dtype "ontology-query/interfaces/data_type"
)

// compileRowFilter turns the small, validated bkn-safe predicate language
// into the existing ontology condition tree. It intentionally has no escape
// hatch for backend expressions: every property is checked against the
// published object type before Vega/OpenSearch receives the condition.
func compileRowFilter(predicate interfaces.RowFilterPredicate,
	objectType interfaces.ObjectType) (*cond.CondCfg, []string, bool, error) {
	properties := make(map[string]cond.DataProperty, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		properties[property.Name] = property
	}
	fields := map[string]struct{}{}
	compiled, noResults, err := compileRowFilterPredicate(predicate, properties, fields)
	if err != nil {
		return nil, nil, false, err
	}
	fieldNames := make([]string, 0, len(fields))
	for name := range fields {
		fieldNames = append(fieldNames, name)
	}
	return compiled, fieldNames, noResults, nil
}

func compileRowFilterPredicate(predicate interfaces.RowFilterPredicate,
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
		values, err := rowFilterValues(predicate.Values, property)
		if err != nil {
			return nil, false, fmt.Errorf("row-filter property %q: %w", predicate.Property, err)
		}
		fields[property.Name] = struct{}{}
		return &cond.CondCfg{Name: property.Name, Operation: cond.OperationIn,
			ValueOptCfg: cond.ValueOptCfg{Value: values}}, false, nil
	case "or":
		children := make([]*cond.CondCfg, 0, len(predicate.Predicates))
		for _, child := range predicate.Predicates {
			compiled, noResults, err := compileRowFilterPredicate(child, properties, fields)
			if err != nil {
				return nil, false, err
			}
			if compiled == nil && !noResults {
				return nil, false, nil // TRUE makes the entire OR TRUE.
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

func rowFilterValues(values []interfaces.RowFilterValue, property cond.DataProperty) ([]any, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("has no values")
	}
	result := make([]any, 0, len(values))
	for _, value := range values {
		switch value.Type {
		case "string":
			if value.String == nil || !rowFilterTypeAllows(property.Type, value.Type) {
				return nil, fmt.Errorf("string value does not match property type")
			}
			result = append(result, *value.String)
		case "integer":
			if value.Integer == nil || !rowFilterTypeAllows(property.Type, value.Type) {
				return nil, fmt.Errorf("integer value does not match property type")
			}
			result = append(result, *value.Integer)
		case "boolean":
			if value.Boolean == nil || !rowFilterTypeAllows(property.Type, value.Type) {
				return nil, fmt.Errorf("boolean value does not match property type")
			}
			result = append(result, *value.Boolean)
		default:
			return nil, fmt.Errorf("unsupported value type")
		}
	}
	return result, nil
}

func rowFilterTypeAllows(propertyType, valueType string) bool {
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

func andRowFilterCondition(user, row *cond.CondCfg) *cond.CondCfg {
	if user == nil {
		return row
	}
	if row == nil {
		return user
	}
	return &cond.CondCfg{Operation: cond.OperationAnd, SubConds: []*cond.CondCfg{user, row}}
}
