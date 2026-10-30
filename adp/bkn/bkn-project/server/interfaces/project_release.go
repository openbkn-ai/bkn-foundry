// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// A project publishes its branch as a new release of its network's lineage. Before it does, the
// owner reviews what the release would bring and whether it may go: the release checks, the changes
// by name, the risks to what is live, and what the release does with functions and Skills. Codes
// are for machines; clients word them.

// Release checks, in the order a review lists them.
const (
	RELEASE_CHECK_CONFIRMED = "confirmed"
	RELEASE_CHECK_MAPPED    = "mapped"
	// RELEASE_CHECK_DATA_SOURCE: every table a type on the branch reads is still in vega.
	RELEASE_CHECK_DATA_SOURCE = "data_source"
	RELEASE_CHECK_NAMES       = "names"
	RELEASE_CHECK_DRIFT       = "drift"
	RELEASE_CHECK_CHECK       = "check"
	RELEASE_CHECK_INDEX       = "index"
	RELEASE_CHECK_BASELINE    = "baseline"
)

// Why a release check did not pass.
const (
	RELEASE_REASON_NOT_CONFIRMED         = "not_confirmed"
	RELEASE_REASON_CONFIRMATION_OUTDATED = "confirmation_outdated"
	RELEASE_REASON_MAPPING_MISSING       = "mapping_missing"
	RELEASE_REASON_DUPLICATE_NAMES       = "duplicate_names"
	RELEASE_REASON_SIMILAR_NAMES         = "similar_names"
	RELEASE_REASON_DRIFT                 = "drift"
	RELEASE_REASON_CHECK_NOT_RUN         = "check_not_run"
	RELEASE_REASON_CHECK_RUNNING         = "check_running"
	RELEASE_REASON_CHECK_ISSUES          = "check_issues"
	RELEASE_REASON_CHECK_FAILED          = "check_failed"
	RELEASE_REASON_CHECK_PARTIAL         = "check_partial"
	RELEASE_REASON_INDEX_NOT_READY       = "index_not_ready"
	RELEASE_REASON_INDEX_UNREADABLE      = "index_unreadable"
	RELEASE_REASON_BRANCH_BEHIND         = "branch_behind"
	// RELEASE_REASON_TABLE_MISSING: tables some types read are gone; details name the types.
	RELEASE_REASON_TABLE_MISSING = "table_missing"
	// RELEASE_REASON_DATA_SOURCE_UNREADABLE: vega did not tell whether the tables are there.
	RELEASE_REASON_DATA_SOURCE_UNREADABLE = "data_source_unreadable"
)

// What the owner may accept to publish though a check did not pass. Nothing else can be accepted.
const (
	RELEASE_ACCEPT_SIMILAR_NAMES   = "similar_names"
	RELEASE_ACCEPT_CHECK_PARTIAL   = "check_partial"
	RELEASE_ACCEPT_INDEX_NOT_READY = "index_not_ready"
)

// Kinds of the definitions a release changes.
const (
	RELEASE_ITEM_OBJECT_TYPE   = "object_type"
	RELEASE_ITEM_RELATION_TYPE = "relation_type"
)

// ReleaseReview is what publishing the project's branch as Version would bring, and whether it may.
type ReleaseReview struct {
	ProjectID string `json:"project_id"`
	Version   string `json:"version"`
	// Checks are the seven release checks, each passed or not.
	Checks []ReleaseCheck `json:"checks"`
	// Refusals are the other reasons the release itself would be refused for, such as a version
	// already published; none can be accepted.
	Refusals []ReleaseRefusal `json:"refusals"`
	// Accepts are what the owner has to accept for the release to go, when every check that did
	// not pass can be accepted; Blocked is set when one cannot, or a refusal stands.
	Accepts []string       `json:"accepts"`
	Blocked bool           `json:"blocked"`
	Changes ReleaseChanges `json:"changes"`
	Risks   ReleaseRisks   `json:"risks"`
	Release ReleaseTarget  `json:"release"`
	// Questions are where the project's scenario questions stand with the release.
	Questions ReleaseQuestions `json:"questions"`
	// Capabilities is what the release does with each function and Skill.
	Capabilities []CapabilityPlan `json:"capabilities"`
	// Signature stands for what the review was made from; publishing refuses another.
	Signature string `json:"signature"`
}

// ReleaseCheck is one release check. Reason says why it did not pass; Accept, when set, is what
// the owner may accept to publish anyway. Details are for the client to show.
type ReleaseCheck struct {
	Code    string `json:"code"`
	Passed  bool   `json:"passed"`
	Reason  string `json:"reason,omitempty"`
	Accept  string `json:"accept,omitempty"`
	Details any    `json:"details,omitempty"`
}

// ReleaseItem names an object type or relation type.
type ReleaseItem struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ReleaseChanges are the object types and relation types the release adds, changes or removes
// against what is live, by name.
type ReleaseChanges struct {
	Added   []ReleaseItem `json:"added"`
	Changed []ReleaseItem `json:"changed"`
	Removed []ReleaseItem `json:"removed"`
	// Other counts the other definitions the release changes, such as action types.
	Other int `json:"other"`
}

// ReleaseRisks are where the release touches what is live. OnlyAdditions is set when it changes
// and removes nothing.
type ReleaseRisks struct {
	OnlyAdditions bool `json:"only_additions"`
	// Existing are the live object types and relation types the release changes or removes.
	Existing []ReleaseItem `json:"existing"`
	// Orphans are the relation types whose records partly refer to nothing in their end.
	Orphans []ReleaseOrphans `json:"orphans"`
	// Copies counts the functions and Skills the release copies to new ids.
	Copies int `json:"copies"`
}

type ReleaseOrphans struct {
	ReleaseItem
	Orphans int64 `json:"orphans"`
}

// ReleaseTarget is the network the release creates and what it starts from.
type ReleaseTarget struct {
	Tag           string `json:"tag"`
	NetworkID     string `json:"network_id"`
	NetworkName   string `json:"network_name"`
	LiveNetworkID string `json:"live_network_id"`
	BaseVersion   string `json:"base_version"`
	MainCommit    string `json:"main_commit"`
	BranchCommit  string `json:"branch_commit"`
}

// ReleaseQuestions place the scenario questions of the knowledge layer, those answered from object
// types and relation types, by where what they need is: AnswerableNew can be answered only once
// released; Affected can be answered now and the release changes or removes something they need;
// Unanswerable need something the release does not have, listed in Missing, or need object types
// that nothing in the draft joins, listed in Unconnected. What a question needs is what it names
// and the path that joins those in the draft (Path). Total counts them all, Unjudged those that
// name nothing they need.
type ReleaseQuestions struct {
	SpecVersion   int               `json:"spec_version"`
	Total         int               `json:"total"`
	Unjudged      int               `json:"unjudged"`
	AnswerableNew []ReleaseQuestion `json:"answerable_new"`
	Affected      []ReleaseQuestion `json:"affected"`
	Unanswerable  []ReleaseQuestion `json:"unanswerable"`
}

type ReleaseQuestion struct {
	ID    string        `json:"id"`
	Text  string        `json:"text"`
	Needs []ReleaseItem `json:"needs"`
	// Path is what joins the object types it needs in the draft, derived when the review is made.
	Path        []ReleaseItem        `json:"path,omitempty"`
	Missing     []ReleaseItem        `json:"missing,omitempty"`
	Unconnected []ReleaseUnconnected `json:"unconnected,omitempty"`
}

// ReleaseUnconnected is two object types a question needs that no adopted path in the draft joins,
// named for people to read.
type ReleaseUnconnected struct {
	From ReleaseItem `json:"from"`
	To   ReleaseItem `json:"to"`
}

// SimilarNames is a pair of names of one kind that are the same or nearly so: Added is the item
// the release adds, Other the one it meets.
type SimilarNames struct {
	Added ReleaseItem `json:"added"`
	Other ReleaseItem `json:"other"`
}

// ProjectPublish is the body of a project's release. Signature is that of the review the owner
// read; Accepts are what the owner accepts of the checks that did not pass.
type ProjectPublish struct {
	Version   string   `json:"version"`
	Notes     string   `json:"notes"`
	Signature string   `json:"signature"`
	Accepts   []string `json:"accepts"`
}

// RELEASE_NOTES_ACCEPTED starts the line of release notes that records, as codes, what the owner
// accepted to publish.
const RELEASE_NOTES_ACCEPTED = "Accepted: "

// ProjectDriftCommit is what committing the drift of the live network did: the drift committed to
// main, and how the project branch was then brought up to it.
type ProjectDriftCommit struct {
	Drift *DriftResult      `json:"drift"`
	Sync  *SyncBranchResult `json:"sync"`
}

// ProjectReleaseService reviews and publishes a project's releases.
//
//go:generate mockgen -source ../interfaces/project_release.go -destination ../interfaces/mock/mock_project_release.go
type ProjectReleaseService interface {
	// ReviewRelease reads what publishing the project's branch as version would bring and whether
	// it may, as the caller.
	ReviewRelease(ctx context.Context, token string, projectID string, version string,
		caller AccountInfo) (*ReleaseReview, error)
	// PublishRelease publishes the project's branch, for the owner, once its review still stands
	// and every check passed or was accepted, and then starts its regression in the background.
	PublishRelease(ctx context.Context, token string, projectID string, req ProjectPublish,
		caller AccountInfo, author GitAuthor) (*PublishReleaseResult, error)
	// SyncBranch brings the live version into the project's branch, for the owner.
	SyncBranch(ctx context.Context, token string, projectID string, caller AccountInfo,
		author GitAuthor) (*SyncBranchResult, error)
	// CommitDrift commits the live network's drift to main and syncs the project's branch, for the
	// owner.
	CommitDrift(ctx context.Context, token string, projectID string, caller AccountInfo,
		author GitAuthor) (*ProjectDriftCommit, error)
	// PlanSwitch reports what switching consumers to a release of the project would do.
	PlanSwitch(ctx context.Context, token string, projectID string, toNetworkID string) (*SwitchPlan, error)
	// GetRegression returns the regression of a release of the project, read by anyone who can
	// read the project.
	GetRegression(ctx context.Context, token string, projectID string, networkID string) (*Regression, error)
	// RunRegression starts the regression of a release of the project again, for the owner, with
	// the questions the specification has now, and returns it, running.
	RunRegression(ctx context.Context, token string, projectID string, networkID string,
		caller AccountInfo) (*Regression, error)
	// SwitchConsumers switches consumers to a release of the project, for the owner.
	SwitchConsumers(ctx context.Context, token string, projectID string, toNetworkID string,
		caller AccountInfo) (*Switch, error)
}
