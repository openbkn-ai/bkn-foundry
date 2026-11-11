// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"reflect"
	"strings"
	"testing"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
)

func TestResolveActionConditionValues(t *testing.T) {
	definition := &cond.CondCfg{Operation: "and", SubConds: []*cond.CondCfg{
		{Name: "delivered", Operation: "<", ValueFrom: "property", Value: "ordered"},
		{Name: "date", Operation: "range", ValueFrom: "input", Value: "window"},
	}}
	resolved, err := ResolveActionConditionValues(definition, map[string]any{"ordered": 10}, map[string]any{"window": []any{1, 2}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(10, resolved.SubConds[0].Value) {
		t.Fatalf("expected %v, got %v", 10, resolved.SubConds[0].Value)
	}
	if !reflect.DeepEqual([]any{1, 2}, resolved.SubConds[1].Value) {
		t.Fatalf("expected %v, got %v", []any{1, 2}, resolved.SubConds[1].Value)
	}
	if !reflect.DeepEqual("ordered", definition.SubConds[0].Value) {
		t.Fatalf("expected %v, got %v", "ordered", definition.SubConds[0].Value)
	}
	_, err = ResolveActionConditionValues(definition, map[string]any{}, map[string]any{"window": []any{1, 2}})
	if err == nil || !strings.Contains(err.Error(), "ordered") {
		t.Fatalf("expected error containing %s, got %v", "ordered", err)
	}
	_, err = ResolveActionConditionValues(definition, map[string]any{"ordered": 10}, map[string]any{"window": nil})
	if err == nil || !strings.Contains(err.Error(), "window") {
		t.Fatalf("expected error containing %s, got %v", "window", err)
	}
	_, err = ResolveActionConditionValues(&cond.CondCfg{Name: "id", Operation: "exist", ValueFrom: "input", Value: "id"}, nil, map[string]any{"id": 1})
	if err == nil {
		t.Fatal("expected an error")
	}
}
