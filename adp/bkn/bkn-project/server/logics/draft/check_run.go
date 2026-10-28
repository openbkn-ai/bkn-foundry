// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// checkAttempts is how many times a run counts: a failed attempt is retried once, counting only
// what failed.
const checkAttempts = 2

// checkRun is a check running in this process; checks only run in the process that started them.
type checkRun struct {
	runID  string
	cancel context.CancelFunc
}

// checkStaleAfter is how long a run may take: its attempts, with a minute to record it. A check
// running longer has ended without being recorded.
func (s *draftService) checkStaleAfter() time.Duration {
	return checkAttempts*s.setting.CheckTimeout + time.Minute
}

// startCheck checks a confirmation's commit in the background, counting as the runner. A commit
// already checked is not checked again unless its check failed. It reports nothing: a check that
// does not start leaves the confirmation as it is, and is shown as not started.
func (s *draftService) startCheck(ctx context.Context, projectID string, commit string, input *checkInput,
	runner interfaces.AccountInfo) {
	existing, exist, err := s.ca.GetCheck(ctx, projectID)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read check of project %s: %v", projectID, err))
		return
	}
	if exist && existing.Commit == commit &&
		(existing.Status == interfaces.CHECK_STATUS_PASSED || existing.Status == interfaces.CHECK_STATUS_ISSUES) {
		return
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Write check input of project %s: %v", projectID, err))
		return
	}
	runID, err := uuid.NewV7()
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("New check run of project %s: %v", projectID, err))
		return
	}
	now := time.Now()
	check := &interfaces.Check{
		ProjectID: projectID, Commit: commit, Status: interfaces.CHECK_STATUS_RUNNING, RunID: runID.String(),
		Attempt: 1, Runner: runner, Input: inputJSON, StartTime: now.UnixMilli(),
	}
	started, err := s.ca.StartCheck(ctx, check, now.Add(-s.checkStaleAfter()).UnixMilli())
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Start check of project %s: %v", projectID, err))
		return
	}
	if !started {
		return
	}
	// The run outlives the request; it keeps the trace but not the request's cancellation.
	// A later run of the project stops it, so replaced runs do not keep counting on the source.
	background, cancel := context.WithCancel(context.WithoutCancel(ctx))
	run := &checkRun{runID: check.RunID, cancel: cancel}
	if previous, replaced := s.checkRuns.Swap(projectID, run); replaced {
		previous.(*checkRun).cancel()
	}
	counter := &vegaCounter{va: s.va, account: runner, timeoutSeconds: s.setting.QueryTimeout}
	s.run(func() {
		defer func() {
			cancel()
			s.checkRuns.CompareAndDelete(projectID, run)
		}()
		s.check(background, check, input, counter)
	})
}

// check runs a started check and records its outcome. Each attempt has the check timeout; a
// failed one is retried once, keeping what it counted.
func (s *draftService) check(ctx context.Context, check *interfaces.Check, input *checkInput, c counter) {
	var results *checkResults
	for attempt := 1; attempt <= checkAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, s.setting.CheckTimeout)
		results = runCheck(attemptCtx, c, input, results)
		cancel()
		check.Attempt = attempt
		// A run a later one replaced is not retried; its outcome is not recorded anyway.
		if checkStatus(results) != interfaces.CHECK_STATUS_FAILED || ctx.Err() != nil {
			break
		}
	}
	check.Status = checkStatus(results)
	check.FinishTime = time.Now().UnixMilli()
	resultsJSON, err := json.Marshal(results)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Write check results of project %s: %v", check.ProjectID, err))
		return
	}
	check.Results = resultsJSON
	written, err := s.ca.FinishCheck(context.WithoutCancel(ctx), check)
	switch {
	case err != nil:
		// Left running, it is taken over once stale, or failed as interrupted on the next start.
		otellog.LogWarn(ctx, fmt.Sprintf("Record check of project %s: %v", check.ProjectID, err))
	case !written:
		otellog.LogInfo(ctx, fmt.Sprintf("Check run %s of project %s was replaced", check.RunID, check.ProjectID))
	}
}
