// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"testing"

	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
)

func TestRowFilterCapabilityOnlyOffersPublishedMappedScalarDataProperties(t *testing.T) {
	capability := rowFilterCapabilityForObjectType("kn-1/customer", interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{DataProperties: []cond.DataProperty{
			{Name: "region", Type: "keyword", MappedField: cond.Field{Name: "region.keyword"}, ConditionOperations: []string{cond.OperationIn}},
			{Name: "amount", Type: "integer", MappedField: cond.Field{Name: "amount"}, ConditionOperations: []string{cond.OperationIn}},
			{Name: "active", Type: "boolean", MappedField: cond.Field{Name: "active"}, ConditionOperations: []string{cond.OperationIn}},
			{Name: "description", Type: "text", MappedField: cond.Field{Name: "description"}, ConditionOperations: []string{cond.OperationIn}},
			{Name: "ratio", Type: "double", MappedField: cond.Field{Name: "ratio"}, ConditionOperations: []string{cond.OperationIn}},
			{Name: "unmapped", Type: "keyword"},
			{Name: "when", Type: "datetime", MappedField: cond.Field{Name: "when"}},
		}},
		Status: &interfaces.ObjectTypeStatus{IndexAvailable: true},
	})
	if !capability.Published || len(capability.Properties) != 3 {
		t.Fatalf("capability = %+v", capability)
	}
	if capability.Properties["region"].Type != "string" || capability.Properties["amount"].Type != "integer" || capability.Properties["active"].Type != "boolean" {
		t.Fatalf("properties = %+v", capability.Properties)
	}
	if _, found := capability.Properties["description"]; found {
		t.Fatal("non-exact text field must not be exposed")
	}
	if _, found := capability.Properties["ratio"]; found {
		t.Fatal("floating-point field must not be exposed")
	}
}

func TestRowFilterCapabilityDoesNotTreatIndexStateAsPublication(t *testing.T) {
	capability := rowFilterCapabilityForObjectType("kn-1/customer", interfaces.ObjectType{})
	if !capability.Published {
		t.Fatal("an existing MAIN object model is published even before index status is available")
	}
}
