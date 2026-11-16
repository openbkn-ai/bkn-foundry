// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestActionOperationRequiresNewDefinition(t *testing.T) {
	at := &ActionType{}
	if err := ValidateActionOperation(at); err == nil {
		t.Fatal("missing operation config must not select a legacy execution mode")
	}
	at.OperationConfig = &ActionOperationConfig{Type: ActionOperationTool}
	if err := ValidateActionOperation(at); err != nil {
		t.Fatal(err)
	}
	at.ObjectTypeID = "orders"
	if err := ValidateActionOperation(at); err == nil {
		t.Fatal("tool actions must not bind object context")
	}
}

func TestActionOperationRejectsObsoleteMappingFields(t *testing.T) {
	var config ActionOperationConfig
	if err := json.Unmarshal([]byte(`{"type":"relation","source_parameter":"order_id"}`), &config); err == nil {
		t.Fatal("obsolete mappings must not be silently ignored")
	}
}

func TestActionRelationRequiresBothIdentities(t *testing.T) {
	at := &ActionType{ActionTypeWithKeyField: ActionTypeWithKeyField{
		ObjectTypeID:    "orders",
		OperationConfig: &ActionOperationConfig{Type: ActionOperationRelation, RelationTypeID: "supplier", RelationOperation: ActionRelationCreate},
		Parameters:      []Parameter{{Name: "order_id", ValueFrom: ActionValueSourceIdentity}},
	}}
	if err := ValidateActionOperation(at); err == nil {
		t.Fatal("one endpoint is insufficient")
	}
	at.Parameters = append(at.Parameters, Parameter{Name: "supplier_id", ValueFrom: ActionValueTargetIdentity})
	if err := ValidateActionOperation(at); err != nil {
		t.Fatal(err)
	}
	at.Parameters[0].Value = "order_id"
	if err := ValidateActionOperation(at); err == nil {
		t.Fatal("endpoint primary key must be derived, not manually mapped")
	}
}

func TestActionConditionReferenceNames(t *testing.T) {
	at := &ActionType{ActionTypeWithKeyField: ActionTypeWithKeyField{
		ObjectTypeID: "orders", OperationConfig: &ActionOperationConfig{Type: ActionOperationObject},
		Parameters: []Parameter{{Name: "window", ValueFrom: "input", Type: "array"}},
		Condition:  &ActionCondCfg{Field: "date", Operation: "range", ValueFrom: "input", Value: "window"},
	}}
	if err := ValidateActionOperation(at); err != nil {
		t.Fatal(err)
	}
	at.Condition.Value = "unknown"
	if err := ValidateActionOperation(at); err == nil {
		t.Fatal("unknown runtime parameter must fail at save")
	}
	at.Condition.ValueFrom = "property"
	at.Condition.Value = "start_date"
	at.Condition.ObjectTypeID = "other"
	if err := ValidateActionOperation(at); err == nil {
		t.Fatal("cross-object property references must fail")
	}
}

func TestObjectCreationRejectsApplicabilityCondition(t *testing.T) {
	at := &ActionType{ActionTypeWithKeyField: ActionTypeWithKeyField{
		ObjectTypeID: "orders", ActionType: ACTION_TYPE_ADD,
		OperationConfig: &ActionOperationConfig{Type: ActionOperationObject},
		Condition:       &ActionCondCfg{Field: "id", Operation: "!=", Value: "1"},
	}}
	if err := ValidateActionOperation(at); err == nil {
		t.Fatal("object creation must not filter an existing instance")
	}
	at.Condition = nil
	if err := ValidateActionOperation(at); err != nil {
		t.Fatal(err)
	}
}

func TestActionJSONPreservesTypedNumbers(t *testing.T) {
	var action ActionType
	raw := []byte(`{"id":"notify","operation_config":{"type":"tool"},"parameters":[{"name":"id","type":"integer","value_from":"const","value":9223372036854775807}]}`)
	if err := json.Unmarshal(raw, &action); err != nil {
		t.Fatal(err)
	}
	if value, ok := action.Parameters[0].Value.(json.Number); !ok || value.String() != "9223372036854775807" {
		t.Fatalf("value=%#v", action.Parameters[0].Value)
	}
}

func TestActionOperationValidatesConstantTypes(t *testing.T) {
	for _, tc := range []struct {
		name, declared string
		value          any
		valid          bool
	}{
		{"string", "string", "123", true},
		{"string number", "string", json.Number("123"), false},
		{"string null", "string", nil, false},
		{"boolean", "boolean", false, true},
		{"boolean null", "boolean", nil, true},
		{"boolean number", "boolean", json.Number("1"), false},
		{"integer", "integer", json.Number("9223372036854775807"), true},
		{"integer fraction", "integer", json.Number("1.5"), false},
		{"number", "number", json.Number("1.5"), true},
		{"number string", "number", "1.5", false},
		{"array", "array", []any{1}, true},
		{"array object", "array", map[string]any{}, false},
		{"object", "object", map[string]any{"a": 1}, true},
		{"object array", "object", []any{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			action := &ActionType{ActionTypeWithKeyField: ActionTypeWithKeyField{
				OperationConfig: &ActionOperationConfig{Type: ActionOperationTool},
				Parameters:      []Parameter{{Name: "payload", Type: tc.declared, ValueFrom: "const", Value: tc.value}},
			}}
			err := ValidateActionOperation(action)
			if (err == nil) != tc.valid {
				t.Fatalf("value %T for %s: %v", tc.value, tc.declared, err)
			}
			if !tc.valid && !strings.Contains(err.Error(), `parameter "payload"`) {
				t.Fatalf("parameter name missing: %v", err)
			}
		})
	}
}

func TestNormalizeActionStringConstantsBeforeSave(t *testing.T) {
	action := &ActionType{ActionTypeWithKeyField: ActionTypeWithKeyField{
		OperationConfig: &ActionOperationConfig{Type: ActionOperationTool},
		Parameters: []Parameter{
			{Name: "code", Type: "string", ValueFrom: "const", Value: json.Number("123")},
			{Name: "enabled", Type: "text", ValueFrom: "const", Value: true},
			{Name: "missing", Type: "keyword", ValueFrom: "const", Value: nil},
			{Name: "count", Type: "integer", ValueFrom: "const", Value: json.Number("42")},
		},
	}}
	if err := NormalizeActionStringConstants(action); err != nil {
		t.Fatal(err)
	}
	if err := ValidateActionOperation(action); err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"123", "true", "null"} {
		if action.Parameters[index].Value != want {
			t.Errorf("parameter %d = %v, want %q", index, action.Parameters[index].Value, want)
		}
	}
	if action.Parameters[3].Value != json.Number("42") {
		t.Fatalf("numeric constant changed: %v", action.Parameters[3].Value)
	}
}
