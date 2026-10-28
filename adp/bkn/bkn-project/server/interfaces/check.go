// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "encoding/json"

// A check counts, in the background after a confirmation, what that confirmation wrote: whether
// identities are unique and filled, how relations reach their ends, and that no property maps an
// excluded field. Its results are hints; they do not undo the confirmation.

// Check states.
const (
	CHECK_STATUS_RUNNING = "running"
	CHECK_STATUS_PASSED  = "passed"
	CHECK_STATUS_ISSUES  = "issues"
	CHECK_STATUS_FAILED  = "failed"
)

// Check is the latest check of a project.
type Check struct {
	ProjectID string `json:"project_id"`
	// Commit is the confirmation checked.
	Commit string `json:"commit"`
	Status string `json:"status"`
	// RunID tells runs apart: only the run that started a check records its outcome.
	RunID string `json:"-"`
	// Attempt counts the attempts of this run, the automatic retry included.
	Attempt int         `json:"attempt"`
	Runner  AccountInfo `json:"runner"`
	// Input is what the confirmation wrote, as the check reads it, so a retry checks the same.
	Input json.RawMessage `json:"-"`
	// Results holds the result of each check item.
	Results json.RawMessage `json:"results"`
	// Error is why the run failed, as a code for the client to word.
	Error      string `json:"error"`
	StartTime  int64  `json:"start_time"`
	FinishTime int64  `json:"finish_time"`
}
