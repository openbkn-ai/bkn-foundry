// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"fmt"
	"strings"

	"bkn-backend/interfaces"
	"bkn-backend/interfaces/data_type"
)

// applyRowFilters resolves all object classes the compiled plan reads in one
// bkn-safe call, then attaches a predicate to every table alias. Applying the
// predicate at table level is intentional: joins, aggregate functions, counts
// and pagination therefore operate on the visible subgraph, and no hidden
// row is materialised by this query service before it is excluded.
func (s *cypherQueryService) applyRowFilters(ctx context.Context, knID string,
	plan *Plan, schema *Schema) error {
	refs := make([]string, 0, len(plan.Tables))
	seen := make(map[string]struct{}, len(plan.Tables))
	for _, table := range plan.Tables {
		ref := interfaces.KNChildResourceID(knID, table.ObjectTypeID)
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	entries, err := s.ps.ResolveRowFilters(ctx, refs)
	if err != nil {
		return err
	}
	byRef := make(map[string]interfaces.RowFilterPredicate, len(entries))
	for _, entry := range entries {
		if _, duplicate := byRef[entry.ObjectTypeRef]; duplicate {
			return fmt.Errorf("duplicate row-filter decision for %q", entry.ObjectTypeRef)
		}
		byRef[entry.ObjectTypeRef] = entry.Predicate
	}
	if len(byRef) != len(refs) {
		return fmt.Errorf("incomplete row-filter decision")
	}

	filters := make([]PlanPredicate, 0, len(plan.Tables)+1)
	if plan.Where != nil {
		filters = append(filters, plan.Where)
	}
	for tableIndex, table := range plan.Tables {
		objectType := schema.objectTypesByID[table.ObjectTypeID]
		if objectType == nil {
			return fmt.Errorf("compiled row-filter table has unknown object type %q", table.ObjectTypeID)
		}
		predicate, exists := byRef[interfaces.KNChildResourceID(knID, table.ObjectTypeID)]
		if !exists {
			return fmt.Errorf("missing row-filter decision for %q", table.ObjectTypeID)
		}
		compiled, err := compileRowFilterPredicate(predicate, tableIndex, objectType, schema)
		if err != nil {
			return fmt.Errorf("compile row filter for object type %q: %w", table.ObjectTypeID, err)
		}
		if compiled != nil {
			filters = append(filters, compiled)
		}
	}
	plan.Where = combineRowFilterAnd(filters)
	return nil
}

func compileRowFilterPredicate(predicate interfaces.RowFilterPredicate, table int,
	objectType *interfaces.ObjectType, schema *Schema) (PlanPredicate, error) {
	switch predicate.Kind {
	case "true":
		return nil, nil
	case "false":
		return PlanNever{}, nil
	case "in", "not_in":
		property, err := rowFilterDataProperty(objectType, predicate.Property)
		if err != nil {
			return nil, err
		}
		column, err := schema.Column(objectType, predicate.Property)
		if err != nil {
			return nil, err
		}
		values, err := compileRowFilterValues(predicate.Values, property.Type)
		if err != nil {
			return nil, err
		}
		return PlanMembership{Table: table, Column: column, Property: predicate.Property, Values: values, Negated: predicate.Kind == "not_in"}, nil
	case "gt", "gte", "lt", "lte":
		return compileRowFilterComparison(predicate, table, objectType, schema)
	case "between":
		property, err := rowFilterDataProperty(objectType, predicate.Property)
		if err != nil {
			return nil, err
		}
		column, err := schema.Column(objectType, predicate.Property)
		if err != nil {
			return nil, err
		}
		values, err := compileRowFilterValues(predicate.Values, property.Type)
		if err != nil || len(values) != 2 {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("between row-filter requires two values")
		}
		return PlanLogical{Operator: "AND", Operands: []PlanPredicate{
			PlanCondition{Table: table, Column: column, Property: predicate.Property, Operator: ">=", Value: values[0]},
			PlanCondition{Table: table, Column: column, Property: predicate.Property, Operator: "<=", Value: values[1]},
		}}, nil
	case "and":
		children := make([]PlanPredicate, 0, len(predicate.Predicates))
		for _, child := range predicate.Predicates {
			compiled, err := compileRowFilterPredicate(child, table, objectType, schema)
			if err != nil {
				return nil, err
			}
			if _, never := compiled.(PlanNever); never {
				return PlanNever{}, nil
			}
			if compiled != nil {
				children = append(children, compiled)
			}
		}
		return combineRowFilterAnd(children), nil
	case "or":
		children := make([]PlanPredicate, 0, len(predicate.Predicates))
		for _, child := range predicate.Predicates {
			compiled, err := compileRowFilterPredicate(child, table, objectType, schema)
			if err != nil {
				return nil, err
			}
			if compiled == nil {
				return nil, nil
			}
			if _, never := compiled.(PlanNever); !never {
				children = append(children, compiled)
			}
		}
		switch len(children) {
		case 0:
			return PlanNever{}, nil
		case 1:
			return children[0], nil
		default:
			return PlanLogical{Operator: "OR", Operands: children}, nil
		}
	default:
		return nil, fmt.Errorf("unsupported row-filter predicate %q", predicate.Kind)
	}
}

func compileRowFilterComparison(predicate interfaces.RowFilterPredicate, table int,
	objectType *interfaces.ObjectType, schema *Schema) (PlanPredicate, error) {
	property, err := rowFilterDataProperty(objectType, predicate.Property)
	if err != nil {
		return nil, err
	}
	column, err := schema.Column(objectType, predicate.Property)
	if err != nil {
		return nil, err
	}
	values, err := compileRowFilterValues(predicate.Values, property.Type)
	if err != nil || len(values) != 1 {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("comparison row-filter requires one value")
	}
	operators := map[string]string{"gt": ">", "gte": ">=", "lt": "<", "lte": "<="}
	return PlanCondition{Table: table, Column: column, Property: predicate.Property, Operator: operators[predicate.Kind], Value: values[0]}, nil
}

func rowFilterDataProperty(objectType *interfaces.ObjectType, name string) (*interfaces.DataProperty, error) {
	for _, property := range objectType.DataProperties {
		if property != nil && property.Name == name {
			if property.MappedField == nil || strings.TrimSpace(property.MappedField.Name) == "" {
				return nil, fmt.Errorf("property %q has no mapped column", name)
			}
			return property, nil
		}
	}
	return nil, fmt.Errorf("property %q is not a mapped data property", name)
}

func compileRowFilterValues(values []interfaces.RowFilterValue, propertyType string) ([]Literal, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("row-filter has no values")
	}
	result := make([]Literal, 0, len(values))
	for _, value := range values {
		switch value.Type {
		case "string":
			if value.String == nil || !rowFilterValueTypeAllowed(propertyType, value.Type) {
				return nil, fmt.Errorf("string value does not match property type")
			}
			result = append(result, Literal{Kind: LiteralString, String: *value.String})
		case "integer":
			if value.Integer == nil || !rowFilterValueTypeAllowed(propertyType, value.Type) {
				return nil, fmt.Errorf("integer value does not match property type")
			}
			result = append(result, Literal{Kind: LiteralInteger, Integer: *value.Integer})
		case "boolean":
			if value.Boolean == nil || !rowFilterValueTypeAllowed(propertyType, value.Type) {
				return nil, fmt.Errorf("boolean value does not match property type")
			}
			result = append(result, Literal{Kind: LiteralBoolean, Boolean: *value.Boolean})
		default:
			return nil, fmt.Errorf("unsupported row-filter value type")
		}
	}
	return result, nil
}

func rowFilterValueTypeAllowed(propertyType, valueType string) bool {
	switch valueType {
	case "string":
		return data_type.SimpleTypeMapping[propertyType] == data_type.SimpleChar || data_type.DataType_IsString(propertyType)
	case "integer":
		return data_type.SimpleTypeMapping[propertyType] == data_type.SimpleInt
	case "boolean":
		return propertyType == data_type.DATATYPE_BOOLEAN || data_type.SimpleTypeMapping[propertyType] == data_type.SimpleBool
	default:
		return false
	}
}

func combineRowFilterAnd(predicates []PlanPredicate) PlanPredicate {
	flattened := make([]PlanPredicate, 0, len(predicates))
	for _, predicate := range predicates {
		if predicate == nil {
			continue
		}
		if _, never := predicate.(PlanNever); never {
			return PlanNever{}
		}
		if logical, ok := predicate.(PlanLogical); ok && logical.Operator == "AND" {
			flattened = append(flattened, logical.Operands...)
			continue
		}
		flattened = append(flattened, predicate)
	}
	switch len(flattened) {
	case 0:
		return nil
	case 1:
		return flattened[0]
	default:
		return PlanLogical{Operator: "AND", Operands: flattened}
	}
}
