package interfaces

import (
	"encoding/json"
	"testing"
)

func TestDataPropertyRejectsLegacyIndexConfigWithoutReexposingIt(t *testing.T) {
	var property DataProperty
	if err := json.Unmarshal([]byte(`{"name":"title","display_name":"Title","type":"string","index_config":{"keyword_config":{"enabled":true}}}`), &property); err != nil {
		t.Fatalf("unmarshal data property: %v", err)
	}
	if !property.HasRetiredIndexConfig() {
		t.Fatal("legacy index_config was not recorded")
	}
	encoded, err := json.Marshal(property)
	if err != nil {
		t.Fatalf("marshal data property: %v", err)
	}
	if string(encoded) == "" || containsIndexConfig(encoded) {
		t.Fatalf("legacy index_config leaked into response: %s", encoded)
	}
}

func containsIndexConfig(value []byte) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(value, &raw); err != nil {
		return true
	}
	_, exists := raw["index_config"]
	return exists
}
