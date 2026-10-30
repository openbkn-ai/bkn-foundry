// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/requirement"
)

const (
	// answerAgentID is the bkn-agent chat Agent that answers a question on a knowledge network,
	// through the network's MCP tools.
	answerAgentID = "studio_smart_qa"
	// regressionStaleAfter is how long a regression may run before another may take it over; the
	// questions are answered one by one, each in at most bkn-agent's five minutes.
	regressionStaleAfter = 6 * time.Hour
)

var (
	rgsOnce sync.Once
	rgs     interfaces.RegressionService
)

// NewRegressionService answers with the service's regression store, bkn-agent and git store, and
// reads the questions from the requirement specification.
func NewRegressionService() interfaces.RegressionService {
	rgsOnce.Do(func() {
		rgs = &regressionService{
			ra:    logics.RGA,
			rs:    requirement.NewRequirementService(),
			ba:    logics.BAA,
			gs:    logics.GS,
			da:    logics.DA,
			pa:    logics.PA,
			run:   func(fn func()) { go fn() },
			sleep: time.Sleep,
			now:   time.Now,
		}
	})
	return rgs
}

type regressionService struct {
	ra interfaces.RegressionAccess
	// rs reads the scenario questions; without it a release has none.
	rs interfaces.RequirementService
	ba interfaces.BknAgentAccess
	gs interfaces.GitStore
	// da names what a release lacks as the project's draft does; pa gives the language of reasons.
	da interfaces.DraftAccess
	pa interfaces.ProjectAccess
	// run answers in the background; tests replace it to run in line. sleep and now pace the
	// waiting for a judgement.
	run   func(fn func())
	sleep func(time.Duration)
	now   func() time.Time
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
			// What it rests on in the draft counts as needed: the release lacking it is missing.
			needs := append(append([]interfaces.RequirementItemRef{}, q.Needs...), q.Path...)
			var tests []interfaces.RegressionTest
			for _, t := range q.Tests {
				tests = append(tests, interfaces.RegressionTest{ID: t.ID, Text: t.Text, Expect: t.Expect})
			}
			questions = append(questions, interfaces.RegressionQuestion{ID: q.ID, Text: q.Text, Needs: needs, Tests: tests})
		}
	}
	return questions, nil
}

// regressionAsk is one question to ask: a question, or one of its tests in its place.
type regressionAsk struct {
	question interfaces.RegressionQuestion
	testID   string
	text     string
	// expect is what a test expects of the data; empty for a question asked itself.
	expect string
}

// regressionAsks lists what a run asks: each question's tests when it has them, else the question.
func regressionAsks(questions []interfaces.RegressionQuestion) []regressionAsk {
	asks := []regressionAsk{}
	for _, q := range questions {
		if len(q.Tests) == 0 {
			asks = append(asks, regressionAsk{question: q, text: q.Text})
			continue
		}
		for _, t := range q.Tests {
			asks = append(asks, regressionAsk{question: q, testID: t.ID, text: t.Text, expect: t.Expect})
		}
	}
	return asks
}

// regressionContext is what a run judges against: the release's items by name, the names the
// project's draft gives, and the language reasons are written in.
type regressionContext struct {
	defined  map[interfaces.ReleaseItem]string
	drafted  map[interfaces.ReleaseItem]string
	language string
}

// name names an item as the release does, else as the draft does, else by its id.
func (c *regressionContext) name(kind string, id string) interfaces.ReleaseItem {
	key := interfaces.ReleaseItem{Kind: kind, ID: id}
	if name, ok := c.defined[key]; ok {
		key.Name = name
	} else if name, ok := c.drafted[key]; ok && name != "" {
		key.Name = name
	} else {
		key.Name = id
	}
	return key
}

// answer asks each question, or its tests, on the release's network, has the judge decide each
// answer, and asks once more what could not be asked; a judgement that failed is made once more
// from the answer kept. It records the outcome as the run that started.
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
	rc := s.regressionContext(ctx, release.ProjectID, files)

	asks := regressionAsks(r.Questions)
	r.Results = make([]interfaces.RegressionResult, len(asks))
	for i, a := range asks {
		r.Results[i] = s.judge(ctx, token, release.NetworkID, r.RunID, i, 1, a, r.Runner, rc)
	}
	for i, a := range asks {
		result := r.Results[i]
		if result.Outcome != interfaces.REGRESSION_OUTCOME_ERROR {
			continue
		}
		r.Attempt = 2
		if result.ErrorCode == interfaces.REGRESSION_CODE_JUDGE_UNAVAILABLE ||
			result.ErrorCode == interfaces.REGRESSION_CODE_JUDGE_INVALID {
			r.Results[i] = s.decide(ctx, a, result, r.Runner, rc)
			continue
		}
		r.Results[i] = s.judge(ctx, token, release.NetworkID, r.RunID, i, 2, a, r.Runner, rc)
	}
	r.Status = regressionStatus(r.Results)
}

// regressionContext reads the names a run judges with. The draft and the project are read as they
// are now; a failure to read them leaves names to ids and reasons to the default language.
func (s *regressionService) regressionContext(ctx context.Context, projectID string,
	files map[string][]byte) *regressionContext {
	rc := &regressionContext{defined: map[interfaces.ReleaseItem]string{}, drafted: map[interfaces.ReleaseItem]string{},
		language: interfaces.NAMING_LANGUAGE_ZH_CN}
	for p := range files {
		if kindOf(p) != "" {
			it := itemAt(p, files)
			rc.defined[interfaces.ReleaseItem{Kind: it.Kind, ID: it.ID}] = it.Name
		}
	}
	if draft, ok, err := s.da.GetDraft(ctx, projectID); err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read draft of project %s: %v", projectID, err))
	} else if ok {
		for _, ot := range draft.Items.ObjectTypes {
			rc.drafted[interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: ot.ID}] = ot.Name
		}
		for _, rt := range draft.Items.RelationTypes {
			rc.drafted[interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_RELATION_TYPE, ID: rt.ID}] = rt.Name
		}
	}
	if project, ok, err := s.pa.GetProject(ctx, projectID); err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read project %s: %v", projectID, err))
	} else if ok && interfaces.IsNamingLanguage(project.NamingLanguage) {
		rc.language = project.NamingLanguage
	}
	return rc
}

// judge decides one ask: missing when the release lacks something its question needs, and
// otherwise by the answer the network gives and what the judge says of it. A question that could
// not be asked, or an answer that could not be judged, is an error, not the ontology's.
func (s *regressionService) judge(ctx context.Context, token string, networkID string, runID string, index int,
	attempt int, a regressionAsk, runner interfaces.AccountInfo, rc *regressionContext) interfaces.RegressionResult {
	result := interfaces.RegressionResult{QuestionID: a.question.ID, TestID: a.testID}
	for _, n := range a.question.Needs {
		if _, ok := rc.defined[interfaces.ReleaseItem{Kind: n.Kind, ID: n.ID}]; !ok {
			result.Missing = append(result.Missing, rc.name(n.Kind, n.ID))
		}
	}
	if len(result.Missing) > 0 {
		result.Outcome = interfaces.REGRESSION_OUTCOME_MISSING
		return result
	}
	answer, err := s.ba.AnswerOnNetwork(ctx, token, &interfaces.BknAgentAnswerRequest{
		AgentID: answerAgentID, Message: a.text, ClientRequestID: clientRequestID(runID, index, attempt), KNID: networkID,
	})
	var agentErr *interfaces.BknAgentError
	if errors.As(err, &agentErr) {
		result.Outcome, result.ErrorCode = interfaces.REGRESSION_OUTCOME_ERROR, agentErr.Code
		return result
	}
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Answer question %s on %s: %v", a.question.ID, networkID, err))
		result.Outcome, result.ErrorCode = interfaces.REGRESSION_OUTCOME_ERROR, interfaces.REGRESSION_CODE_UNREACHABLE
		if errors.Is(err, interfaces.ErrBknAgentAnswerCut) {
			result.ErrorCode = interfaces.REGRESSION_CODE_ANSWER_INTERRUPTED
		}
		return result
	}
	// What it was answered with is kept whatever the outcome, for the answer to be judged again.
	result.Answer, result.ToolUses = answer.Answer, answer.ToolUses
	switch {
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
		return s.decide(ctx, a, result, runner, rc)
	}
	return result
}

// decide has the judge say whether the answer kept in result answers what was asked, and takes its
// verdict: answered passes; a question lacking a specific value needs values, which is not the
// ontology's fault; an answer that cannot answer or is about something else is not answered. A
// judgement that cannot be had is an error, never a pass.
func (s *regressionService) decide(ctx context.Context, a regressionAsk, result interfaces.RegressionResult,
	runner interfaces.AccountInfo, rc *regressionContext) interfaces.RegressionResult {
	needs := make([]interfaces.ReleaseItem, 0, len(a.question.Needs))
	for _, n := range a.question.Needs {
		needs = append(needs, rc.name(n.Kind, n.ID))
	}
	result.ErrorCode, result.Verdict, result.Reason, result.UsedNeeds = "", "", "", nil
	out, code := s.judgeAnswer(ctx, runner, a.text, a.expect, result.Answer, result.ToolUses, needs, rc.language)
	if out == nil {
		result.Outcome, result.ErrorCode = interfaces.REGRESSION_OUTCOME_ERROR, code
		return result
	}
	result.Verdict, result.Reason, result.UsedNeeds = out.Verdict, out.Reason, out.UsedNeeds
	switch out.Verdict {
	case interfaces.REGRESSION_VERDICT_ANSWERED:
		result.Outcome = interfaces.REGRESSION_OUTCOME_PASSED
	case interfaces.REGRESSION_VERDICT_CLARIFICATION_NEEDED:
		result.Outcome = interfaces.REGRESSION_OUTCOME_NEEDS_VALUES
	default:
		result.Outcome = interfaces.REGRESSION_OUTCOME_NOT_ANSWERED
	}
	return result
}

// regressionStatus is failed while a question could not be asked or judged; not passed when one
// lacks what it needs, got no answer or was not answered; passed when one passed; and needs values
// when every question judged lacked a specific value, which shows nothing of what the ontology can
// answer.
func regressionStatus(results []interfaces.RegressionResult) string {
	failed, notPassed, passed := false, false, false
	for _, r := range results {
		switch r.Outcome {
		case interfaces.REGRESSION_OUTCOME_ERROR:
			failed = true
		case interfaces.REGRESSION_OUTCOME_MISSING, interfaces.REGRESSION_OUTCOME_NO_ANSWER,
			interfaces.REGRESSION_OUTCOME_NOT_ANSWERED:
			notPassed = true
		case interfaces.REGRESSION_OUTCOME_PASSED:
			passed = true
		}
	}
	switch {
	case failed:
		return interfaces.REGRESSION_STATUS_FAILED
	case notPassed:
		return interfaces.REGRESSION_STATUS_NOT_PASSED
	case passed:
		return interfaces.REGRESSION_STATUS_PASSED
	}
	return interfaces.REGRESSION_STATUS_NEEDS_VALUES
}

// clientRequestID names one ask of one question in a run, in bkn-agent's form for request ids:
// letters, digits, "_", "." and "-", at most 100. Asking again gets a new id, so bkn-agent answers
// anew instead of replaying the failed answer.
func clientRequestID(runID string, index int, attempt int) string {
	return fmt.Sprintf("%s.%d.%d", runID, index, attempt)
}
