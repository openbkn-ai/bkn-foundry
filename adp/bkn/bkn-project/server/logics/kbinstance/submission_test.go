// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"strings"
	"testing"
)

func submissionBuild() *interfaces.KBBuild {
	return &interfaces.KBBuild{ID: "build", ProjectID: "project", State: "running", Revision: 1, AgentVersion: 3, Files: []interfaces.KBFileBuild{{File: interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: "2", ContentVersionID: "3"}, State: "pending"}}, UpdateTime: 10}
}
func TestSubmissionIntentCannotBeReplayedAsAnotherInference(t *testing.T) {
	build := submissionBuild()
	before, _ := json.Marshal(build)
	intent, err := PrepareFileSubmission(build, 0, 11)
	if err != nil || intent.Files[0].State != "submitting" || intent.Revision != 2 {
		t.Fatal("no durable submission intent", err)
	}
	after, _ := json.Marshal(build)
	if string(before) != string(after) {
		t.Fatal("submission mutated unsaved process record")
	}
	if _, err := PrepareFileSubmission(intent, 0, 12); err == nil {
		t.Fatal("uncertain submission silently retried")
	}
	interrupted, err := InterruptFileSubmission(intent, 0, 13)
	if err != nil || interrupted.State != "attention" || interrupted.Files[0].State != "submission_unknown" {
		t.Fatal("crash uncertainty hidden", err)
	}
	if _, err := PrepareFileSubmission(interrupted, 0, 14); err == nil {
		t.Fatal("unknown submission silently retried")
	}
}
func TestKnownTaskMayResumeButNeverBeReplacedByAnotherTask(t *testing.T) {
	intent, err := PrepareFileSubmission(submissionBuild(), 0, 11)
	if err != nil {
		t.Fatal(err)
	}
	version := 3
	task := &interfaces.BknAgentTask{TaskID: "task-one", AgentID: "extractor", Status: interfaces.BKN_AGENT_TASK_PENDING, AgentVersion: &version}
	waiting, err := AttachFileTask(intent, 0, "extractor", task, 12)
	if err != nil || waiting.Files[0].State != "waiting" || waiting.Files[0].AgentTaskID != "task-one" || waiting.Files[0].AgentVersion == nil || *waiting.Files[0].AgentVersion != 3 {
		t.Fatal("known task not saved", err)
	}
	task.TaskID = "task-other"
	if _, err := AttachFileTask(waiting, 0, "extractor", task, 13); err == nil {
		t.Fatal("known task replaced by another inference")
	}
	task.TaskID = "task-one"
	task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
	task.ModelID = "actual-model"
	ready, err := AttachFileTask(waiting, 0, "extractor", task, 14)
	if err != nil || ready.Files[0].State != "validating" || ready.Files[0].ModelID != "actual-model" || ready.Files[0].PromptVersion != nil {
		t.Fatal("actual execution metadata fabricated", err)
	}
}
func TestLateTaskAcknowledgementCannotReviveCancelledBuildOrChangePinnedVersion(t *testing.T) {
	for _, scenario := range []string{"cancelled", "wrong_agent", "wrong_version", "unknown_version", "no_id", "late_acknowledgement"} {
		t.Run(scenario, func(t *testing.T) {
			intent, err := PrepareFileSubmission(submissionBuild(), 0, 11)
			if err != nil {
				t.Fatal(err)
			}
			v := 3
			task := &interfaces.BknAgentTask{TaskID: "task", AgentID: "extractor", Status: "pending", AgentVersion: &v}
			switch scenario {
			case "cancelled":
				intent.State = "cancelled"
			case "wrong_agent":
				task.AgentID = "another"
			case "wrong_version":
				v = 4
			case "unknown_version":
				task.AgentVersion = nil
			case "no_id":
				task.TaskID = ""
			case "late_acknowledgement":
				intent, err = InterruptFileSubmission(intent, 0, 12)
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := AttachFileTask(intent, 0, "extractor", task, 13)
			if scenario == "late_acknowledgement" {
				if err != nil || got.State != "running" || got.Files[0].State != "waiting" {
					t.Fatal("same-task late acknowledgement lost", err)
				}
			} else if scenario == "unknown_version" {
				if err != nil || got.Files[0].AgentVersion != nil || got.Files[0].State != "waiting" {
					t.Fatal("unknown version fabricated", err)
				}
				task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
				done, err := AttachFileTask(got, 0, "extractor", task, 14)
				if err != nil || done.State != "attention" || done.Files[0].Reason != "execution_version_unknown" || done.Files[0].AgentTaskID != "task" {
					t.Fatal("unknown execution identity used or known task lost", err)
				}
			} else if err == nil {
				t.Fatal("invalid execution attached")
			}
		})
	}
}

func TestResumingOneFileCannotEraseAnotherFilesUncertaintyOrPersistRawOutput(t *testing.T) {
	intent, err := PrepareFileSubmission(submissionBuild(), 0, 11)
	if err != nil {
		t.Fatal(err)
	}
	intent.Files = append(intent.Files, interfaces.KBFileBuild{State: "submission_unknown", Reason: "submission_unknown"})
	intent.State = "attention"
	version := 3
	task := &interfaces.BknAgentTask{TaskID: "task", AgentID: "extractor", AgentVersion: &version, Status: "succeeded", Output: "private raw model output", FailureDetail: "private upstream diagnostics"}
	got, err := AttachFileTask(intent, 0, "extractor", task, 12)
	if err != nil || got.State != "attention" || got.Files[0].State != "validating" {
		t.Fatal("another file's uncertainty erased", err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "private") {
		t.Fatal("raw task content entered process persistence")
	}
}
