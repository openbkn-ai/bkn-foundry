// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"testing"
)

func TestSourceExecutionKeepsAnActualTaskReceiptAndRejectsReplacedRows(t *testing.T) {
	build, _, _, task := extractionFixture(t, "contract", false)
	build.ModelID, build.Model = task.ModelID, task.Model
	build.Files[0].AgentVersion, build.Files[0].PromptVersion, build.Files[0].ModelID = task.AgentVersion, task.PromptVersion, task.ModelID
	raw, err := extractionVersion(task)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := verifiedSourceExecution(build, 0, raw)
	if err != nil || receipt.TaskID != task.TaskID || receipt.RecordedAt != task.CreateTime {
		t.Fatal("actual source execution was lost", receipt, err)
	}
	for _, scenario := range []string{"another_task", "unknown_legacy_task", "wrong_model", "wrong_prompt"} {
		t.Run(scenario, func(t *testing.T) {
			var body map[string]any
			if json.Unmarshal([]byte(raw), &body) != nil {
				t.Fatal("fixture")
			}
			switch scenario {
			case "another_task":
				body["task_id"] = "a-newer-run-with-the-same-versions"
			case "unknown_legacy_task":
				delete(body, "task_id")
			case "wrong_model":
				body["model"] = "changed-model"
			case "wrong_prompt":
				body["prompt"] = 99
			}
			encoded, _ := json.Marshal(body)
			if _, err := verifiedSourceExecution(build, 0, string(encoded)); err == nil {
				t.Fatal("a replaced or unverified source row was trusted")
			}
		})
	}
}

func TestInheritedSourceExecutionUsesActualFileReceipt(t *testing.T) {
	build, _, _, task := extractionFixture(t, "invoice", false)
	task.Model = "actual-model-name"
	build.ModelID, build.Model = task.ModelID, task.Model
	build.Files[0].AgentVersion, build.Files[0].PromptVersion, build.Files[0].ModelID = task.AgentVersion, task.PromptVersion, task.ModelID
	build.Files[0].Model = task.Model
	raw, err := extractionVersion(task)
	if err != nil {
		t.Fatal(err)
	}
	// New extraction defaults do not rewrite acknowledged unchanged file receipts.
	build.AgentVersion++
	build.ModelID = "new-default-model"
	build.Model = "new-default-name"
	if _, err := verifiedSourceExecution(build, 0, raw); err != nil {
		t.Fatal("actual inherited receipt rejected", err)
	}
	build.Files[0].Model = "unverified-name"
	if _, err := verifiedSourceExecution(build, 0, raw); err == nil {
		t.Fatal("mismatched file model name accepted")
	}
}

func TestSourceExecutionOptionalNamesDoNotInheritAnotherFilesLabel(t *testing.T) {
	for _, scenario := range []string{"both_absent", "row_name_only", "file_name_only", "both_matching", "known_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			build, _, _, task := extractionFixture(t, "asset", false)
			build.ModelID, build.Model = task.ModelID, "another-file-label"
			file := &build.Files[0]
			file.AgentVersion, file.PromptVersion, file.ModelID = task.AgentVersion, task.PromptVersion, task.ModelID
			task.Model, file.Model = "", ""
			if scenario == "row_name_only" || scenario == "both_matching" || scenario == "known_conflict" {
				task.Model = "actual-label"
			}
			if scenario == "file_name_only" || scenario == "both_matching" {
				file.Model = "actual-label"
			}
			if scenario == "known_conflict" {
				file.Model = "different-label"
			}
			raw, err := extractionVersion(task)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := verifiedSourceExecution(build, 0, raw)
			if scenario == "known_conflict" {
				if err == nil {
					t.Fatal("contradictory known name was accepted")
				}
				return
			}
			if err != nil || receipt.ModelName != task.Model {
				t.Fatal("optional model name was rejected or invented", err)
			}
		})
	}
}
