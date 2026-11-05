// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"strings"
)

// copyFileStep changes process metadata only. The coordinator must persist the
// returned revision with its lease and current source/model guard before doing
// any external action. These functions neither submit nor retry an inference.
func copyFileStep(build *interfaces.KBBuild, index int, now int64) (*interfaces.KBBuild, error) {
	if build == nil || build.ID == "" || build.ProjectID == "" || build.Revision < 1 || build.AgentVersion < 1 || index < 0 || index >= len(build.Files) || now <= 0 || (build.State != "running" && build.State != "queued" && build.State != "attention") {
		return nil, ErrInvalidBuild
	}
	raw, err := json.Marshal(build)
	if err != nil {
		return nil, ErrInvalidBuild
	}
	var copy interfaces.KBBuild
	if json.Unmarshal(raw, &copy) != nil {
		return nil, ErrInvalidBuild
	}
	copy.Revision++
	copy.UpdateTime = max(now, copy.UpdateTime+1)
	return &copy, nil
}

// PrepareFileSubmission is a write-ahead intent, not permission to call twice.
// A persisted submitting state is never automatically eligible for submission.
func PrepareFileSubmission(build *interfaces.KBBuild, index int, now int64) (*interfaces.KBBuild, error) {
	next, err := copyFileStep(build, index, now)
	if err != nil {
		return nil, err
	}
	file := &next.Files[index]
	if (next.State != "running" && next.State != "queued" && next.State != "attention") || (file.State != "pending" && file.State != "reading") || file.AgentTaskID != "" {
		return nil, ErrInvalidBuild
	}
	next.State = "running"
	for _, other := range next.Files {
		if other.State == "failed" || other.State == "submission_unknown" {
			next.State = "attention"
		}
	}
	file.State = "submitting"
	file.Reason = ""
	file.SubmittedAt, file.NextAttempt = now, 0
	return next, nil
}

// InterruptFileSubmission preserves uncertainty after a lost acknowledgement or
// coordinator restart. A person must start a new build, or the original request
// must return a known task acknowledgement. Neither path replays this intent.
func InterruptFileSubmission(build *interfaces.KBBuild, index int, now int64) (*interfaces.KBBuild, error) {
	next, err := copyFileStep(build, index, now)
	if err != nil {
		return nil, err
	}
	file := &next.Files[index]
	if file.State != "submitting" || file.AgentTaskID != "" {
		return nil, ErrInvalidBuild
	}
	file.State = "submission_unknown"
	file.Reason = "submission_unknown"
	next.State = "attention"
	return next, nil
}

// AttachFileTask records actual execution metadata and resumes only that task.
// Raw outputs, failure details and request credentials never enter the process
// journal. Missing version metadata is unknown, never the requested version.
func AttachFileTask(build *interfaces.KBBuild, index int, agent string, task *interfaces.BknAgentTask, now int64) (*interfaces.KBBuild, error) {
	next, err := copyFileStep(build, index, now)
	if err != nil {
		return nil, err
	}
	if task == nil || agent == "" || task.AgentID != agent || strings.TrimSpace(task.TaskID) == "" || len(task.TaskID) > 128 ||
		(task.AgentVersion != nil && *task.AgentVersion != next.AgentVersion) || (task.PromptVersion != nil && *task.PromptVersion < 1) {
		return nil, ErrInvalidBuild
	}
	file := &next.Files[index]
	if (file.State != "submitting" && file.State != "submission_unknown" && file.State != "waiting") || (file.AgentTaskID != "" && file.AgentTaskID != task.TaskID) {
		return nil, ErrInvalidBuild
	}
	if (next.PromptVersion != nil && task.PromptVersion != nil && *next.PromptVersion != *task.PromptVersion) || (next.ModelID != "" && task.ModelID != "" && next.ModelID != task.ModelID) || (next.Model != "" && task.Model != "" && next.Model != task.Model) {
		return nil, ErrInvalidBuild
	}
	if task.PromptVersion != nil {
		v := *task.PromptVersion
		next.PromptVersion = &v
	}
	if task.ModelID != "" {
		next.ModelID = task.ModelID
	}
	if task.Model != "" {
		next.Model = task.Model
	}
	if file.PromptVersion != nil && task.PromptVersion != nil && *file.PromptVersion != *task.PromptVersion {
		return nil, ErrInvalidBuild
	}
	if file.ModelID != "" && task.ModelID != "" && file.ModelID != task.ModelID {
		return nil, ErrInvalidBuild
	}
	file.AgentTaskID = task.TaskID
	if task.AgentVersion != nil {
		v := *task.AgentVersion
		file.AgentVersion = &v
	}
	if task.PromptVersion != nil {
		v := *task.PromptVersion
		file.PromptVersion = &v
	}
	if task.ModelID != "" {
		file.ModelID = task.ModelID
	}
	if task.Model != "" {
		file.Model = task.Model
	}
	file.Reason = ""
	if (task.Status == interfaces.BKN_AGENT_TASK_SUCCEEDED || task.Status == interfaces.BKN_AGENT_TASK_FAILED) && task.CreateTime > 0 && task.UpdateTime >= task.CreateTime {
		duration := task.UpdateTime - task.CreateTime
		file.DurationMillis = &duration
	}
	switch task.Status {
	case interfaces.BKN_AGENT_TASK_PENDING, interfaces.BKN_AGENT_TASK_RUNNING:
		file.State = "waiting"
		next.State = "running"
	case interfaces.BKN_AGENT_TASK_SUCCEEDED:
		if file.AgentVersion == nil {
			file.State = "failed"
			file.Reason = "execution_version_unknown"
			next.State = "attention"
		} else {
			file.State = "validating"
			next.State = "running"
		}
	case interfaces.BKN_AGENT_TASK_FAILED:
		file.State = "failed"
		file.Reason = "extraction_failed"
		next.State = "attention"
	default:
		return nil, ErrInvalidBuild
	}
	for _, other := range next.Files {
		if other.State == "failed" || other.State == "submission_unknown" {
			next.State = "attention"
			break
		}
	}
	return next, nil
}
