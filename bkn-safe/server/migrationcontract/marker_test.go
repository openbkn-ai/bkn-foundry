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

// Keep this fixture in sync with openbkn-deploy's migration writer test.
// A fixed digest catches unintentional changes to the receipt's wire contract.
func TestMarkerContractGolden(t *testing.T) {
	if CurrentVersion != "authz-edition-boundaries-v1" {
		t.Fatalf("receipt version changed: %q", CurrentVersion)
	}
	marker := Marker{
		Version:               CurrentVersion,
		EETableState:          EETablePresentWithRows,
		CorePolicyCount:       12,
		CoreGrantCount:        34,
		CoreSourceSummary:     `{"policies":12,"grants":34}`,
		EERowCount:            56,
		EEPublishedCount:      7,
		EEDormantCount:        8,
		EEInvalidCount:        9,
		EEActiveCount:         10,
		EEDenyCount:           11,
		EEInventoryDigest:     "ee-inventory-digest",
		EEAssemblyEvidenceRef: "assembly:2026-10-12",
		ActivatedGrantIDs:     `["grant-a","grant-b"]`,
		ActivationConfirmedBy: "owner-a",
		ActivationEvidenceRef: "approval:42",
	}
	const checksum = "987016e93c27b9964c4c8c0db591147389284d430c0da048f08f1ee5936fa6e7"
	if got := marker.ExpectedChecksum(); got != checksum {
		t.Fatalf("receipt checksum changed: got %s, want %s", got, checksum)
	}
	if got := marker.Seal(); got.Checksum != checksum || !got.Valid() {
		t.Fatalf("golden receipt did not seal and validate: %+v", got)
	}
}
