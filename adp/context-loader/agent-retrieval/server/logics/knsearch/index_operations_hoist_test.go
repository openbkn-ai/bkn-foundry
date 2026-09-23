// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func fullIndexOps() []interfaces.KnOperationType {
	return []interfaces.KnOperationType{
		interfaces.KnOperationTypeMatch,
		interfaces.KnOperationTypeMultiMatch,
		interfaces.KnOperationTypeKnn,
	}
}

// The list the indexed properties share moves to the object type, and the properties that
// differ keep their own, so a reader still sees which fields can do what.
func TestTrimHoistsTheSharedIndexOperations(t *testing.T) {
	objType := &interfaces.KnSearchObjectType{
		ConceptID: "sales_order",
		DataProperties: []*interfaces.KnSearchDataProperty{
			{Name: "order_id", Type: "string", ConditionOperations: fullIndexOps()},
			{Name: "customer_name", Type: "string", ConditionOperations: fullIndexOps()},
			{Name: "remark", Type: "text", ConditionOperations: []interfaces.KnOperationType{
				interfaces.KnOperationTypeMatch,
			}},
			{Name: "signing_date", Type: "date", ConditionOperations: []interfaces.KnOperationType{
				interfaces.KnOperationTypeEqual,
			}},
		},
	}

	trimToIndexBackedOperations([]*interfaces.KnSearchObjectType{objType}, true)

	if got := operationsKey(objType.IndexOperations); got != "match,multi_match,knn" {
		t.Fatalf("the shared list belongs on the object type, got %q", got)
	}
	for _, p := range objType.DataProperties[:2] {
		if !p.Indexed {
			t.Fatalf("%s shares the object type's list and must say so, got %+v", p.Name, p)
		}
		if len(p.ConditionOperations) != 0 {
			t.Fatalf("%s must not repeat the shared list, got %+v", p.Name, p.ConditionOperations)
		}
	}

	remark := objType.DataProperties[2]
	if remark.Indexed {
		t.Fatalf("remark does not have the shared capability and must not claim it, got %+v", remark)
	}
	if operationsKey(remark.ConditionOperations) != "match" {
		t.Fatalf("a property that differs keeps its own list, got %+v", remark.ConditionOperations)
	}

	date := objType.DataProperties[3]
	if date.Indexed || len(date.ConditionOperations) != 0 {
		t.Fatalf("a property with no index carries neither key, got %+v", date)
	}
}

// Lifting a list that only one property carries would move bytes rather than save them, and
// would cost the reader a second place to look.
func TestTrimKeepsAnUnsharedListInline(t *testing.T) {
	objType := &interfaces.KnSearchObjectType{
		ConceptID: "product",
		DataProperties: []*interfaces.KnSearchDataProperty{
			{Name: "material_name", Type: "string", ConditionOperations: fullIndexOps()},
			{Name: "material_code", Type: "string"},
		},
	}

	trimToIndexBackedOperations([]*interfaces.KnSearchObjectType{objType}, true)

	if len(objType.IndexOperations) != 0 {
		t.Fatalf("nothing to share, got %+v", objType.IndexOperations)
	}
	if objType.DataProperties[0].Indexed {
		t.Fatalf("without a shared list the property cannot point at one, got %+v", objType.DataProperties[0])
	}
	if operationsKey(objType.DataProperties[0].ConditionOperations) != "match,multi_match,knn" {
		t.Fatalf("the only indexed property keeps its list, got %+v", objType.DataProperties[0].ConditionOperations)
	}
}

// The REST surface publishes every operator on every property, and this response shape does
// not reach it.
func TestRESTSurfaceKeepsOperationsOnEveryProperty(t *testing.T) {
	objType := &interfaces.KnSearchObjectType{
		ConceptID: "supplier",
		DataProperties: []*interfaces.KnSearchDataProperty{
			{Name: "supplier_code", Type: "string", ConditionOperations: fullIndexOps()},
			{Name: "supplier_name", Type: "string", ConditionOperations: fullIndexOps()},
		},
	}

	trimToIndexBackedOperations([]*interfaces.KnSearchObjectType{objType}, false)

	if len(objType.IndexOperations) != 0 {
		t.Fatalf("REST keeps the shape it had, got %+v", objType.IndexOperations)
	}
	for _, p := range objType.DataProperties {
		if p.Indexed || len(p.ConditionOperations) != 3 {
			t.Fatalf("REST keeps every property's own list, got %+v", p)
		}
	}
}

// Two properties with the same capability must produce the same list, whatever order
// bkn-backend sent it in, or neither would be recognised as shared.
func TestIndexOperationsHaveAFixedOrder(t *testing.T) {
	objType := &interfaces.KnSearchObjectType{
		ConceptID: "material",
		DataProperties: []*interfaces.KnSearchDataProperty{
			{Name: "a", ConditionOperations: []interfaces.KnOperationType{
				interfaces.KnOperationTypeKnn,
				interfaces.KnOperationTypeMatch,
			}},
			{Name: "b", ConditionOperations: []interfaces.KnOperationType{
				interfaces.KnOperationTypeMatch,
				interfaces.KnOperationTypeKnn,
			}},
		},
	}

	trimToIndexBackedOperations([]*interfaces.KnSearchObjectType{objType}, true)

	if got := operationsKey(objType.IndexOperations); got != "match,knn" {
		t.Fatalf("the published order is fixed, got %q", got)
	}
	for _, p := range objType.DataProperties {
		if !p.Indexed {
			t.Fatalf("%s has the shared capability, got %+v", p.Name, p)
		}
	}
}
