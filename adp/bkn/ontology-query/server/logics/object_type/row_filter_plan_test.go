// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"testing"

	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
)

func TestCompileRowFilterBuildsOnlyExactMappedPredicates(t *testing.T) {
	east := "east"
	west := "west"
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		DataProperties: []cond.DataProperty{{Name: "region", Type: "keyword", MappedField: cond.Field{Name: "region.keyword"}, ConditionOperations: []string{cond.OperationIn}}},
	}}
	compiled, fields, noResults, err := compileRowFilter(interfaces.RowFilterPredicate{
		Kind: "or", Predicates: []interfaces.RowFilterPredicate{
			{Kind: "in", Property: "region", Values: []interfaces.RowFilterValue{{Type: "string", String: &east}}},
			{Kind: "in", Property: "region", Values: []interfaces.RowFilterValue{{Type: "string", String: &west}}},
		},
	}, objectType)
	if err != nil || noResults || compiled == nil || compiled.Operation != cond.OperationOr || len(compiled.SubConds) != 2 {
		t.Fatalf("compileRowFilter() = %#v, %#v, %v, %v", compiled, fields, noResults, err)
	}
	if len(fields) != 1 || fields[0] != "region" {
		t.Fatalf("row-filter fields = %#v", fields)
	}
}

func TestCompileRowFilterRejectsUnmappedOrWronglyTypedFields(t *testing.T) {
	value := int64(1)
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		DataProperties: []cond.DataProperty{{Name: "region", Type: "keyword", MappedField: cond.Field{Name: "region.keyword"}, ConditionOperations: []string{cond.OperationIn}}},
	}}
	if _, _, _, err := compileRowFilter(interfaces.RowFilterPredicate{
		Kind: "in", Property: "region", Values: []interfaces.RowFilterValue{{Type: "integer", Integer: &value}},
	}, objectType); err == nil {
		t.Fatal("row filter with a mismatched value type must fail closed")
	}
	if _, _, _, err := compileRowFilter(interfaces.RowFilterPredicate{
		Kind: "in", Property: "unknown", Values: []interfaces.RowFilterValue{{Type: "integer", Integer: &value}},
	}, objectType); err == nil {
		t.Fatal("row filter with an unknown field must fail closed")
	}
}

func TestCompileRowFilterRejectsFieldsWithoutExactCapability(t *testing.T) {
	value := "east"
	for _, property := range []cond.DataProperty{
		{Name: "region", Type: "keyword", MappedField: cond.Field{Name: "region.keyword"}},
		{Name: "description", Type: "text", MappedField: cond.Field{Name: "description"}, ConditionOperations: []string{cond.OperationIn}},
	} {
		objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{DataProperties: []cond.DataProperty{property}}}
		if _, _, _, err := compileRowFilter(interfaces.RowFilterPredicate{
			Kind: "in", Property: property.Name, Values: []interfaces.RowFilterValue{{Type: "string", String: &value}},
		}, objectType); err == nil {
			t.Fatalf("row filter for %#v must fail closed", property)
		}
	}
}

func TestCompileRowFilterFalseReadsNoRows(t *testing.T) {
	compiled, fields, noResults, err := compileRowFilter(interfaces.RowFilterPredicate{Kind: "false"}, interfaces.ObjectType{})
	if err != nil || compiled != nil || len(fields) != 0 || !noResults {
		t.Fatalf("FALSE row filter = %#v, %#v, %v, %v", compiled, fields, noResults, err)
	}
}
