// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// A regression answers, after a project publishes a release, the project's scenario questions of
// the knowledge layer on the network the release created, and judges each by rules: whether the
// release has everything the question needs, and whether an answer came back. It never blocks or
// undoes the release. Codes are for machines; clients word them.

// Regression states.
const (
	REGRESSION_STATUS_RUNNING = "running"
	// REGRESSION_STATUS_PASSED: every question was answered and the release had all it needs.
	REGRESSION_STATUS_PASSED = "passed"
	// REGRESSION_STATUS_NOT_PASSED: some question lacks what it needs, or got no answer.
	REGRESSION_STATUS_NOT_PASSED = "not_passed"
	// REGRESSION_STATUS_FAILED: some question could not be asked, such as the answering Agent
	// being unavailable; that is not the ontology's fault.
	REGRESSION_STATUS_FAILED = "failed"
	// REGRESSION_STATUS_NO_QUESTIONS: the project had no questions to answer.
	REGRESSION_STATUS_NO_QUESTIONS = "no_questions"
	// REGRESSION_STATUS_NEEDS_VALUES: every question judged lacked a specific value to ask about,
	// so the regression shows nothing of what the ontology can answer.
	REGRESSION_STATUS_NEEDS_VALUES = "needs_values"
)

// Outcomes of a question.
const (
	REGRESSION_OUTCOME_PASSED = "passed"
	// REGRESSION_OUTCOME_MISSING: the release lacks something the question needs.
	REGRESSION_OUTCOME_MISSING = "missing"
	// REGRESSION_OUTCOME_NO_ANSWER: the question was answered with nothing.
	REGRESSION_OUTCOME_NO_ANSWER = "no_answer"
	// REGRESSION_OUTCOME_NEEDS_VALUES: the question is a template lacking a specific value, and the
	// answer asked for it; that is not the ontology's fault.
	REGRESSION_OUTCOME_NEEDS_VALUES = "needs_values"
	// REGRESSION_OUTCOME_NOT_ANSWERED: the answer could not answer the question, or was about something
	// else; Reason says why.
	REGRESSION_OUTCOME_NOT_ANSWERED = "not_answered"
	// REGRESSION_OUTCOME_ERROR: the question could not be asked; ErrorCode says why.
	REGRESSION_OUTCOME_ERROR = "error"
)

// What the answer judge said of an answer.
const (
	REGRESSION_VERDICT_ANSWERED             = "answered"
	REGRESSION_VERDICT_CLARIFICATION_NEEDED = "clarification_needed"
	REGRESSION_VERDICT_CANNOT_ANSWER        = "cannot_answer"
	REGRESSION_VERDICT_OFF_TOPIC            = "off_topic"
)

// Regression is the regression of one release, kept in t_bkn_project_regression.
type Regression struct {
	// NetworkID is the network the release created.
	NetworkID string `json:"network_id"`
	ProjectID string `json:"project_id"`
	Version   string `json:"version"`
	Status    string `json:"status"`
	// RunID tells runs apart: only the run that started a regression records its outcome.
	RunID string `json:"-"`
	// Attempt counts the attempts of this run, the automatic retry included.
	Attempt int         `json:"attempt"`
	Runner  AccountInfo `json:"runner"`
	// Questions are the questions answered, as the specification had them.
	Questions []RegressionQuestion `json:"questions"`
	Results   []RegressionResult   `json:"results"`
	// Error is why the run failed, as a code.
	Error      string `json:"error"`
	StartTime  int64  `json:"start_time"`
	FinishTime int64  `json:"finish_time"`
}

// RegressionQuestion is a question answered. When it has Tests, the tests are asked in its place,
// each with specific values; otherwise the question itself is.
type RegressionQuestion struct {
	ID    string               `json:"id"`
	Text  string               `json:"text"`
	Needs []RequirementItemRef `json:"needs"`
	Tests []RegressionTest     `json:"tests,omitempty"`
}

// RegressionTest is a test question asked in its question's place, with what it expects of the
// data: exists, not_exists or same_record.
type RegressionTest struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Expect string `json:"expect,omitempty"`
}

// RegressionResult is how one question, or one of its tests, did. Missing lists what the release
// lacks of what it needs; ErrorCode is the answering service's code when it could not be asked.
// Answer and ToolUses are what an asked question was answered with and the tool calls behind it,
// kept whatever the outcome. Verdict, Reason and UsedNeeds are what the answer judge said of the
// answer: the verdict the outcome follows, why, and which needs the answer used.
type RegressionResult struct {
	QuestionID string               `json:"question_id"`
	TestID     string               `json:"test_id,omitempty"`
	Outcome    string               `json:"outcome"`
	Verdict    string               `json:"verdict,omitempty"`
	Reason     string               `json:"reason,omitempty"`
	UsedNeeds  []RequirementItemRef `json:"used_needs,omitempty"`
	Missing    []ReleaseItem        `json:"missing,omitempty"`
	Answer     string               `json:"answer,omitempty"`
	ToolUses   []BknAgentToolUse    `json:"tool_uses,omitempty"`
	ErrorCode  string               `json:"error_code,omitempty"`
}

//go:generate mockgen -source ../interfaces/regression.go -destination ../interfaces/mock/mock_regression.go
type RegressionAccess interface {
	// GetRegression returns the regression of the release that created networkID, and false when
	// it has none.
	GetRegression(ctx context.Context, networkID string) (*Regression, bool, error)
	// StartRegression writes r, running, as its release's regression unless one is running already;
	// it reports whether it did. One running since before staleBefore is taken over: it has ended
	// without being recorded.
	StartRegression(ctx context.Context, r *Regression, staleBefore int64) (bool, error)
	// FinishRegression records how a run ended while it is still its release's running regression;
	// it reports whether it did, and false means a later run took it over.
	FinishRegression(ctx context.Context, r *Regression) (bool, error)
	// FailRunning marks every running regression as failed with reason; regressions run in the
	// service process, so ones left running after a restart will never finish.
	FailRunning(ctx context.Context, reason string, finishTime int64) (int64, error)
}

// Why a regression run failed as a whole.
const (
	// REGRESSION_ERROR_INTERRUPTED: the service stopped while it ran.
	REGRESSION_ERROR_INTERRUPTED = "interrupted"
	// REGRESSION_ERROR_DEFINITIONS_UNREADABLE: the release's definitions could not be read.
	REGRESSION_ERROR_DEFINITIONS_UNREADABLE = "definitions_unreadable"
)

// Codes of a question that could not be asked, besides the answering service's own.
const (
	// REGRESSION_CODE_UNREACHABLE: the answering service could not be reached.
	REGRESSION_CODE_UNREACHABLE = "unreachable"
	// REGRESSION_CODE_ANSWER_INTERRUPTED: the answering service was reached, but its answer stopped
	// before it ended or could not be read.
	REGRESSION_CODE_ANSWER_INTERRUPTED = "answer_interrupted"
	// REGRESSION_CODE_CANCELLED: the answer was cancelled before it ended.
	REGRESSION_CODE_CANCELLED = "cancelled"
	// REGRESSION_CODE_TOOLS_UNAVAILABLE: no call of the answering Agent reached the network.
	REGRESSION_CODE_TOOLS_UNAVAILABLE = "tools_unavailable"
	// REGRESSION_CODE_JUDGE_UNAVAILABLE: the answer judge could not be run, failed or timed out.
	REGRESSION_CODE_JUDGE_UNAVAILABLE = "judge_unavailable"
	// REGRESSION_CODE_JUDGE_INVALID: the answer judge returned what its schema does not allow, or a
	// need it was not given.
	REGRESSION_CODE_JUDGE_INVALID = "judge_invalid"
)

type RegressionService interface {
	// StartRegression starts the regression of release in the background, answering as runner with
	// token, and returns it; false when one of the release is running already. A project without
	// questions of the knowledge layer gets a regression with no questions, finished at once.
	StartRegression(ctx context.Context, token string, release *Release, runner AccountInfo) (*Regression, bool, error)
	// FailInterrupted fails the regressions a previous run of the service left running.
	FailInterrupted(ctx context.Context) error
}
