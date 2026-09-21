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
	valueType, exactFilterable := ExactFilterValueType(property)
	if !exactFilterable {
		return nil, fmt.Errorf("is not an exactly filterable data property")
	}
	result := make([]any, 0, len(values))
	for _, value := range values {
		switch value.Type {
		case "string":
			if value.String == nil || valueType != value.Type {
				return nil, fmt.Errorf("string value does not match property type")
			}
			result = append(result, *value.String)
		case "integer":
			if value.Integer == nil || valueType != value.Type {
				return nil, fmt.Errorf("integer value does not match property type")
			}
			result = append(result, *value.Integer)
		case "boolean":
			if value.Boolean == nil || valueType != value.Type {
				return nil, fmt.Errorf("boolean value does not match property type")
			}
			result = append(result, *value.Boolean)
		default:
			return nil, fmt.Errorf("unsupported value type")
		}
	}
	return result, nil
}

// ExactFilterValueType is the single model-level capability check for a
// row-filter IN predicate. String properties must explicitly advertise IN
// support; that is the model's evidence that the mapped string field can be
// matched exactly. Integer and boolean fields have no ConditionOperations
// metadata in the existing object-model contract, but their scalar equality
// semantics are exact when mapped, so they intentionally do not depend on it.
// When the required proof or a supported scalar type is absent, callers must
// reject the policy rather than trying a best-effort predicate.
func ExactFilterValueType(property cond.DataProperty) (string, bool) {
	if strings.TrimSpace(property.Name) == "" || strings.TrimSpace(property.MappedField.Name) == "" {
		return "", false
	}
	switch {
	case property.Type == dtype.DATATYPE_KEYWORD,
		dtype.SimpleTypeMapping[property.Type] == dtype.SimpleChar && !isNonExactStringType(property.Type):
		return "string", supportsIn(property.ConditionOperations)
	case dtype.SimpleTypeMapping[property.Type] == dtype.SimpleInt:
		return "integer", true
	case property.Type == dtype.DATATYPE_BOOLEAN || dtype.SimpleTypeMapping[property.Type] == dtype.SimpleBool:
		return "boolean", true
	default:
		return "", false
	}
}

func supportsIn(operations []string) bool {
	for _, operation := range operations {
		if strings.EqualFold(strings.TrimSpace(operation), cond.OperationIn) {
			return true
		}
	}
	return false
}

func isNonExactStringType(propertyType string) bool {
	switch strings.ToLower(strings.TrimSpace(propertyType)) {
	case dtype.DATATYPE_TEXT, dtype.DATATYPE_BINARY, "json", "jsonb", "xml", "ntext", "nclob":
		return true
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
