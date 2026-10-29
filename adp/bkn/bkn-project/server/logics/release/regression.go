// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	// answerAgentID is the bkn-agent chat Agent that answers a question on a knowledge network,
	// through the network's MCP tools.
	answerAgentID = "studio_smart_qa"
	// regressionStaleAfter is how long a regression may run before another may take it over; the
	// questions are answered one by one, each in at most bkn-agent's five minutes.
	regressionStaleAfter = 6 * time.Hour
)

type regressionService struct {
	ra interfaces.RegressionAccess
	// rs reads the scenario questions; without it a release has none.
	rs interfaces.RequirementService
	ba interfaces.BknAgentAccess
	gs interfaces.GitStore
	// run answers in the background; tests replace it to run in line.
	run func(fn func())
}

func (s *regressionService) StartRegression(ctx context.Context, token string, release *interfaces.Release,
	runner interfaces.AccountInfo) (*interfaces.Regression, bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "StartRegression")
	defer span.End()

	questions, err := s.knowledgeQuestions(ctx, release.ProjectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get scenario questions failed")
		return nil, false, err
	}
	runID, err := uuid.NewV7()
	if err != nil {
		span.SetStatus(codes.Error, "New run failed")
		return nil, false, err
	}
	now := time.Now()
	r := &interfaces.Regression{
		NetworkID: release.NetworkID, ProjectID: release.ProjectID, Version: release.Version,
		Status: interfaces.REGRESSION_STATUS_RUNNING, RunID: runID.String(), Attempt: 1, Runner: runner,
		Questions: questions, Results: []interfaces.RegressionResult{}, StartTime: now.UnixMilli(),
	}
	if len(questions) == 0 {
		r.Status, r.FinishTime = interfaces.REGRESSION_STATUS_NO_QUESTIONS, r.StartTime
	}
	started, err := s.ra.StartRegression(ctx, r, now.Add(-regressionStaleAfter).UnixMilli())
	if err != nil {
		span.SetStatus(codes.Error, "Start regression failed")
		return nil, false, err
	}
	if !started || r.Status != interfaces.REGRESSION_STATUS_RUNNING {
		span.SetStatus(codes.Ok, "")
		return r, started, nil
	}
	background, running := context.WithoutCancel(ctx), *r
	s.run(func() { s.answer(background, token, release, &running) })
	span.SetStatus(codes.Ok, "")
	return r, true, nil
}

func (s *regressionService) FailInterrupted(ctx context.Context) error {
	n, err := s.ra.FailRunning(ctx, interfaces.REGRESSION_ERROR_INTERRUPTED, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if n > 0 {
		otellog.LogWarn(ctx, fmt.Sprintf("Failed %d regressions left running by a previous run", n))
	}
	return nil
}

// knowledgeQuestions are the project's scenario questions answered from object types and relation
// types, as the specification has them now.
func (s *regressionService) knowledgeQuestions(ctx context.Context, projectID string) ([]interfaces.RegressionQuestion, error) {
	questions := []interfaces.RegressionQuestion{}
	if s.rs == nil {
		return questions, nil
	}
	sq, err := s.rs.ScenarioQuestions(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, q := range sq.Questions {
		if q.Layer == interfaces.REQUIREMENT_LAYER_KNOWLEDGE {
			needs := append([]interfaces.RequirementItemRef{}, q.Needs...)
			questions = append(questions, interfaces.RegressionQuestion{ID: q.ID, Text: q.Text, Needs: needs})
		}
	}
	return questions, nil
}

// answer judges each question against the release's definitions and, when it has all it needs,
// against the answer the network gives; the questions that could not be asked are asked once more.
// It records the outcome as the run that started.
func (s *regressionService) answer(ctx context.Context, token string, release *interfaces.Release,
	r *interfaces.Regression) {
	defer func() {
		r.FinishTime = time.Now().UnixMilli()
		if ok, err := s.ra.FinishRegression(ctx, r); err != nil || !ok {
			otellog.LogWarn(ctx, fmt.Sprintf("Record regression of %s: recorded %v, %v", r.NetworkID, ok, err))
		}
	}()
	files, err := s.gs.ReadTree(ctx, release.LineageID, "refs/tags/v"+release.Version)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read definitions of release %s: %v", release.NetworkID, err))
		r.Status, r.Error = interfaces.REGRESSION_STATUS_FAILED, interfaces.REGRESSION_ERROR_DEFINITIONS_UNREADABLE
		return
	}
	defined := map[interfaces.ReleaseItem]bool{}
	for p := range files {
		if kindOf(p) != "" {
			it := itemAt(p, files)
			defined[interfaces.ReleaseItem{Kind: it.Kind, ID: it.ID}] = true
		}
	}

	r.Results = make([]interfaces.RegressionResult, len(r.Questions))
	for i, q := range r.Questions {
		r.Results[i] = s.judge(ctx, token, release.NetworkID, r.RunID, i, 1, q, defined)
	}
	for i, q := range r.Questions {
		if r.Results[i].Outcome == interfaces.REGRESSION_OUTCOME_ERROR {
			r.Attempt = 2
			r.Results[i] = s.judge(ctx, token, release.NetworkID, r.RunID, i, 2, q, defined)
		}
	}
	r.Status = regressionStatus(r.Results)
}

// judge decides one question: missing when the release lacks something it needs, and otherwise by
// the answer the network gives. A question that could not be asked is an error, not the ontology's.
func (s *regressionService) judge(ctx context.Context, token string, networkID string, runID string, index int,
	attempt int, q interfaces.RegressionQuestion, defined map[interfaces.ReleaseItem]bool) interfaces.RegressionResult {
	result := interfaces.RegressionResult{QuestionID: q.ID}
	for _, n := range q.Needs {
		if !defined[interfaces.ReleaseItem{Kind: n.Kind, ID: n.ID}] {
			result.Missing = append(result.Missing, interfaces.ReleaseItem{Kind: n.Kind, ID: n.ID, Name: n.ID})
		}
	}
	if len(result.Missing) > 0 {
		result.Outcome = interfaces.REGRESSION_OUTCOME_MISSING
		return result
	}
	answer, err := s.ba.AnswerOnNetwork(ctx, token, &interfaces.BknAgentAnswerRequest{
		AgentID: answerAgentID, Message: q.Text, ClientRequestID: clientRequestID(runID, index, attempt), KNID: networkID,
	})
	var agentErr *interfaces.BknAgentError
	switch {
	case errors.As(err, &agentErr):
		result.Outcome, result.ErrorCode = interfaces.REGRESSION_OUTCOME_ERROR, agentErr.Code
	case err != nil:
		otellog.LogWarn(ctx, fmt.Sprintf("Answer question %s on %s: %v", q.ID, networkID, err))
		result.Outcome, result.ErrorCode = interfaces.REGRESSION_OUTCOME_ERROR, interfaces.REGRESSION_CODE_UNREACHABLE
	case answer.Status == interfaces.BKN_AGENT_ANSWER_CANCELLED:
		result.Outcome, result.ErrorCode = interfaces.REGRESSION_OUTCOME_ERROR, interfaces.REGRESSION_CODE_CANCELLED
	case answer.Status != interfaces.BKN_AGENT_ANSWER_SUCCEEDED:
		result.Outcome, result.ErrorCode = interfaces.REGRESSION_OUTCOME_ERROR, answer.ErrorCode
	case answer.ToolCalls > 0 && answer.ToolsUnavailable == answer.ToolCalls:
		// The Agent never reached the network: what it said is not about the ontology.
		result.Outcome, result.ErrorCode = interfaces.REGRESSION_OUTCOME_ERROR, interfaces.REGRESSION_CODE_TOOLS_UNAVAILABLE
	case strings.TrimSpace(answer.Answer) == "":
		result.Outcome = interfaces.REGRESSION_OUTCOME_NO_ANSWER
	default:
		result.Outcome, result.Answer = interfaces.REGRESSION_OUTCOME_PASSED, answer.Answer
	}
	return result
}

// regressionStatus is failed while a question could not be asked; not passed when one lacks what
// it needs or got no answer; passed otherwise.
func regressionStatus(results []interfaces.RegressionResult) string {
	status := interfaces.REGRESSION_STATUS_PASSED
	for _, r := range results {
		switch r.Outcome {
		case interfaces.REGRESSION_OUTCOME_ERROR:
			return interfaces.REGRESSION_STATUS_FAILED
		case interfaces.REGRESSION_OUTCOME_MISSING, interfaces.REGRESSION_OUTCOME_NO_ANSWER:
			status = interfaces.REGRESSION_STATUS_NOT_PASSED
		}
	}
	return status
}

// clientRequestID names one ask of one question in a run, in bkn-agent's form for request ids:
// letters, digits, "_", "." and "-", at most 100. Asking again gets a new id, so bkn-agent answers
// anew instead of replaying the failed answer.
func clientRequestID(runID string, index int, attempt int) string {
	return fmt.Sprintf("%s.%d.%d", runID, index, attempt)
}
