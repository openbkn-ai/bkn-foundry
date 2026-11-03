// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"encoding/json"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestConfirmFilePeriodRequiresExistingDateFieldsAndAnExplicitHumanDecision(t *testing.T) {
	for _, scenario := range []string{"confirm", "until_only", "clear", "same_field", "missing", "text", "managed", "unconfirmed", "wrong_role", "app"} {
		t.Run(scenario, func(t *testing.T) {
			object := &interfaces.DraftObjectType{Document: &interfaces.DraftDocumentModel{Role: "file"}, Properties: []*interfaces.DraftProperty{{Name: "starts", Type: "date"}, {Name: "ends", Type: "datetime"}, {Name: "title", Type: "string"}, {Name: "recorded_at", Type: "datetime", ReadOnly: true}}}
			caller := interfaces.AccountInfo{ID: "reviewer", Type: "user"}
			decision := interfaces.FilePeriodDecision{From: "starts", Until: "ends", Confirm: true}
			switch scenario {
			case "until_only":
				decision.From = ""
			case "clear":
				decision = interfaces.FilePeriodDecision{Clear: true}
			case "same_field":
				decision.Until = decision.From
			case "missing":
				decision.From = "guessed_effective_date"
			case "text":
				decision.From = "title"
			case "managed":
				decision.From = "recorded_at"
			case "unconfirmed":
				decision.Confirm = false
			case "wrong_role":
				object.Document.Role = "entity"
			case "app":
				caller.Type = "app"
			}
			err := ConfirmFilePeriod(object, decision, caller, 10)
			valid := scenario == "confirm" || scenario == "until_only" || scenario == "clear"
			if (err == nil) != valid {
				t.Fatal("incorrect period confirmation", scenario, err)
			}
			if !valid && object.Document.FilePeriod != nil {
				t.Fatal("invalid mapping became authoritative")
			}
			if scenario == "confirm" || scenario == "until_only" {
				proof := object.Document.FilePeriod
				if proof == nil || proof.ConfirmedBy != caller || proof.ConfirmedAt != 10 || proof.From != decision.From || proof.Until != decision.Until {
					t.Fatal("human decision was not retained", proof)
				}
				object.Properties[1].Type = "string"
				if ValidFilePeriod(object, proof) {
					t.Fatal("changed field type reused a stale date mapping")
				}
			}
		})
	}
}

func TestFilePeriodAuthoritySurvivesOnlyUnchangedDateFields(t *testing.T) {
	for _, scenario := range []string{"unchanged", "renamed", "changed_type", "forged_proof"} {
		t.Run(scenario, func(t *testing.T) {
			input := modelInput("Contract C-1 starts on 2026-01-01.")
			proposal := proposedObject(input, "contract_file", "file")
			proposal.Properties = append(proposal.Properties, &interfaces.DraftProperty{Name: "starts", DisplayName: "Effective date", Type: "date", Extraction: &interfaces.DraftExtractionField{Instruction: "Extract the explicitly stated start date", Examples: []interfaces.DraftSourceCitation{documentQuote(input)}}})
			validated, err := ValidateModel(input, interfaces.DraftItems{}, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{proposal}})
			if err != nil {
				t.Fatal(err)
			}
			original := validated.ObjectTypes[0]
			if err := ConfirmFilePeriod(original, interfaces.FilePeriodDecision{From: "starts", Confirm: true}, interfaces.AccountInfo{ID: "reviewer", Type: "user"}, 10); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(original)
			var edited interfaces.DraftObjectType
			if json.Unmarshal(raw, &edited) != nil {
				t.Fatal("fixture")
			}
			for _, field := range edited.Properties {
				if field.Name != "starts" {
					continue
				}
				if scenario == "renamed" {
					field.Name = "signed_date"
				}
				if scenario == "changed_type" {
					field.Type = "datetime"
				}
			}
			if scenario == "forged_proof" {
				edited.Document.FilePeriod.ConfirmedBy.ID = "forged"
			}
			result, err := ValidateEditedObject(input, interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{original}}, original, &edited)
			if scenario == "forged_proof" {
				if err == nil {
					t.Fatal("client forged a server-owned confirmation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (result.Document.FilePeriod != nil) != (scenario == "unchanged") {
				t.Fatal("stale date mapping survived the edit", scenario)
			}
		})
	}
}
