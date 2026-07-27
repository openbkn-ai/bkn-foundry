// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"strings"
	"testing"
)

// Regression for openbkn-ai/bkn-studio#219: ActionType trigger condition must
// serialize/deserialize as "condition" (OpenAPI / Studio), not "cond".
func TestActionTypeConditionJSONFieldName(t *testing.T) {
	at := ActionTypeWithKeyField{
		ATID:         "at1",
		ATName:       "demo",
		ActionType:   ACTION_TYPE_MODIFY,
		ObjectTypeID: "ot1",
		Condition: &ActionCondCfg{
			Field:     "status",
			Operation: "eq",
		},
	}

	raw, err := json.Marshal(at)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if strings.Contains(s, `"cond"`) {
		t.Fatalf("marshaled JSON still uses legacy key cond: %s", s)
	}
	if !strings.Contains(s, `"condition"`) {
		t.Fatalf("marshaled JSON missing condition key: %s", s)
	}

	payload := `{"id":"at1","name":"demo","action_type":"modify","object_type_id":"ot1","condition":{"field":"status","operation":"eq","value":"open","value_from":"const"}}`
	var decoded ActionTypeWithKeyField
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("unmarshal condition: %v", err)
	}
	if decoded.Condition == nil {
		t.Fatal("expected Condition to be bound from condition key")
	}
	if decoded.Condition.Field != "status" || decoded.Condition.Operation != "eq" {
		t.Fatalf("unexpected condition: %+v", decoded.Condition)
	}
}
