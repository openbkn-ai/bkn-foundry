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
)

// Outcomes of a question.
const (
	REGRESSION_OUTCOME_PASSED = "passed"
	// REGRESSION_OUTCOME_MISSING: the release lacks something the question needs.
	REGRESSION_OUTCOME_MISSING = "missing"
	// REGRESSION_OUTCOME_NO_ANSWER: the question was answered with nothing.
	REGRESSION_OUTCOME_NO_ANSWER = "no_answer"
	// REGRESSION_OUTCOME_ERROR: the question could not be asked; ErrorCode says why.
	REGRESSION_OUTCOME_ERROR = "error"
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

type RegressionQuestion struct {
	ID    string               `json:"id"`
	Text  string               `json:"text"`
	Needs []RequirementItemRef `json:"needs"`
}

// RegressionResult is how one question did. Missing lists what the release lacks of what it needs;
// ErrorCode is the answering service's code when it could not be asked. Answer and ToolUses are
// what an asked question was answered with and the tool calls behind it, kept whatever the outcome
// so the answer can be judged again later.
type RegressionResult struct {
	QuestionID string            `json:"question_id"`
	Outcome    string            `json:"outcome"`
	Missing    []ReleaseItem     `json:"missing,omitempty"`
	Answer     string            `json:"answer,omitempty"`
	ToolUses   []BknAgentToolUse `json:"tool_uses,omitempty"`
	ErrorCode  string            `json:"error_code,omitempty"`
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
	// REGRESSION_CODE_CANCELLED: the answer was cancelled before it ended.
	REGRESSION_CODE_CANCELLED = "cancelled"
	// REGRESSION_CODE_TOOLS_UNAVAILABLE: no call of the answering Agent reached the network.
	REGRESSION_CODE_TOOLS_UNAVAILABLE = "tools_unavailable"
)

type RegressionService interface {
	// StartRegression starts the regression of release in the background, answering as runner with
	// token, and returns it; false when one of the release is running already. A project without
	// questions of the knowledge layer gets a regression with no questions, finished at once.
	StartRegression(ctx context.Context, token string, release *Release, runner AccountInfo) (*Regression, bool, error)
	// FailInterrupted fails the regressions a previous run of the service left running.
	FailInterrupted(ctx context.Context) error
}
