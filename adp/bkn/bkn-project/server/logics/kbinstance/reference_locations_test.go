// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestReferenceStorageRespectsOriginalSwitchAndRestoresOnlyFromAuthorizedSource(t *testing.T) {
	for _, original := range []bool{false, true} {
		build, _, input, task := extractionFixture(t, "inspection", original)
		proposal, err := parseFileExtraction(task.Output)
		if err != nil {
			t.Fatal(err)
		}
		review := ReferenceReview{Reviewer: build.Owner, ReviewedAt: 10, Revision: 1, Extraction: *proposal}
		stored, err := referenceForStorage(review, input, original)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(stored)
		if !original && strings.Contains(string(raw), "Reference ABC. Total 25.50.") {
			t.Fatal("raw quote persisted with originals off")
		}
		if review.Extraction.Records[0].Anchor.Quote == "" {
			t.Fatal("input review was mutated")
		}
		if err = hydrateReference(stored, input, original); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stored.Extraction, review.Extraction) {
			t.Fatal("located reference did not reconstruct exactly")
		}
	}
}
func TestReferenceLocationCannotRestoreForeignOrUnreviewedEvidence(t *testing.T) {
	build, _, input, task := extractionFixture(t, "patent", false)
	proposal, _ := parseFileExtraction(task.Output)
	stored, err := referenceForStorage(ReferenceReview{Reviewer: build.Owner, ReviewedAt: 10, Revision: 1, Extraction: *proposal}, input, false)
	if err != nil {
		t.Fatal(err)
	}
	location := stored.Locations["record:0:anchor"]
	location.End = 100000
	stored.Locations["record:0:anchor"] = location
	if err = hydrateReference(stored, input, false); err == nil {
		t.Fatal("out-of-range annotation was restored")
	}
}
