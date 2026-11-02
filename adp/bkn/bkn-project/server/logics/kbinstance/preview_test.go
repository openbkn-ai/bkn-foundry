// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPreviewScopeExcludesHeldoutFilesAndRequiresExactStoredModel(t *testing.T) {
	build, draft, _, _ := extractionFixture(t, "inspection", false)
	build.Files[0].State = "succeeded"
	heldout := build.Files[0]
	heldout.File.FileID = "20"
	heldout.File.ContentVersionID = "200"
	heldout.File.SampleRole = "acceptance"
	build.Files = append(build.Files, heldout)
	spec := draft.Items.DatasetPlans[0].Datasets[0]
	where, scope, err := previewScope(spec, build, "")
	if err != nil || len(scope) != 1 || !strings.Contains(where, build.ModelFingerprint) || strings.Contains(where, "'200'") {
		t.Fatal("heldout scope leaked", err)
	}
	if _, _, err = previewScope(spec, build, heldout.File.Key()); !errors.Is(err, ErrAcceptanceHidden) {
		t.Fatal("explicit heldout selection was permitted", err)
	}
	spec.Layout.Mapping["document_id"] = "unsafe`field"
	if _, _, err = previewScope(spec, build, ""); !errors.Is(err, ErrInvalidBuild) {
		t.Fatal("untrusted physical field accepted", err)
	}
}

func TestDisabledOriginalRedactsNestedEvidenceWithoutMutatingQueryResults(t *testing.T) {
	original := map[string]any{"source_quote": "private quote", "nested": []any{map[string]any{"quote": "private nested", "block_id": "native-1", "source_start": 2}}}
	raw, err := json.Marshal(redactEvidenceQuotes(original))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private") || !strings.Contains(string(raw), "native-1") {
		t.Fatal("quote leak or lost source locator", string(raw))
	}
	if original["source_quote"] != "private quote" {
		t.Fatal("mutated query result")
	}
}

func TestPreviewScopeDoesNotExposeIncompleteOrNativeOnlyBusinessRows(t *testing.T) {
	build, draft, _, _ := extractionFixture(t, "patent", true)
	for _, state := range []string{"waiting", "writing", "failed", "evidence_only"} {
		build.Files[0].State = state
		where, scope, err := previewScope(draft.Items.DatasetPlans[0].Datasets[0], build, "")
		if err != nil || where != "" || len(scope) != 0 {
			t.Fatal("partial file became visible", state, err)
		}
	}
}
