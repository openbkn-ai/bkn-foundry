// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbpublication

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
)

func frozenFixture(t *testing.T) *interfaces.Draft {
	t.Helper()
	doc := &interfaces.DraftDocumentModel{Role: "entity", Layer: "entity", Reason: "private source reasoning", SelectionID: "private-selection", SelectionRevision: 1, Citations: []interfaces.DraftSourceCitation{{KnowledgeBaseID: "private-kb", FileID: "private-file", ContentVersionID: "private-version", BlockID: "private-block", Start: 0, End: 18, Quote: "private source text"}}}
	draft := &interfaces.Draft{ProjectID: "p", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: 12, Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "asset_doc", Name: "Asset", Adopted: true, Document: doc, PrimaryKeys: []string{"number"}, Properties: []*interfaces.DraftProperty{{Name: "_id", Type: "string", ReadOnly: true, SourceOfRecord: "document"}, {Name: "number", Type: "string", SourceOfRecord: "document", Extraction: &interfaces.DraftExtractionField{Instruction: "Read the explicit asset number", Examples: doc.Citations}}}}}}}
	plan, err := kbdataset.NewPlan(draft, "generation", "catalog", interfaces.AccountInfo{ID: "private-reviewer", Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	plan.State = "ready"
	for i := range plan.Datasets {
		plan.Datasets[i].Ready = true
	}
	draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
	draft.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: 12, Commit: "confirmed-definition"}
	return draft
}
func TestFrozenRulesSeparateReviewableDefinitionsFromPrivateRuntimeEvidence(t *testing.T) {
	draft := frozenFixture(t)
	original, _ := json.Marshal(draft)
	bundle, err := Freeze(draft, draft.Items.DatasetPlans[0])
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(draft)
	if !bytes.Equal(original, after) {
		t.Fatal("freezing mutated source model")
	}
	for _, secret := range []string{"private source text", "private source reasoning", "private-selection", "private-file", "private-reviewer", "citations", "examples", "confirmed_by", "decisions"} {
		if bytes.Contains(bundle.Definition, []byte(secret)) {
			t.Fatalf("private runtime material leaked to Git definition: %s", secret)
		}
	}
	if !bytes.Contains(bundle.Definition, []byte("Read the explicit asset number")) {
		t.Fatal("extraction rule absent")
	}
	if !bytes.Contains(bundle.Runtime, []byte("private source text")) {
		t.Fatal("runtime evidence lost from Dataset snapshot")
	}
	restored, err := Restore(bundle.Runtime, bundle.RuntimeDigest, bundle.ModelFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	draft.Items.ObjectTypes[0].Properties[1].Extraction.Instruction = "later mutable project edit"
	if restored.Items.ObjectTypes[0].Properties[1].Extraction.Instruction != "Read the explicit asset number" {
		t.Fatal("runtime depends on mutable project")
	}
	selection := &interfaces.KBSelection{ID: "private-selection", ProjectID: "p", Owner: draft.Items.DatasetPlans[0].Owner, State: "active", Revision: 1, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "kb", FileID: "file", ContentVersionID: "version"}}}
	if _, err := kbinstance.PlanFileBuild("refresh-job", "lineage", "full", 3, restored, selection); err != nil {
		t.Fatal("fixed model cannot plan independently of the project draft", err)
	}

	// Restoring an immutable snapshot reproduces the original execution fingerprint.
	fp, err := kbdataset.ModelFingerprint(restored)
	if err != nil || fp != bundle.ModelFingerprint {
		t.Fatal("runtime fingerprint changed", err)
	}

}
func TestFrozenRuntimeRejectsTamperingWrongModelAndUnreadyBindings(t *testing.T) {
	draft := frozenFixture(t)
	bundle, err := Freeze(draft, draft.Items.DatasetPlans[0])
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.ReplaceAll(bundle.Runtime, []byte("Read the explicit asset number"), []byte("Guess an asset number"))
	if _, err := Restore(tampered, bundle.RuntimeDigest, bundle.ModelFingerprint); err == nil {
		t.Fatal("tampered runtime accepted")
	}
	if _, err := Restore(bundle.Runtime, bundle.RuntimeDigest, "wrong-model"); err == nil {
		t.Fatal("wrong execution fingerprint accepted")
	}
	draft.Items.DatasetPlans[0].Datasets[0].Ready = false
	if _, err := Freeze(draft, draft.Items.DatasetPlans[0]); err == nil {
		t.Fatal("unready Dataset frozen")
	}
}
