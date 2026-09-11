// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package migrationcontract

import "testing"

func TestSealedMarkerDetectsSemanticMutation(t *testing.T) {
	marker := Marker{
		Version:           CurrentVersion,
		EETableState:      EETableAbsent,
		CoreSourceSummary: "{}",
		ActivatedGrantIDs: "[]",
	}.Seal()
	if !marker.Valid() {
		t.Fatal("freshly sealed marker is invalid")
	}
	marker.CorePolicyCount++
	if marker.Valid() {
		t.Fatal("mutated marker remained valid")
	}
}
