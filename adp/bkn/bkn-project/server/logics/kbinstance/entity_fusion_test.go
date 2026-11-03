// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

func entityFusionFixture(t *testing.T, domain string) (*interfaces.KBBuild, *interfaces.Draft, []SourceClaim) {
	t.Helper()
	build, draft, input, task := extractionFixture(t, domain, false)
	writes, err := MaterializeFile(build, 0, draft, input, task)
	if err != nil {
		t.Fatal(err)
	}
	build.ModelID, build.Model = task.ModelID, task.Model
	step := &build.Files[0]
	step.State, step.AgentVersion, step.PromptVersion, step.ModelID = "succeeded", task.AgentVersion, task.PromptVersion, task.ModelID
	reader := &claimSnapshot{rows: map[string][]map[string]any{}}
	for _, write := range writes {
		reader.rows[write.Spec.ResourceID] = append(reader.rows[write.Spec.ResourceID], write.Row)
	}
	claims, err := ReadSourceClaims(context.Background(), reader, build, draft, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return build, draft, claims
}
func TestEntityFusionWritesNativeCurrentRowsWithPropertyLevelSourceReceipts(t *testing.T) {
	for _, domain := range []string{"contract", "patent", "maintenance"} {
		t.Run(domain, func(t *testing.T) {
			build, draft, claims := entityFusionFixture(t, domain)
			plan, err := PlanEntityFusion(build, draft, claims, nil, 2000)
			if err != nil || len(plan.Writes) != 1 || len(plan.Summaries) != 1 {
				t.Fatal("no native entity row", plan, err)
			}
			row := plan.Writes[0].Row
			if row["number"] != "ABC" || row["amount"] != json.Number("25.50") || row["document_id"] != "" || row["source_quote"] != "" {
				t.Fatal("current value, privacy or multi-source scope lost", row)
			}
			var receipt EntityFusionReceipt
			if json.Unmarshal([]byte(row["extraction_version"].(string)), &receipt) != nil || len(receipt.Sources["amount"]) != 1 || len(receipt.Pending) != 0 {
				t.Fatal("current value has no independently grounded source")
			}
			repeated, err := PlanEntityFusion(build, draft, claims, map[string][]map[string]any{plan.Writes[0].Spec.ResourceID: {row}}, 2001)
			if err != nil || !reflect.DeepEqual(plan, repeated) {
				t.Fatal("unchanged inputs were not stable", err)
			}
		})
	}
}
func TestEntityFusionKeepsOnlyStillEvidencedPriorValuesWhenNewClaimsConflict(t *testing.T) {
	build, draft, claims := entityFusionFixture(t, "contract")
	initial, err := PlanEntityFusion(build, draft, claims, nil, 2000)
	if err != nil {
		t.Fatal(err)
	}
	previous := map[string][]map[string]any{initial.Writes[0].Spec.ResourceID: {initial.Writes[0].Row}}
	var extra SourceClaim
	for _, claim := range claims {
		if claim.ObjectTypeID == "contract_doc" && claim.Property == "amount" {
			extra = claim
		}
	}
	extra.ID = "another-claim"
	extra.DocumentID = "another-document"
	extra.ContentVersionID = "another-version"
	extra.Value = json.Number("30")
	claims = append(claims, extra)
	plan, err := PlanEntityFusion(build, draft, claims, previous, 2000)
	if err != nil || plan.Writes[0].Row["amount"] != json.Number("25.50") || plan.Summaries[0].Pending != 1 {
		t.Fatal("ambiguous proposal overwrote known current value", plan, err)
	}
	// Replaced/removed source claims cannot keep a value alive through an old receipt.
	remaining := []SourceClaim{}
	for _, claim := range claims {
		if claim.ObjectTypeID == "contract_doc" && claim.Property == "amount" && claim.ID != extra.ID {
			continue
		}
		remaining = append(remaining, claim)
	}
	plan, err = PlanEntityFusion(build, draft, remaining, previous, 2000)
	if err != nil || plan.Writes[0].Row["amount"] != json.Number("30") {
		t.Fatal("removed evidence remained authoritative", plan, err)
	}
}
func TestEntityFusionRejectsForgedGlobalIdentityAndForeignPriorRows(t *testing.T) {
	for _, scenario := range []string{"identity", "prior_model", "prior_duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			build, draft, claims := entityFusionFixture(t, "contract")
			initial, err := PlanEntityFusion(build, draft, claims, nil, 2000)
			if err != nil {
				t.Fatal(err)
			}
			row := initial.Writes[0].Row
			previous := map[string][]map[string]any{initial.Writes[0].Spec.ResourceID: {row}}
			if scenario == "identity" {
				for i := range claims {
					if claims[i].ObjectTypeID == "contract_doc" {
						claims[i].RecordID = "forged"
					}
				}
			}
			if scenario == "prior_model" {
				row["model_fingerprint"] = "foreign"
			}
			if scenario == "prior_duplicate" {
				previous[initial.Writes[0].Spec.ResourceID] = append(previous[initial.Writes[0].Spec.ResourceID], row)
			}
			plan, err := PlanEntityFusion(build, draft, claims, previous, 2000)
			if err == nil || plan != nil {
				t.Fatal("untrusted entity snapshot accepted", scenario)
			}
		})
	}
}

func TestEntityFusionUsesOnlyAnAdoptedIdentityLinkForFilePeriods(t *testing.T) {
	for _, scenario := range []string{"confirmed", "future_only", "document_authority", "unadopted_link", "other_version_date"} {
		t.Run(scenario, func(t *testing.T) {
			build, draft, claims := entityFusionFixture(t, "contract")
			entity := instanceObject(draft.Items, "contract_doc")
			instanceProperty(entity, "amount").SourceOfRecord = "fusion"
			if scenario == "document_authority" {
				instanceProperty(entity, "amount").SourceOfRecord = "document"
			}
			file := instanceObject(draft.Items, "contract_file")
			file.Properties = append(file.Properties, &interfaces.DraftProperty{Name: "starts", Type: "date"})
			file.Document.FilePeriod = &interfaces.FileEffectivePeriod{From: "starts", ConfirmedBy: build.Owner, ConfirmedAt: 10}
			relation := &interfaces.DraftRelationType{ID: "file_contract", Name: "Same contract", Adopted: scenario != "unadopted_link", Source: file.ID, Target: "contract_doc", Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE, Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}}
			draft.Items.RelationTypes = append(draft.Items.RelationTypes, relation)
			plan, err := kbdataset.NewPlan(draft, build.GenerationID, "catalog", build.Owner)
			if err != nil {
				t.Fatal(err)
			}
			plan.State = "ready"
			for i := range plan.Datasets {
				plan.Datasets[i].Ready = true
			}
			draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
			build.ModelFingerprint = plan.ModelFingerprint
			original := append([]SourceClaim{}, claims...)
			for _, claim := range original {
				copyClaim := claim
				copyClaim.ID += "-second"
				copyClaim.DocumentID = "second-document"
				if copyClaim.Property == "amount" {
					copyClaim.Value = json.Number("30")
				}
				claims = append(claims, copyClaim)
			}
			for _, item := range []struct{ doc, date string }{{original[0].DocumentID, "2026-01-01"}, {"second-document", "2026-02-01"}} {
				version := original[0].ContentVersionID
				if scenario == "other_version_date" && item.doc == "second-document" {
					version = "another-version"
				}
				claims = append(claims, SourceClaim{ID: item.doc + "-date", ObjectTypeID: file.ID, RecordID: "file-" + item.doc, DocumentID: item.doc, ContentVersionID: version, Property: "starts", Identity: map[string]any{"number": "ABC"}, Value: item.date})
				// All claims for the same file record share its identity.
				for i := range claims {
					if claims[i].ObjectTypeID == file.ID && claims[i].DocumentID == item.doc {
						claims[i].RecordID = "file-" + item.doc
					}
				}
			}
			year := 2026
			if scenario == "future_only" {
				year = 2025
			}
			fused, err := PlanEntityFusion(build, draft, claims, nil, time.Date(year, 3, 1, 0, 0, 0, 0, time.UTC).UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "confirmed" {
				if fused.Writes[0].Row["amount"] != json.Number("30") || fused.Summaries[0].Pending != 0 {
					t.Fatal("confirmed file dates were ignored", fused)
				}
			} else if fused.Writes[0].Row["amount"] != nil || fused.Summaries[0].Pending != 1 {
				t.Fatal("unconfirmed or foreign dates selected a current value", scenario, fused)
			}
		})
	}
}

func TestMentioningAnotherEntityDoesNotMakeTheFilesPeriodItsAuthority(t *testing.T) {
	for _, dated := range []bool{false, true} {
		build, draft, claims := entityFusionFixture(t, "supplier")
		file := instanceObject(draft.Items, "supplier_file")
		file.Properties = append(file.Properties, &interfaces.DraftProperty{Name: "invoice_no", Type: "string"}, &interfaces.DraftProperty{Name: "starts", Type: "date"})
		file.Document.FileIdentity = &interfaces.FileBusinessIdentity{Property: "invoice_no", ConfirmedBy: build.Owner, ConfirmedAt: 10}
		if dated {
			file.Document.FilePeriod = &interfaces.FileEffectivePeriod{From: "starts", ConfirmedBy: build.Owner, ConfirmedAt: 10}
		}
		draft.Items.RelationTypes = append(draft.Items.RelationTypes, &interfaces.DraftRelationType{ID: "invoice_supplier", Name: "Invoice supplier", Adopted: true, Source: file.ID, Target: "supplier_doc", Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE, Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}})
		plan, err := kbdataset.NewPlan(draft, build.GenerationID, "catalog", build.Owner)
		if err != nil {
			t.Fatal(err)
		}
		plan.State = "ready"
		for i := range plan.Datasets {
			plan.Datasets[i].Ready = true
		}
		draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
		build.ModelFingerprint = plan.ModelFingerprint
		result, err := PlanEntityFusion(build, draft, claims, nil, 2000)
		if err != nil || len(result.Writes) != 1 || result.Writes[0].Row["amount"] != json.Number("25.50") {
			t.Fatal("ordinary mention was mistaken for file-period authority", dated, result, err)
		}
	}
}
