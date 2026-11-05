// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"errors"
	"net"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	maxFileAttempts = 3
	fileRetryDelay  = 30 * time.Second
	maxSubmitHold   = 10 * time.Minute

	reasonSubmissionRefused = "submission_refused"
	reasonSourceVersionGone = "source_version_gone"
	reasonSourceUnavailable = "source_unavailable"
	// ReasonNoContentVersion marks a selected file that has nothing to extract.
	ReasonNoContentVersion = "no_content_version"
)

// retryableReason lists failures of a known execution: another attempt cannot
// duplicate an unknown task, and may succeed. A lost submission is not here.
func retryableReason(reason string) bool {
	return reason == "extraction_failed" || reason == ReasonExtractionTimedOut || reason == "invalid_extraction"
}

func fileBackoff(attempts int) int64 {
	return (fileRetryDelay << max(attempts, 0)).Milliseconds()
}

// resetFileExecution forgets one attempt. The content digest stays, so a retry
// that reads different content is still refused.
func resetFileExecution(file *interfaces.KBFileBuild) {
	file.State, file.Reason = "pending", ""
	file.AgentTaskID, file.AgentVersion, file.PromptVersion, file.ModelID, file.Model = "", nil, nil, "", ""
	file.RowsDigest, file.RowsWritten, file.SubmittedAt, file.DurationMillis = "", 0, 0, nil
	file.NextAttempt, file.ReusedFrom = 0, ""
}

// outstandingFile reports a file that ended without a usable result. The build
// cannot be released until a person accepts leaving such files out.
func outstandingFile(file *interfaces.KBFileBuild) bool {
	return !file.Completed()
}

// submissionRefused reports a submission that definitely created no task: the
// runtime said so, the connection never opened, or the Agent service answered
// with a refusal. Anything else stays uncertain and is never submitted again.
func submissionRefused(err error) bool {
	if errors.Is(err, kb.ErrNotSubmitted) {
		return true
	}
	var refusal *interfaces.BknAgentError
	if errors.As(err, &refusal) {
		return refusal.HTTPCode >= 400 && refusal.HTTPCode < 500
	}
	var dial *net.OpError
	return errors.As(err, &dial) && dial.Op == "dial"
}

// sourceFileGone reports that this one file's version cannot be read any more.
// It says nothing about the person's access or about the other files.
func sourceFileGone(err error) bool {
	if errors.Is(err, kb.ErrVersionGone) {
		return true
	}
	var source *kb.SourceError
	return errors.As(err, &source) && (source.StatusCode == 404 || source.StatusCode == 409 || source.StatusCode == 410)
}

// holdSubmissions spaces out submissions while the Agent service is failing, so
// an outage does not spend every remaining file in a few seconds.
func holdSubmissions(build *interfaces.KBBuild, now int64) {
	delay := fileRetryDelay << min(build.SubmitFailures, 8)
	build.SubmitFailures++
	build.SubmitHoldUntil = now + min(delay, maxSubmitHold).Milliseconds()
}
