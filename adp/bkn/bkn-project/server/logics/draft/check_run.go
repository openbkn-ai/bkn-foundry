// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
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
		(existing.Status == interfaces.CHECK_STATUS_PASSED || existing.Status == interfaces.CHECK_STATUS_ISSUES ||
			existing.Status == interfaces.CHECK_STATUS_PARTIAL) {
		return
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Write check input of project %s: %v", projectID, err))
		return
	}
	if _, err := s.beginCheck(ctx, projectID, commit, inputJSON, runner); err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Start check of project %s: %v", projectID, err))
	}
}

// beginCheck starts a run of the commit over input unless that commit is being checked already,
// and returns the check, or nil when it did not start.
func (s *draftService) beginCheck(ctx context.Context, projectID string, commit string, inputJSON json.RawMessage,
	runner interfaces.AccountInfo) (*interfaces.Check, error) {
	var input checkInput
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return nil, fmt.Errorf("read check input: %w", err)
	}
	runID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("new check run: %w", err)
	}
	now := time.Now()
	check := &interfaces.Check{
		ProjectID: projectID, Commit: commit, Status: interfaces.CHECK_STATUS_RUNNING, RunID: runID.String(),
		Attempt: 1, Runner: runner, Input: inputJSON, StartTime: now.UnixMilli(),
	}
	started, err := s.ca.StartCheck(ctx, check, now.Add(-s.checkStaleAfter()).UnixMilli())
	if err != nil || !started {
		return nil, err
	}
	// The run outlives the request; it keeps the trace but not the request's cancellation.
	// A later run of the project stops it, so replaced runs do not keep counting on the source.
	background, cancel := context.WithCancel(context.WithoutCancel(ctx))
	run := &checkRun{runID: check.RunID, cancel: cancel}
	if previous, replaced := s.checkRuns.Swap(projectID, run); replaced {
		previous.(*checkRun).cancel()
	}
	counter := &vegaCounter{va: s.va, account: runner, timeoutSeconds: s.setting.QueryTimeout}
	// The reply is the check as started; the run goes on writing its own.
	reply := *check
	s.run(func() {
		defer func() {
			cancel()
			s.checkRuns.CompareAndDelete(projectID, run)
		}()
		s.check(background, check, &input, counter)
	})
	return &reply, nil
}

// GetCheck returns the check of the project's last confirmation to whoever can see the project.
func (s *draftService) GetCheck(ctx context.Context, token string, projectID string) (*interfaces.Check, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetCheck")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	check, exist, err := s.ca.GetCheck(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get check failed")
		return nil, internalError(ctx, "Get check failed", err)
	}
	if !exist {
		span.SetStatus(codes.Error, "Check not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Check_NotFound)
	}
	span.SetStatus(codes.Ok, "")
	return check, nil
}

// RunCheck checks the last confirmation again, all of it, counting as the owner. What was
// confirmed is read from the draft while it is still confirmed, and otherwise from the last
// check of that commit.
func (s *draftService) RunCheck(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.Check, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "RunCheck")
	defer span.End()

	entry, err := s.unarchivedProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	if entry.Owner.ID != caller.ID {
		span.SetStatus(codes.Error, "Not the owner")
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Check_NotOwner)
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist || draft.Items.Confirmation == nil || draft.Items.Confirmation.Commit == "" {
		span.SetStatus(codes.Error, "Nothing confirmed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Check_NothingConfirmed)
	}
	confirmation := draft.Items.Confirmation
	existing, checked, err := s.ca.GetCheck(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get check failed")
		return nil, internalError(ctx, "Get check failed", err)
	}
	var inputJSON json.RawMessage
	switch {
	case draft.Status == interfaces.DRAFT_STATUS_READY && confirmation.UpdateTime == draft.UpdateTime:
		if inputJSON, err = json.Marshal(checkInputOf(&draft.Items)); err != nil {
			span.SetStatus(codes.Error, "Write check input failed")
			return nil, internalError(ctx, "Write check input failed", err)
		}
	case checked && existing.Commit == confirmation.Commit && len(existing.Input) > 0:
		inputJSON = existing.Input
	default:
		// Changed since, and never checked: what was confirmed is on the branch only.
		span.SetStatus(codes.Error, "Confirmation unknown")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Check_ConfirmAgain)
	}
	check, err := s.beginCheck(ctx, projectID, confirmation.Commit, inputJSON, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Start check failed")
		return nil, internalError(ctx, "Start check failed", err)
	}
	if check == nil {
		span.SetStatus(codes.Error, "Check running")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Check_Running)
	}
	span.SetStatus(codes.Ok, "")
	return check, nil
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
