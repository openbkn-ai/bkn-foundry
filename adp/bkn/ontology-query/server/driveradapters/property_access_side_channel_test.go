// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"encoding/json"
	"strings"
	"testing"

	"ontology-query/interfaces"
)

func TestEvidenceQueryShapeDoesNotContainRawPaginationValues(t *testing.T) {
	const watermark = "raw-property-watermark-1342"
	shape := safeObjectQueryShape(&interfaces.ObjectQueryBaseOnObjectType{
		PageQuery: interfaces.PageQuery{
			Cursor:            "opaque-cursor",
			SearchAfterParams: interfaces.SearchAfterParams{SearchAfter: []any{watermark}},
		},
	})
	body, err := json.Marshal(shape)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), watermark) {
		t.Fatalf("raw page state leaked into evidence shape: %s", body)
	}
}
