// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// declaredObjectTypeJSON is an object type as bkn-backend returns it with BKN spec 3.0 property
// declarations (SPECIFICATION.md §6.8): semantic and required on the property, unit, crs and
// timezone under constraints, next to constraint keys search_schema does not surface.
const declaredObjectTypeJSON = `{
	"id": "ot_store",
	"name": "store",
	"data_source": {"type": "resource", "id": "RID1", "name": "stores"},
	"primary_keys": ["store_id"],
	"data_properties": [
		{"name": "store_id", "type": "string", "comment": "Store id"},
		{"name": "revenue", "type": "decimal", "comment": "Revenue", "required": true,
			"constraints": {"range": {"min": 0}, "unit": "元"}},
		{"name": "location", "type": "point", "semantic": "location",
			"constraints": {"crs": "gcj02", "unique": true}},
		{"name": "opened_at", "type": "datetime", "semantic": "event_time", "required": true,
			"constraints": {"timezone": "Asia/Shanghai"}},
		{"name": "status", "type": "string", "semantic": "status", "constraints": {}}
	]
}`

// undeclaredObjectTypeJSON is an object type of a network without declarations.
const undeclaredObjectTypeJSON = `{
	"id": "ot_order",
	"name": "order",
	"comment": "Order",
	"tags": ["t1"],
	"data_source": {"type": "resource", "id": "RID2", "name": "orders"},
	"primary_keys": ["id"],
	"data_properties": [
		{"name": "id", "type": "string", "comment": "Order id"},
		{"name": "amount", "type": "double", "comment": "Amount", "condition_operations": ["==", "in"]}
	]
}`

func convertObjectTypeJSON(t *testing.T, raw string, brief bool) string {
	t.Helper()
	var obj interfaces.ObjectType
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		t.Fatalf("unmarshal object type: %v", err)
	}
	local := (&localSearchImpl{}).convertObjectTypesToLocal([]*interfaces.ObjectType{&obj}, brief, false)
	out, err := json.Marshal(local[0].DataProperties)
	if err != nil {
		t.Fatalf("marshal data properties: %v", err)
	}
	return string(out)
}

func TestConvertObjectTypesToLocal_PropertyDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		brief bool
		want  string
	}{
		{
			name: "full mode outputs every declaration and omits undeclared ones",
			raw:  declaredObjectTypeJSON,
			want: `[` +
				`{"name":"store_id","comment":"Store id","type":"string"},` +
				`{"name":"revenue","comment":"Revenue","type":"decimal","required":true,"unit":"元"},` +
				`{"name":"location","type":"point","semantic":"location","crs":"gcj02"},` +
				`{"name":"opened_at","type":"datetime","semantic":"event_time","required":true,"timezone":"Asia/Shanghai"},` +
				`{"name":"status","type":"string","semantic":"status"}` +
				`]`,
		},
		{
			name:  "brief mode outputs only semantic",
			raw:   declaredObjectTypeJSON,
			brief: true,
			want: `[` +
				`{"name":"store_id","type":"string"},` +
				`{"name":"revenue","type":"decimal"},` +
				`{"name":"location","type":"point","semantic":"location"},` +
				`{"name":"opened_at","type":"datetime","semantic":"event_time"},` +
				`{"name":"status","type":"string","semantic":"status"}` +
				`]`,
		},
		{
			// The expected output is what search_schema produced before declarations existed.
			name: "full mode output of a network without declarations is unchanged",
			raw:  undeclaredObjectTypeJSON,
			want: `[` +
				`{"name":"id","comment":"Order id","type":"string"},` +
				`{"name":"amount","comment":"Amount","type":"double","condition_operations":["==","in"]}` +
				`]`,
		},
		{
			name:  "brief mode output of a network without declarations is unchanged",
			raw:   undeclaredObjectTypeJSON,
			brief: true,
			want: `[` +
				`{"name":"id","type":"string"},` +
				`{"name":"amount","type":"double","condition_operations":["==","in"]}` +
				`]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := convertObjectTypeJSON(t, tt.raw, tt.brief); got != tt.want {
				t.Fatalf("data properties mismatch\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}
