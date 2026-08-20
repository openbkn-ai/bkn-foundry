// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knsearch

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Instance rows are decoded with UseNumber (see drivenadapters.precisionJSON), so
// _score arrives as json.Number; without a branch for it every node scored 0 and
// the ranking collapsed.
func TestConvertToKnSearchNodeReadsJSONNumberScore(t *testing.T) {
	s := &localSearchImpl{}
	objType := &interfaces.KnSearchObjectType{ConceptID: "ot-1", ConceptName: "Shipment"}

	node := s.convertToKnSearchNode(objType, map[string]any{
		"_score": json.Number("0.87"),
		"id":     json.Number("9223372036854775808"),
	})
	if node.Score != 0.87 {
		t.Errorf("Score = %v, want 0.87", node.Score)
	}

	// Wide integers must stay untouched in the properties that ride along.
	id, ok := node.Properties["id"].(json.Number)
	if !ok {
		t.Fatalf("id type = %T, want json.Number", node.Properties["id"])
	}
	if id.String() != "9223372036854775808" {
		t.Errorf("id = %s, want 9223372036854775808", id.String())
	}

	if got := s.convertToKnSearchNode(objType, map[string]any{"_score": 0.5}); got.Score != 0.5 {
		t.Errorf("float64 Score = %v, want 0.5", got.Score)
	}
}
