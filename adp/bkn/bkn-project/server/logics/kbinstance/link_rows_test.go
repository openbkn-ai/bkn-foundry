// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

func TestPlanSystemLinksKeepsTheVerifiedBusinessNumberAndOriginalPrimaryKey(t *testing.T) {
	for _, scenario := range []string{"valid", "missing_target", "unconfirmed", "changed_model", "different_execution"} {
		t.Run(scenario, func(t *testing.T) {
			build, draft, input, task := extractionFixture(t, "contract", false)
			target := &interfaces.DraftObjectType{ID: "system_contract", Adopted: true, Table: interfaces.DraftTable{ResourceID: "system-table"}, PrimaryKeys: []string{"id"}, Properties: []*interfaces.DraftProperty{{Name: "id", Type: "unsigned integer", MappedField: "id"}, {Name: "number", Type: "string", MappedField: "number"}}}
			relation := &interfaces.DraftRelationType{ID: "same_contract", Source: "contract_doc", Target: target.ID, Adopted: true, Cardinality: interfaces.DRAFT_CARDINALITY_ONE_TO_ONE, Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number", BusinessIdentity: &interfaces.DraftBusinessIdentityEvidence{TargetObjectTypeID: target.ID, ResourceID: target.Table.ResourceID, Property: "number", MappedField: "number", Type: "string", Rows: 1, Distinct: 1, ConfirmedBy: build.Owner, CheckedAt: 1}}}
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, target)
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
			draft, err = kbdataset.BindPreview(draft, plan)
			if err != nil {
				t.Fatal(err)
			}
			build.ModelFingerprint = plan.ModelFingerprint
			writes, err := MaterializeFile(build, 0, draft, input, task)
			if err != nil {
				t.Fatal(err)
			}
			build.ModelID, build.Model = task.ModelID, task.Model
			build.Files[0].State, build.Files[0].AgentVersion, build.Files[0].PromptVersion, build.Files[0].ModelID = "succeeded", task.AgentVersion, task.PromptVersion, task.ModelID
			reader := &claimSnapshot{rows: map[string][]map[string]any{}}
			for _, write := range writes {
				reader.rows[write.Spec.ResourceID] = append(reader.rows[write.Spec.ResourceID], write.Row)
			}
			claims, err := ReadSourceClaims(context.Background(), reader, build, draft, func(context.Context) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			system := &SystemSnapshot{Targets: []LinkTarget{{Identity: "ABC", RecordID: json.Number("18446744073709551615")}}, Digest: strings.Repeat("a", 64)}
			switch scenario {
			case "missing_target":
				system.Targets = nil
			case "unconfirmed":
				relation.DocumentMapping.BusinessIdentity = nil
			case "changed_model":
				build.ModelFingerprint = "changed"
			case "different_execution":
				for i := range claims {
					claims[i].ExtractionVersion = `{"task_id":"other"}`
				}
			}
			result, err := PlanSystemLinks(build, draft, relation, claims, system, nil)
			if scenario != "valid" && scenario != "missing_target" {
				if err == nil || result != nil {
					t.Fatal("invalid linking input returned writes", scenario, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "missing_target" {
				if len(result.Writes) != 0 || len(result.Resolution.Pending) != 1 {
					t.Fatal("missing target produced an edge")
				}
				return
			}
			if len(result.Writes) != 1 || len(result.Resolution.Linked) != 1 {
				t.Fatal("complete linking did not produce one edge", result)
			}
			row := result.Writes[0].Row
			business, ok := row["target_identity"].(map[string]any)
			if !ok || len(business) != 1 || business["number"] != "ABC" {
				t.Fatal("review identity exposed the system primary key instead of the confirmed business number", business)
			}
			if row["target_record_id"] != json.Number("18446744073709551615") || row["source_quote"] != "" || row["document_id"] == "" || row["extraction_version"] == "" {
				t.Fatal("primary key, locator or execution changed", row)
			}
			again, err := PlanSystemLinks(build, draft, relation, claims, system, nil)
			if err != nil || again.Writes[0].Row["_id"] != row["_id"] {
				t.Fatal("repeated plan changed edge identity", err)
			}
		})
	}
}
