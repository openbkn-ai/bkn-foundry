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
