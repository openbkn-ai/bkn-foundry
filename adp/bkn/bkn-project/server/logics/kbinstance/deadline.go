// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// ReasonExtractionTimedOut marks a known task that did not finish in time. Unlike
// a lost submission it blocks nothing: its task id is known and is never reused.
const ReasonExtractionTimedOut = "extraction_timed_out"

const defaultAgentTaskDeadline = 30 * time.Minute

var agentTaskDeadline atomic.Int64

// ConfigureAgentTaskDeadline installs the startup setting. Zero or less keeps the
// default, since an Agent task must never wait without limit.
func ConfigureAgentTaskDeadline(deadline time.Duration) {
	agentTaskDeadline.Store(int64(deadline))
}

// AgentTaskDeadline is how long a document extraction task may stay unfinished.
func AgentTaskDeadline() time.Duration {
	if configured := time.Duration(agentTaskDeadline.Load()); configured > 0 {
		return configured
	}
	return defaultAgentTaskDeadline
}

// TaskOverdue reports a task past its deadline that has not reported an end. A
// finished task is always accepted, however late; its result is already paid for.
func TaskOverdue(file *interfaces.KBFileBuild, task *interfaces.BknAgentTask, pollErr error, now int64) bool {
	if file.SubmittedAt == 0 || now <= file.SubmittedAt+AgentTaskDeadline().Milliseconds() {
		return false
	}
	return pollErr != nil || task == nil || task.Status == interfaces.BKN_AGENT_TASK_PENDING || task.Status == interfaces.BKN_AGENT_TASK_RUNNING
}

// WorkSummary is what operators see of unfinished background work.
type WorkSummary struct {
	Pending       int            `json:"pending"`
	ByState       map[string]int `json:"by_state"`
	StuckSeconds  int64          `json:"stuck_seconds"`
	AtSampleLimit bool           `json:"at_sample_limit,omitempty"`
}

// workSampleLimit matches the adapters' cap on records read for this report.
const workSampleLimit = 200

// SummarizeWork counts work and finds the longest time without progress.
func SummarizeWork(items []interfaces.KBWorkItem, now int64) WorkSummary {
	summary := WorkSummary{Pending: len(items), ByState: map[string]int{}, AtSampleLimit: len(items) >= workSampleLimit}
	for _, item := range items {
		summary.ByState[item.State]++
		if stuck := (now - item.ProgressTime) / 1000; stuck > summary.StuckSeconds {
			summary.StuckSeconds = stuck
		}
	}
	return summary
}
