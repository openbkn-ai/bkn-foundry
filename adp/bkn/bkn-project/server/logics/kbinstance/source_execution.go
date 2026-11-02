// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type sourceExecution struct {
	Agent      int    `json:"agent"`
	Prompt     int    `json:"prompt"`
	Model      string `json:"model"`
	ModelName  string `json:"model_name"`
	TaskID     string `json:"task_id"`
	RecordedAt int64  `json:"recorded_at"`
}

// Source row ids intentionally survive re-extraction. The actual task receipt
// distinguishes the current build from a newer run that replaced those rows
// under the same model/Agent versions. Unknown legacy receipts require rebuild;
// there is no guess or impersonation of a retired background principal.
func verifiedSourceExecution(build *interfaces.KBBuild, index int, raw string) (*sourceExecution, error) {
	if build == nil || index < 0 || index >= len(build.Files) {
		return nil, ErrInvalidBuild
	}
	file := build.Files[index]
	var execution sourceExecution
	if json.Unmarshal([]byte(raw), &execution) != nil || execution.TaskID == "" || execution.TaskID != file.AgentTaskID || execution.Agent != build.AgentVersion || file.AgentVersion == nil || execution.Agent != *file.AgentVersion || file.PromptVersion == nil || execution.Prompt != *file.PromptVersion || execution.Model == "" || execution.Model != file.ModelID || execution.Model != build.ModelID || execution.ModelName != build.Model || execution.RecordedAt < 0 {
		return nil, ErrBuildChanged
	}
	return &execution, nil
}
