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
	"sync"
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

// askOptions are how a run asks the Agent. retryCutOff asks once more for an answer cut off at
// the model's output cap; a caller that can ask for less at a time does that instead. thinking
// turns the model's reasoning on or off, empty leaving the model's default.
type askOptions struct {
	retryCutOff bool
	thinking    string
}

// longAnswer is how the runs that write the specification ask: their answer is one long JSON
// document, and reasoning spends the model's output cap before it (a thinking model once used
// all 16384 tokens on it), so it is off.
var longAnswer = askOptions{retryCutOff: true, thinking: requirementThinkingMode}

// askAgent runs the Agent once with message and the response format and waits for it up to
// agentDeadline, decoding its answer into out: the failure code with why, or none.
func (s *requirementService) askAgent(ctx context.Context, caller interfaces.AccountInfo, message string,
	format map[string]any, out any, taskID *string, thinking string) (string, error) {
	task, err := s.ba.RunTask(ctx, caller, &interfaces.BknAgentRunRequest{
		AgentID: requirementAgentID, Message: message, ResponseFormat: format, ThinkingMode: thinking,
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

// cutOff tells whether the Agent failed for an answer cut off at the model's output cap: as
// bkn-agent names it, or as the OpenAI SDK does when bkn-agent passes its error on.
func cutOff(failure string, err error) bool {
	return failure == failureAgentFailed && err != nil &&
		(strings.Contains(err.Error(), structuredOutputTruncated) || strings.Contains(err.Error(), lengthFinishReason))
}

// askTwice asks the Agent, and once more when its answer was not the JSON the schema asks for: a
// model without structured output sometimes writes a field missing or broken, and asked again,
// writes it whole; see askOptions for an answer cut off at the model's output cap. A run that ran
// out of time is not asked again. It reports whether it asked again.
func (s *requirementService) askTwice(ctx context.Context, projectID string, caller interfaces.AccountInfo,
	message string, format map[string]any, newOut func() any, taskID *string, opts askOptions) (any, bool, string, error) {
	out := newOut()
	failure, err := s.askAgent(ctx, caller, message, format, out, taskID, opts.thinking)
	if cutOff(failure, err) && !opts.retryCutOff {
		return out, false, failureOutputTooLong, err
	}
	retried := failure == failureOutputInvalid ||
		failure == failureAgentFailed && (strings.Contains(err.Error(), structuredOutputFailed) || cutOff(failure, err))
	if retried {
		logger.Warnf("Requirement run of project %s asks the Agent again (%s): %v", projectID, failure, err)
		out = newOut()
		failure, err = s.askAgent(ctx, caller, message, format, out, taskID, opts.thinking)
	}
	if cutOff(failure, err) {
		failure = failureOutputTooLong
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
	// parts, of a run that reads its documents in parts, are their keys: the Agent is asked once
	// per part, all side by side, and a part that fails leaves what the others found. A run without
	// parts asks once, as part 0.
	parts []string
	// halves, of a run in parts, tells whether a part can be split in two halves: a part whose
	// answer was cut off at the model's output cap is then asked for half by half, once. Nil for a
	// run that cannot.
	halves func(part int) bool
	// prepareMessage builds the message of a part, in the background: it may read what is slow to
	// read. half is 0 for the whole part, 1 or 2 for its first or second half.
	prepareMessage func(ctx context.Context, part int, half int) (string, error)
	// check checks the answer of a part, outside the write: it may write what the specification
	// does not hold. Parts are checked one at a time, in order.
	check func(ctx context.Context, part int, out any)
	// write writes what check kept into the specification as stored.
	write func(req *interfaces.Requirement)
	// inputs are what the run read, recorded once it is ready.
	inputs *interfaces.RequirementInputs
}

// partAnswer is what asking the Agent for one part came to.
type partAnswer struct {
	// outs are the answers that came: the part's, or its halves'.
	outs    []any
	retried bool
	failure string
	err     error
	taskID  string
}

// ask asks the Agent for every part of p, side by side when there are several: one after another
// they would outlast the time a run is given.
func (s *requirementService) ask(ctx context.Context, p *partRun) []partAnswer {
	answers := make([]partAnswer, max(len(p.parts), 1))
	// A run that can halve a part asks for less rather than the same again.
	opts := askOptions{retryCutOff: p.halves == nil, thinking: requirementThinkingMode}
	askHalf := func(a *partAnswer, i int, half int) {
		message, err := p.prepareMessage(ctx, i, half)
		if err != nil {
			a.failure, a.err = failureAgentUnavailable, err
			return
		}
		out, retried, failure, err := s.askTwice(ctx, p.projectID, p.caller, message, p.format, p.newOut, &a.taskID, opts)
		a.retried = a.retried || retried
		if failure != "" {
			a.failure, a.err = failure, err
			return
		}
		a.outs = append(a.outs, out)
	}
	askPart := func(i int) {
		a := &answers[i]
		askHalf(a, i, 0)
		if a.failure != failureOutputTooLong || p.halves == nil || !p.halves(i) {
			return
		}
		// Cut off at the model's output cap: each half asks for about half the answer.
		logger.Warnf("Requirement %s run of project %s reads part %d in halves: %v", p.kind, p.projectID, i+1, a.err)
		a.failure, a.err, a.retried = "", nil, true
		askHalf(a, i, 1)
		askHalf(a, i, 2)
	}
	if len(answers) == 1 {
		askPart(0)
		return answers
	}
	var wg sync.WaitGroup
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			askPart(i)
		}()
	}
	wg.Wait()
	return answers
}

// startRun records a run of kind going over the specification as read at readUpdateTime (0 when
// the project had none), refusing while the project drafts or another run goes.
func (s *requirementService) startRun(ctx context.Context, projectID string, kind string,
	caller interfaces.AccountInfo, previous *interfaces.Requirement) (*interfaces.RequirementRun, error) {
	now := s.now()
	run := &interfaces.RequirementRun{Status: interfaces.REQUIREMENT_RUN_RUNNING, RunID: s.newID(), By: caller,
		StartTime: now, UpdateTime: now}
	readUpdateTime := int64(0)
	if previous != nil {
		readUpdateTime = previous.UpdateTime
		// What the last run that ended ready read stays until another ends ready: a run that fails
		// leaves its part as it was, and that part was extracted from these inputs.
		if last := previous.Spec.Runs[kind]; last != nil {
			run.Inputs, run.PendingParts = last.Inputs, last.PendingParts
		}
	}
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
// leaving the specification as it was. A run in parts that some parts failed writes what the
// others found and records those it still has to read, for the next run to read only them, with
// why they failed as its error.
func (s *requirementService) execute(ctx context.Context, p *partRun) {
	var taskID string
	retried, failure, done := false, "", 0
	var err error
	pending := []string{}
	for i, a := range s.ask(ctx, p) {
		retried = retried || a.retried
		if a.taskID != "" {
			taskID = a.taskID
		}
		// What came is kept, of a part one half of which failed too; the part stays to read.
		for _, out := range a.outs {
			p.check(ctx, i, out)
			done++
		}
		if a.failure != "" {
			failure, err = a.failure, a.err
			if i < len(p.parts) {
				pending = append(pending, p.parts[i])
			}
			logger.Errorf("Requirement %s run of project %s failed on part %d of %d (%s, asked again: %t): %v",
				p.kind, p.projectID, i+1, max(len(p.parts), 1), a.failure, a.retried, a.err)
		}
	}
	lastFailure := failure
	if done > 0 {
		// What some parts found stands; the run says why the others failed.
		failure = ""
	}
	finished, err := s.ra.FinishRun(ctx, p.projectID, p.kind, p.run.RunID, func(req *interfaces.Requirement) {
		run := req.Spec.Runs[p.kind]
		// Only starting the run writes the specification while it goes. Changed since, the run outlived
		// its time and people edited what it read: it writes nothing over their edits.
		changed := req.UpdateTime != p.run.StartTime
		run.AgentTaskID, run.Retried, run.UpdateTime = taskID, retried, s.now()
		req.UpdateTime = run.UpdateTime
		if changed && failure == "" {
			failure = failureInterrupted
			logger.Warnf("Requirement %s run of project %s ended after the specification changed; it writes nothing", p.kind, p.projectID)
		}
		if failure != "" {
			// The parts still to read stay those of the last run that wrote.
			run.Status, run.Error = interfaces.REQUIREMENT_RUN_FAILED, failure
			return
		}
		p.write(req)
		run.Status, run.Error, run.Inputs, run.PendingParts = interfaces.REQUIREMENT_RUN_READY, "", p.inputs, nil
		if len(pending) > 0 {
			// Why the parts still to read failed, for the client to say what to do.
			run.Error, run.PendingParts = lastFailure, pending
		}
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
