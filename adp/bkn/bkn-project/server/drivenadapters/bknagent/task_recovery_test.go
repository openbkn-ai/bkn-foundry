// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package bknagent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskRecoveryInputIsReadableButNeverSerialized(t *testing.T) {
	task, err := readTask([]byte(`{"task_id":"t1","agent_id":"concept","status":"succeeded","input":{"message":"private-recovery-input"},"output":"{}"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(task.Input), "private-recovery-input") {
		t.Fatal("task input cannot be checked for exact recovery scope")
	}
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-recovery-input") || strings.Contains(string(raw), `"input"`) {
		t.Fatal("private task input leaked into a business DTO")
	}
}
