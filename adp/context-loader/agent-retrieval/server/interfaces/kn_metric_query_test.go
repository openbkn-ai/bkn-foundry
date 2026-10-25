// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"testing"
)

func TestObjectMetricQueryConditionMatchesOntologyQueryWireContract(t *testing.T) {
	raw := []byte(`{
		"filter": {
			"node_type": "group",
			"boolean_operator": "not",
			"children": [{
				"node_type": "predicate",
				"left": {"source": "property", "property_ref": {"object_type_id": "order", "property_name": "status"}},
				"comparison_operator": "in",
				"right": [
					{"source": "literal", "value_type": "string", "value": "cancelled"},
					{"source": "literal", "value_type": "string", "value": "closed"}
				]
			}]
		}
	}`)

	var query ObjectMetricQuery
	if err := json.Unmarshal(raw, &query); err != nil {
		t.Fatal(err)
	}
	if query.Filter == nil || query.Filter.NodeType != "group" || query.Filter.BooleanOperator != "not" || len(query.Filter.Children) != 1 {
		t.Fatalf("unexpected condition group: %#v", query.Filter)
	}
	predicate := query.Filter.Children[0]
	if predicate.Left == nil || predicate.Left.PropertyRef == nil || predicate.Left.PropertyRef.PropertyName != "status" ||
		predicate.ComparisonOperator != "in" || len(predicate.Right) != 2 || predicate.Right[1].Value != "closed" {
		t.Fatalf("unexpected predicate: %#v", predicate)
	}

	encoded, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	filter, ok := roundTrip["filter"].(map[string]any)
	if !ok || filter["node_type"] != "group" || filter["boolean_operator"] != "not" {
		t.Fatalf("wire contract changed after round trip: %s", encoded)
	}
}
