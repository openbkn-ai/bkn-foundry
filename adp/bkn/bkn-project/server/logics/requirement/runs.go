// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A project's specification is written by three runs of the requirements Agent, one at a time:
// the concepts run extracts concepts, rules, conflicts, choices and stated kinetic needs from the
// documents, before any model exists; the kinetic run analyses the confirmed model for kinetic
// needs; scenario drafting drafts scenarios and questions last. Each writes only its own part, so
// none undoes another's.

// staleRunBefore is the start time before which a run still recorded as going has ended without
// being recorded: past the Agent's deadline twice, with a minute to record it.
func (s *requirementService) staleRunBefore() int64 {
	return s.now() - draftDeadline.Milliseconds() - time.Minute.Milliseconds()
}

// askAgent runs the Agent once with message and the response format and waits for it up to
// agentDeadline, decoding its answer into out: the failure code with why, or none.
func (s *requirementService) askAgent(ctx context.Context, caller interfaces.AccountInfo, message string,
	format map[string]any, out any, taskID *string) (string, error) {
	task, err := s.ba.RunTask(ctx, caller, &interfaces.BknAgentRunRequest{
		AgentID: requirementAgentID, Message: message, ResponseFormat: format,
	})
	if err != nil {
		return startFailure(err), err
	}
	*taskID = task.TaskID
	deadline := s.now() + agentDeadline.Milliseconds()
	for !task.Terminal() {
		if s.now() >= deadline {
			return failureAgentTimeout, fmt.Errorf("task %s did not finish", task.TaskID)
		}
		s.sleep(pollInterval)
		// A failed or slow read is retried until the deadline: the task goes on in bkn-agent.
		readCtx, cancel := context.WithTimeout(ctx, taskReadTimeout)
		read, err := s.ba.GetTask(readCtx, caller, task.TaskID)
		cancel()
		if err == nil {
			task = read
		}
	}
	if task.Status != interfaces.BKN_AGENT_TASK_SUCCEEDED {
		return failureAgentFailed, errors.New(task.FailureDetail)
	}
	if err := json.Unmarshal([]byte(task.Output), out); err != nil {
		return failureOutputInvalid, err
	}
	return "", nil
}

// askTwice asks the Agent, and once more when its answer was not the JSON the schema asks for: a
// model without structured output sometimes writes a field missing or broken, and asked again,
// writes it whole. A run that ran out of time is not asked again. It reports whether it asked again.
func (s *requirementService) askTwice(ctx context.Context, projectID string, caller interfaces.AccountInfo,
	message string, format map[string]any, newOut func() any, taskID *string) (any, bool, string, error) {
	out := newOut()
	failure, err := s.askAgent(ctx, caller, message, format, out, taskID)
	retried := failure == failureOutputInvalid ||
		failure == failureAgentFailed && strings.Contains(err.Error(), structuredOutputFailed)
	if retried {
		logger.Warnf("Requirement run of project %s asks the Agent again (%s): %v", projectID, failure, err)
		out = newOut()
		failure, err = s.askAgent(ctx, caller, message, format, out, taskID)
	}
	return out, retried, failure, err
}

// partRun is one concepts or kinetic run: the message and schema it asks the Agent with, and
// how it writes what the Agent answered into the specification as stored when it ends.
type partRun struct {
	kind      string
	projectID string
	run       *interfaces.RequirementRun
	caller    interfaces.AccountInfo
	format    map[string]any
	newOut    func() any
	// prepareMessage builds the message, in the background: it may read what is slow to read.
	prepareMessage func(ctx context.Context) (string, error)
	// check checks the answer, outside the write: it may write what the specification does not hold.
	check func(ctx context.Context, out any)
	// write writes what check kept into the specification as stored.
	write func(req *interfaces.Requirement, out any)
	// inputs are what the run read, recorded once it is ready.
	inputs *interfaces.RequirementInputs
}

// startRun records a run of kind going over the specification as read at readUpdateTime (0 when
// the project had none), refusing while the project drafts or another run goes.
func (s *requirementService) startRun(ctx context.Context, projectID string, kind string,
	caller interfaces.AccountInfo, readUpdateTime int64) (*interfaces.RequirementRun, error) {
	now := s.now()
	run := &interfaces.RequirementRun{Status: interfaces.REQUIREMENT_RUN_RUNNING, RunID: s.newID(), By: caller,
		StartTime: now, UpdateTime: now}
	started, err := s.ra.StartRun(ctx, projectID, kind, run, s.staleRunBefore(), readUpdateTime)
	switch {
	case errors.Is(err, interfaces.ErrRequirementChanged):
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Stale)
	case err != nil:
		return nil, internalError(ctx, "Start requirement run failed", err)
	case !started:
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
	}
	return run, nil
}

// execute asks the Agent and records the outcome of the run: what it wrote, or why it failed,
// leaving the specification as it was.
func (s *requirementService) execute(ctx context.Context, p *partRun) {
	var taskID string
	var out any
	retried := false
	message, err := p.prepareMessage(ctx)
	failure := ""
	if err != nil {
		failure = failureAgentUnavailable
	} else {
		out, retried, failure, err = s.askTwice(ctx, p.projectID, p.caller, message, p.format, p.newOut, &taskID)
	}
	if failure == "" {
		p.check(ctx, out)
	}
	if failure != "" {
		logger.Errorf("Requirement %s run of project %s failed (%s, asked again: %t): %v", p.kind, p.projectID,
			failure, retried, err)
	}
	finished, err := s.ra.FinishRun(ctx, p.projectID, p.kind, p.run.RunID, func(req *interfaces.Requirement) {
		run := req.Spec.Runs[p.kind]
		run.AgentTaskID, run.Retried, run.UpdateTime = taskID, retried, s.now()
		req.UpdateTime = run.UpdateTime
		if failure != "" {
			run.Status, run.Error = interfaces.REQUIREMENT_RUN_FAILED, failure
			return
		}
		p.write(req, out)
		run.Status, run.Error, run.Inputs = interfaces.REQUIREMENT_RUN_READY, "", p.inputs
	})
	if err != nil {
		logger.Errorf("Record requirement %s run of project %s failed: %v", p.kind, p.projectID, err)
	} else if !finished {
		logger.Warnf("Requirement %s run of project %s was taken over before it ended", p.kind, p.projectID)
	}
}

// startedRun is the project's specification as a run just started leaves it.
func (s *requirementService) startedRun(ctx context.Context, projectID string) (*interfaces.Requirement, error) {
	req, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	if !ok {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
	}
	return req, nil
}

// failInterruptedRuns fails the concepts and kinetic runs a previous run of the service left going.
func (s *requirementService) failInterruptedRuns(ctx context.Context) (int, error) {
	projects, err := s.ra.ProjectsWithRunning(ctx)
	if err != nil {
		return 0, err
	}
	failed := 0
	for _, projectID := range projects {
		req, ok, err := s.ra.GetRequirement(ctx, projectID)
		if err != nil {
			return failed, err
		}
		if !ok {
			continue
		}
		for kind, run := range req.Spec.Runs {
			if run.Status != interfaces.REQUIREMENT_RUN_RUNNING {
				continue
			}
			done, err := s.ra.FinishRun(ctx, projectID, kind, run.RunID, func(req *interfaces.Requirement) {
				r := req.Spec.Runs[kind]
				r.Status, r.Error, r.UpdateTime = interfaces.REQUIREMENT_RUN_FAILED, failureInterrupted, s.now()
				req.UpdateTime = r.UpdateTime
			})
			if err != nil {
				return failed, err
			}
			if done {
				failed++
			}
		}
	}
	return failed, nil
}

// rejectedOf keeps the rejected items of other runs than run, for run to list its own after.
func rejectedOf(rejected []*interfaces.RequirementRejected, run string) []*interfaces.RequirementRejected {
	kept := []*interfaces.RequirementRejected{}
	for _, r := range rejected {
		if r.Run != run {
			kept = append(kept, r)
		}
	}
	return kept
}

// nextVersion is the version a run writes the specification at: a specification an ontology
// confirmation was made against is frozen, so writing it again starts the next version.
func nextVersion(version int, draft *interfaces.Draft) int {
	if version == 0 {
		return 1
	}
	if draft != nil && draft.Items.Confirmation != nil && draft.Items.Confirmation.RequirementVersion == version {
		return version + 1
	}
	return version
}

// modelConfirmed tells whether the project's ontology was confirmed to its branch and adopts an
// object type: the kinetic run and scenario drafting rest on it. A confirmation under way or failed
// has committed nothing yet.
func modelConfirmed(draft *interfaces.Draft) bool {
	if draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY || draft.Items.Confirmation == nil ||
		draft.Items.Confirmation.Commit == "" {
		return false
	}
	for _, ot := range draft.Items.ObjectTypes {
		if ot.Adopted {
			return true
		}
	}
	return false
}
