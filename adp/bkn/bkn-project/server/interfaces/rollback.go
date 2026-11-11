// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// A rollback switches the running version of the project's network back to an earlier version:
// at once for every consumer, without a new version and without touching the draft or the
// project's branch. The community edition goes back to the previous version only; the professional
// one to any earlier version still kept. A project rolls back only after reading the plan: the
// rollback refuses a plan that no longer stands. Codes are for machines; clients word them.

// RollbackVersion is a version of the project's network: its network version, the label it was
// published with, and the project release that published it, if a release of the lineage did.
type RollbackVersion struct {
	KNVersion int    `json:"kn_version"`
	Label     string `json:"label"`
	Version   string `json:"version,omitempty"`
}

// RollbackPlan is what rolling the project's network back to To would do. To is absent when there
// is no version to go back to. Questions place the project's scenario questions as To's
// definitions have what they need, against the running version. Refusals say why the rollback
// cannot go; none stands when it can. Signature stands for the plan; the rollback refuses another.
type RollbackPlan struct {
	ProjectID string           `json:"project_id"`
	From      RollbackVersion  `json:"from"`
	To        *RollbackVersion `json:"to,omitempty"`
	// FullHistory is false in the community edition, which rolls back to the previous version only.
	FullHistory bool             `json:"full_history"`
	Questions   ReleaseQuestions `json:"questions"`
	Refusals    []ReleaseRefusal `json:"refusals"`
	Signature   string           `json:"signature"`
}

// ProjectRollback is the body of a project's rollback: the signature of the plan read, and the
// network version it was read for; 0 is the previous version.
type ProjectRollback struct {
	Signature string `json:"signature"`
	ToVersion int    `json:"to_version"`
}

// RollbackResult is what a rollback did.
type RollbackResult struct {
	RunningVersion int                `json:"running_version"`
	Warnings       []KNVersionWarning `json:"warnings"`
}

// ACTION_REVIEW_ROLLBACK reads what rolling back the release in use would do; rolling back goes on
// from the plan.
const ACTION_REVIEW_ROLLBACK = "review_rollback"
