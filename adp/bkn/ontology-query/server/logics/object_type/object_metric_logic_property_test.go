// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func TestObjectMetricLiteralTypePreservesPreciseJSONNumbers(t *testing.T) {
	tests := []struct {
		name  string
		value json.Number
		want  string
	}{
		{name: "integer", value: json.Number("42"), want: "int64"},
		{name: "decimal", value: json.Number("100.25"), want: "decimal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := objectMetricLiteralType(tt.value); got != tt.want {
				t.Fatalf("objectMetricLiteralType(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestBuildObjectMetricQueryFromLogicPropertyTypesPreciseJSONNumberFilters(t *testing.T) {
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "order",
	}}
	query, err := buildObjectMetricQueryFromLogicProperty(objectType, []interfaces.Filter{{
		Name: "amount", Operation: ">", Value: json.Number("100.25"),
	}}, interfaces.MetricPropertyDynamicParams{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if query.Filter == nil || len(query.Filter.Right) != 1 || query.Filter.Right[0].ValueType != "decimal" {
		t.Fatalf("unexpected precise-number filter: %#v", query.Filter)
	}
}

func TestBuildObjectMetricQueryFromLogicPropertyUsesCompleteObjectIdentity(t *testing.T) {
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "order", PrimaryKeys: []string{"tenant_id", "order_id"},
	}}
	query, err := buildObjectMetricQueryFromLogicProperty(objectType, []interfaces.Filter{
		{Name: "tenant_id", Operation: "==", Value: "t1"},
		{Name: "order_id", Operation: "==", Value: "o1"},
	}, interfaces.MetricPropertyDynamicParams{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(query.InstanceIdentities) != 1 || query.InstanceIdentities[0]["tenant_id"] != "t1" || query.InstanceIdentities[0]["order_id"] != "o1" {
		t.Fatalf("unexpected instance identity: %#v", query.InstanceIdentities)
	}
}

func TestBuildObjectMetricQueryFromLogicPropertyRejectsPartialIdentity(t *testing.T) {
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "order", PrimaryKeys: []string{"tenant_id", "order_id"},
	}}
	_, err := buildObjectMetricQueryFromLogicProperty(objectType, []interfaces.Filter{
		{Name: "order_id", Operation: "==", Value: "o1"},
	}, interfaces.MetricPropertyDynamicParams{}, nil, nil)
	if err == nil {
		t.Fatal("partial compound identity must be rejected")
	}
}
