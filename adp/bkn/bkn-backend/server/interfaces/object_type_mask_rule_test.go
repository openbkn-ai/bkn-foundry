// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"testing"

	"bkn-backend/common/maskrule"
)

func TestDataPropertyMaskRuleJSONRoundTrip(t *testing.T) {
	keepStart, keepEnd := 3, 4
	original := DataProperty{
		Name: "mobile", DisplayName: "Mobile", Type: "string",
		MaskRule: &maskrule.Rule{Kind: maskrule.KindPartial, KeepStart: &keepStart, KeepEnd: &keepEnd, Replacement: "*"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded DataProperty
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded.MaskRule == nil || decoded.MaskRule.Kind != maskrule.KindPartial || *decoded.MaskRule.KeepEnd != 4 {
		t.Fatalf("decoded mask_rule = %#v", decoded.MaskRule)
	}

	legacy, err := json.Marshal(DataProperty{Name: "legacy", Type: "string"})
	if err != nil {
		t.Fatalf("json.Marshal() legacy error = %v", err)
	}
	if string(legacy) == "" || containsJSONField(legacy, "mask_rule") {
		t.Fatalf("legacy JSON unexpectedly contains mask_rule: %s", legacy)
	}
}

func containsJSONField(data []byte, field string) bool {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(data, &value); err != nil {
		return false
	}
	_, ok := value[field]
	return ok
}
