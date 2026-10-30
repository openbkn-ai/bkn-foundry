// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// A rollback switches a lineage's consumers back to the network before the one in use: the release
// published just before it, or the lineage's base network before the first. Rolling back again goes
// further back. It moves schedules and records a switch, as any switch does; it never rewrites data
// or definitions. A project rolls back only a release of its own, and only after reading the plan:
// the rollback refuses a plan that no longer stands. Codes are for machines; clients word them.

// The consumers whose owners switch them back; bkn-project lists the kinds, as it keeps no record
// of each.
const (
	ROLLBACK_CONSUMER_AGENTS   = "agents"
	ROLLBACK_CONSUMER_MCP      = "mcp"
	ROLLBACK_CONSUMER_WORKSHOP = "workshop"
	ROLLBACK_CONSUMER_EVAL     = "eval"
)

// RollbackNetwork is a network of the lineage: a release's, by its version, or the base network.
type RollbackNetwork struct {
	NetworkID string `json:"network_id"`
	Version   string `json:"version,omitempty"`
	Base      bool   `json:"base"`
}

// RollbackPlan is what rolling the project's network back would do. To is absent when there is no
// network to go back to. Schedules are the active schedules of the network in use, which the
// rollback turns off there and on on To. Consumers are switched back by their owners. Questions
// place the project's scenario questions as To's definitions have what they need, against the
// network in use: unanswerable lose something, affected rest on something To changes, and
// answerable_new can be answered only on To. Refusals say why the rollback cannot go; none stands
// when it can. Signature stands for the plan; the rollback refuses another.
type RollbackPlan struct {
	ProjectID string            `json:"project_id"`
	From      RollbackNetwork   `json:"from"`
	To        *RollbackNetwork  `json:"to,omitempty"`
	Schedules []*ActionSchedule `json:"schedules"`
	Consumers []string          `json:"consumers"`
	Questions ReleaseQuestions  `json:"questions"`
	Refusals  []ReleaseRefusal  `json:"refusals"`
	Signature string            `json:"signature"`
}

// ProjectRollback is the body of a project's rollback: the signature of the plan read.
type ProjectRollback struct {
	Signature string `json:"signature"`
}

// ACTION_REVIEW_ROLLBACK reads what rolling back the release in use would do; rolling back goes on
// from the plan.
const ACTION_REVIEW_ROLLBACK = "review_rollback"
