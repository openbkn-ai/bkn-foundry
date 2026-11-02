// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

func buildPlanFixture(t *testing.T) (*interfaces.Draft, *interfaces.KBSelection) {
	t.Helper()
	owner := interfaces.AccountInfo{ID: "owner", Type: "user"}
	object := &interfaces.DraftObjectType{ID: "source_document", Name: "Document", Adopted: true, PrimaryKeys: []string{"_id"}, Document: &interfaces.DraftDocumentModel{Role: "evidence_document", SelectionID: "selection", SelectionRevision: 2}, Properties: []*interfaces.DraftProperty{{Name: "_id", Type: "string", ReadOnly: true}}}
	draft := &interfaces.Draft{ProjectID: "project", Status: "ready", UpdateTime: 7, Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{object}, Confirmation: &interfaces.DraftConfirmation{UpdateTime: 7, Commit: "confirmed-commit"}}}
	plan, err := kbdataset.NewPlan(draft, "generation", "catalog", owner)
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
	selection := &interfaces.KBSelection{ID: "selection", ProjectID: "project", State: "active", Revision: 2, Owner: owner, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "20", ContentVersionID: "200", SampleRole: "acceptance"}, {KnowledgeBaseID: "1", FileID: "10", ContentVersionID: "100", SampleRole: "debug"}, {KnowledgeBaseID: "1", FileID: "30", ContentVersionID: "300"}}}
	return draft, selection
}

func TestBuildManifestSeparatesDebugAcceptanceAndFullScope(t *testing.T) {
	for _, test := range []struct {
		mode  string
		want  int
		first string
	}{{"debug", 1, "10"}, {"acceptance", 1, "20"}, {"full", 3, "10"}} {
		draft, selection := buildPlanFixture(t)
		job, err := PlanFileBuild("job", "lineage", test.mode, 3, draft, selection)
		if err != nil {
			t.Fatal(err)
		}
		if len(job.Files) != test.want || job.Files[0].File.FileID != test.first || job.AgentVersion != 3 || job.DefinitionCommit != "confirmed-commit" || job.GenerationID != "generation" {
			t.Fatal("wrong immutable build scope", job)
		}
		if selection.Files[0].FileID != "20" {
			t.Fatal("planning mutated manual sample decisions")
		}
		selection.Files[1].ContentVersionID = "changed"
		if job.Files[0].File.ContentVersionID == "changed" {
			t.Fatal("build manifest aliases a mutable source decision")
		}
	}
}

func TestBuildRefusesUnconfirmedStaleOrPartlyPreparedModels(t *testing.T) {
	for _, scenario := range []string{"unconfirmed", "drafting", "no_commit", "source_changed", "project_changed", "owner_changed", "model_changed", "partial", "wrong_binding", "missing_version", "duplicate_file", "unknown_role", "unknown_mode", "unknown_agent"} {
		t.Run(scenario, func(t *testing.T) {
			draft, selection := buildPlanFixture(t)
			mode := "debug"
			version := 1
			switch scenario {
			case "unconfirmed":
				draft.UpdateTime++
			case "drafting":
				draft.Status = "drafting"
			case "no_commit":
				draft.Items.Confirmation.Commit = ""
			case "source_changed":
				selection.Revision++
			case "project_changed":
				selection.ProjectID = "other"
			case "owner_changed":
				selection.Owner.ID = "other"
			case "model_changed":
				draft.Items.ObjectTypes[0].Description = "changed"
			case "partial":
				draft.Items.DatasetPlans[0].Datasets[0].Ready = false
			case "wrong_binding":
				draft.Items.ObjectTypes[0].Table.ResourceID = "foreign-resource"
			case "missing_version":
				selection.Files[0].ContentVersionID = ""
			case "duplicate_file":
				selection.Files = append(selection.Files, selection.Files[0])
			case "unknown_role":
				selection.Files[0].SampleRole = "heldout-typo"
			case "unknown_mode":
				mode = "all_samples"
			case "unknown_agent":
				version = 0
			}
			if job, err := PlanFileBuild("job", "lineage", mode, version, draft, selection); err == nil || job != nil {
				t.Fatal("unsafe task manifest accepted")
			}
		})
	}
}
