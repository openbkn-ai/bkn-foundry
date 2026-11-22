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

func TestResolveActionConditionValuesRejectsEmptySearchValues(t *testing.T) {
	for _, operation := range []string{"like", "not_like", "prefix", "not_prefix", "regex"} {
		for _, source := range []string{"", "const", "property", "input"} {
			t.Run(operation+"/"+source, func(t *testing.T) {
				definition := &cond.CondCfg{Name: "name", Operation: operation, ValueFrom: source, Value: ""}
				if source == "property" || source == "input" {
					definition.Value = "pattern"
				}
				for _, logical := range []string{"", "and", "or"} {
					t.Run(logical, func(t *testing.T) {
						condition := definition
						if logical != "" {
							condition = &cond.CondCfg{Operation: logical, SubConds: []*cond.CondCfg{
								{Name: "name", Operation: "==", Value: "alpha"}, definition,
							}}
						}
						resolved, err := ResolveActionConditionValues(condition, map[string]any{"pattern": ""}, map[string]any{"pattern": ""})
						if err == nil || resolved != nil || !strings.Contains(err.Error(), operation) || !strings.Contains(err.Error(), "non-empty string") {
							t.Fatalf("expected invalid empty %s condition, resolved=%#v err=%v", operation, resolved, err)
						}
						if source == "property" || source == "input" {
							if !strings.Contains(err.Error(), source) || !strings.Contains(err.Error(), "pattern") {
								t.Fatalf("missing reference context: %v", err)
							}
							if definition.Value != "pattern" || definition.ValueFrom != source {
								t.Fatalf("stored reference was mutated: %#v", definition)
							}
						}
					})
				}
			})
		}
	}
}

func TestResolveActionConditionValuesPreservesAllowedEmptyAndSearchValues(t *testing.T) {
	for _, operation := range []string{"like", "not_like", "prefix", "not_prefix", "regex", "==", "!=", "in", "not_in"} {
		values := []any{" ", "%", ".*"}
		switch operation {
		case "==", "!=":
			values = []any{""}
		case "in", "not_in":
			values = []any{[]any{""}}
		}
		for _, source := range []string{"", "const", "property", "input"} {
			t.Run(operation+"/"+source, func(t *testing.T) {
				for _, value := range values {
					definition := &cond.CondCfg{Name: "name", Operation: operation, ValueFrom: source, Value: value}
					if source == "property" || source == "input" {
						definition.Value = "pattern"
					}
					resolved, err := ResolveActionConditionValues(definition, map[string]any{"pattern": value}, map[string]any{"pattern": value})
					if err != nil || resolved == nil || !reflect.DeepEqual(resolved.Value, value) {
						t.Fatalf("value %#v must be preserved, resolved=%#v err=%v", value, resolved, err)
					}
				}
			})
		}
	}
}
