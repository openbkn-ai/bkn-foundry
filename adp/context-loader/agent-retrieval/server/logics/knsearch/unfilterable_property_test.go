// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knsearch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// #3365: over MCP search_schema lists only index operators and leaves comparisons
// to the property type, so a JSON field that its data source returns but cannot
// filter on read as filterable. bkn-backend reports the switch on the object type
// detail; the schema has to carry it where it is off.
func TestBackfillCarriesAnUnfilterableProperty(t *testing.T) {
	on, off := true, false
	backend := &mockBknBackend{objectDetailResp: []*interfaces.ObjectType{
		{ID: "claims", DataProperties: []*interfaces.DataProperty{
			{Name: "claim_code", Filterable: &on, ConditionOperations: []interfaces.KnOperationType{
				interfaces.KnOperationTypeEqual, interfaces.KnOperationTypeMatch,
			}},
			{Name: "claim_value", Filterable: &off},
			{Name: "legacy"},
		}},
		// Nothing here can be searched; the switch still has to arrive.
		{ID: "notes", DataProperties: []*interfaces.DataProperty{{Name: "payload", Filterable: &off}}},
	}}
	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	claims := &interfaces.KnSearchObjectType{ConceptID: "claims", DataProperties: []*interfaces.KnSearchDataProperty{
		{Name: "claim_code", Type: "string"}, {Name: "claim_value", Type: "json"}, {Name: "legacy", Type: "string"},
	}}
	notes := &interfaces.KnSearchObjectType{ConceptID: "notes", DataProperties: []*interfaces.KnSearchDataProperty{
		{Name: "payload", Type: "json"},
	}}

	svc.backfillConditionOperations(context.Background(), "kn1", []*interfaces.KnSearchObjectType{claims, notes}, false)
	trimToIndexBackedOperations([]*interfaces.KnSearchObjectType{claims, notes}, true)

	for _, p := range []*interfaces.KnSearchDataProperty{claims.DataProperties[1], notes.DataProperties[0]} {
		if p.Filterable == nil || *p.Filterable {
			t.Errorf("%s: filterable = %v, want false", p.Name, p.Filterable)
		}
	}
	for _, p := range []*interfaces.KnSearchDataProperty{claims.DataProperties[0], claims.DataProperties[2]} {
		if p.Filterable != nil {
			t.Errorf("%s: filterable = %v, want it absent", p.Name, *p.Filterable)
		}
	}

	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), `"filterable"`) != 1 || !strings.Contains(string(body), `"filterable":false`) {
		t.Errorf("schema = %s, want filterable:false on claim_value only", body)
	}
}

// An object type the concept recall already read in detail skips the backfill;
// its properties carry the switch from that detail instead.
func TestConvertKeepsOnlyAnUnfilterableSwitch(t *testing.T) {
	on, off := true, false
	obj := &interfaces.ObjectType{ID: "claims", DataProperties: []*interfaces.DataProperty{
		{Name: "claim_code", Type: "string", Filterable: &on},
		{Name: "claim_value", Type: "json", Filterable: &off},
		{Name: "legacy", Type: "string"},
	}}
	for _, brief := range []bool{false, true} {
		local := (&localSearchImpl{}).convertObjectTypesToLocal([]*interfaces.ObjectType{obj}, brief, false)
		props := local[0].DataProperties
		if props[0].Filterable != nil || props[2].Filterable != nil {
			t.Errorf("brief=%v: a filterable property carries the switch", brief)
		}
		if props[1].Filterable == nil || *props[1].Filterable {
			t.Errorf("brief=%v: claim_value filterable = %v, want false", brief, props[1].Filterable)
		}
	}
}
