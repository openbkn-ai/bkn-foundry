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
			{Name: "region", Type: "keyword", MappedField: cond.Field{Name: "region.keyword"}},
			{Name: "amount", Type: "integer", MappedField: cond.Field{Name: "amount"}},
			{Name: "active", Type: "boolean", MappedField: cond.Field{Name: "active"}},
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
}

func TestRowFilterCapabilityRejectsIndexUnavailableObjectType(t *testing.T) {
	capability := rowFilterCapabilityForObjectType("kn-1/customer", interfaces.ObjectType{})
	if capability.Published {
		t.Fatal("index-unavailable object type must not be published")
	}
}
