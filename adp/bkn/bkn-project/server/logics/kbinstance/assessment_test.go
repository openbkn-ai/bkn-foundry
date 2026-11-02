// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestHumanReferencesUseTheSameGroundingWithoutInventingAnAgentExecution(t *testing.T) {
	build, draft, input, task := extractionFixture(t, "contract", true)
	var proposal FileExtraction
	if json.Unmarshal([]byte(task.Output), &proposal) != nil {
		t.Fatal("fixture")
	}
	build.Files[0].AgentTaskID = ""
	review := ReferenceReview{Reviewer: build.Owner, ReviewedAt: 1234, Revision: 1, Extraction: proposal}
	rows, err := MaterializeReference(build, 0, draft, input, review)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		var provenance map[string]any
		if json.Unmarshal([]byte(row.Row["extraction_version"].(string)), &provenance) != nil || provenance["reviewed_reference"] != float64(1) || provenance["agent"] != nil {
			t.Fatal("fabricated extraction execution")
		}
	}
	review.Reviewer = interfaces.AccountInfo{}
	if _, err = MaterializeReference(build, 0, draft, input, review); err == nil {
		t.Fatal("unconfirmed reference accepted")
	}
	review.Reviewer = build.Owner
	review.Extraction.Records[0].Values["amount"] = InstanceValue{Value: json.Number("5"), Evidence: []InstanceCitation{{BlockID: "native-1", Quote: "invented quote"}}}
	if _, err = MaterializeReference(build, 0, draft, input, review); err == nil {
		t.Fatal("human reference bypassed source grounding")
	}
}
func TestAcceptanceUsesVerifiedCountsAndNeverAwardsUnknownDenominators(t *testing.T) {
	report, err := scoreAtoms([]assessmentAtom{{Kind: "property", Key: "a", Value: "1"}, {Kind: "property", Key: "b", Value: "2"}, {Kind: "link", Key: "r", Value: "target"}}, []assessmentAtom{{Kind: "property", Key: "a", Value: "1"}, {Kind: "property", Key: "b", Value: "wrong"}, {Kind: "link", Key: "r", Value: "target"}})
	if err != nil || report.Precision == nil || *report.Precision != 0.5 || report.Recall == nil || *report.Recall != 0.5 || report.Linking == nil || *report.Linking != 1 || report.Passed {
		t.Fatal("wrong measured counts", report, err)
	}
	empty, err := scoreAtoms(nil, nil)
	if err != nil || empty.Precision != nil || empty.Recall != nil || empty.Linking != nil || empty.Passed {
		t.Fatal("empty denominator treated as perfect", empty, err)
	}
}
func TestAcceptanceDuplicateEvidenceDoesNotInflateScores(t *testing.T) {
	atom := assessmentAtom{Kind: "property", Key: "a", Value: "1"}
	report, err := scoreAtoms([]assessmentAtom{atom, atom}, []assessmentAtom{atom, atom, atom})
	if err != nil || report.Expected != 1 || report.Predicted != 1 || report.Correct != 1 || report.Passed {
		t.Fatal("duplicate claims inflated counts or unmeasured links passed", report, err)
	}
}

func TestAcceptanceGateRejectsCorruptCountsEvenWhenStoredAsPassed(t *testing.T) {
	report := AcceptanceCounts{Expected: 1, Predicted: 1, Correct: 2, ExpectedLinks: 1, PredictedLinks: 1, CorrectLinks: 1, Passed: true}
	calculateAcceptance(&report)
	if report.Passed || report.Precision != nil || report.Recall != nil || report.Linking != nil {
		t.Fatal("invalid stored counts passed the full-build gate")
	}
}
