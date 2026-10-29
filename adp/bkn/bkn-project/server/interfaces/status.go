// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// A project's status is computed on each request from its draft, confirmation and check: where
// the project stands, what blocks it and who it waits for, what is out of date, and what can be
// done next. Nothing of it is stored. Codes are for machines; clients word them.

// Project stages, in the order a project goes through them.
const (
	PROJECT_STAGE_ARCHIVED              = "archived"
	PROJECT_STAGE_NO_DRAFT              = "no_draft"
	PROJECT_STAGE_DRAFTING              = "drafting"
	PROJECT_STAGE_DRAFT_FAILED          = "draft_failed"
	PROJECT_STAGE_DECIDING              = "deciding"
	PROJECT_STAGE_READY_TO_CONFIRM      = "ready_to_confirm"
	PROJECT_STAGE_CHECK_NOT_STARTED     = "check_not_started"
	PROJECT_STAGE_CHECKING              = "checking"
	PROJECT_STAGE_CHECK_NEEDS_ATTENTION = "check_needs_attention"
	PROJECT_STAGE_CONFIRMED             = "confirmed"
	// PROJECT_STAGE_PUBLISHED is a project whose current confirmation the newest release published.
	PROJECT_STAGE_PUBLISHED = "published"
)

// What blocks a project from its next stage.
const (
	BLOCKER_NO_CATALOGS     = "no_catalogs"
	BLOCKER_NEEDS_IDENTITY  = "needs_identity"
	BLOCKER_UNADOPTED_ENDS  = "unadopted_ends"
	BLOCKER_NOTHING_ADOPTED = "nothing_adopted"
	// What the project branch would refuse confirming for, read ahead.
	BLOCKER_BRANCH_NOT_V3     = "branch_not_v3"
	BLOCKER_LINEAGE_NOT_V3    = "lineage_not_v3"
	BLOCKER_DEFINITIONS_EXIST = "definitions_exist"
	BLOCKER_CHECK_ISSUES      = "check_issues"
	BLOCKER_CHECK_PARTIAL     = "check_partial"
	BLOCKER_CHECK_FAILED      = "check_failed"
)

// Who a blocker waits for.
const (
	WAITING_FOR_OWNER  = "owner"
	WAITING_FOR_EDITOR = "editor"
	WAITING_FOR_SYSTEM = "system"
)

// What is out of date.
const (
	STALE_CONFIRMATION_OUTDATED = "confirmation_outdated"
	STALE_CHECK_OUTDATED        = "check_outdated"
)

// What can be done next.
const (
	ACTION_UNARCHIVE      = "unarchive"
	ACTION_SET_CATALOGS   = "set_catalogs"
	ACTION_GENERATE_DRAFT = "generate_draft"
	ACTION_ADOPT_ITEMS    = "adopt_items"
	ACTION_CONFIRM_DRAFT  = "confirm_draft"
	ACTION_RUN_CHECK      = "run_check"
	ACTION_SYNC_BRANCH    = "sync_branch"
	ACTION_COMMIT_DRIFT   = "commit_drift"
	ACTION_WAIT           = "wait"
	// ACTION_REVIEW_RELEASE reads what publishing the confirmation would bring; publishing goes on
	// from the review, which needs a version.
	ACTION_REVIEW_RELEASE = "review_release"
	// ACTION_SWITCH_CONSUMERS switches consumers to the release that published the project.
	ACTION_SWITCH_CONSUMERS = "switch_consumers"
)

// What sending an action accepts on the caller's behalf.
const (
	// ACCEPTS_PARTIAL_DRAFT confirms a draft knowing the tables drafting could not read.
	ACCEPTS_PARTIAL_DRAFT = "partial_draft"
	// ACCEPTS_UNDECIDED_ITEMS confirms a draft leaving out the items no one decided on.
	ACCEPTS_UNDECIDED_ITEMS = "undecided_items"
	// ACCEPTS_UNRESOLVED_CONFLICTS confirms a draft knowing conflicts between the requirement
	// documents are unresolved.
	ACCEPTS_UNRESOLVED_CONFLICTS = "unresolved_conflicts"
)

// Why an action cannot be done by the caller; otherwise a blocker code.
const (
	ACTION_REASON_NOT_OWNER = "not_owner"
	ACTION_REASON_RUNNING   = "running"
)

type ProjectStatus struct {
	ProjectID    string                  `json:"project_id"`
	Stage        string                  `json:"stage"`
	Draft        *ProjectStatusDraft     `json:"draft,omitempty"`
	Confirmation *ProjectStatusConfirmed `json:"confirmation,omitempty"`
	Check        *ProjectStatusCheck     `json:"check,omitempty"`
	// Release is the release that published the current confirmation.
	Release  *ProjectStatusRelease  `json:"release,omitempty"`
	Blockers []ProjectStatusBlocker `json:"blockers"`
	// Undecided counts the items drafting left out and no one decided on: they do not block, but
	// confirming leaves them out only when the confirmation accepts it.
	Undecided int `json:"undecided"`
	// UnresolvedConflicts counts the conflicts between the requirement documents of a ready
	// specification without a resolution, deferred ones included: they do not block, but confirming
	// accepts them.
	UnresolvedConflicts int                       `json:"unresolved_conflicts"`
	Stale               []string                  `json:"stale"`
	NextActions         []ProjectStatusNextAction `json:"next_actions"`
	// Links are the reads that show the details: the draft, its pending items and the check.
	Links      map[string]string `json:"links"`
	ComputedAt int64             `json:"computed_at"`
}

type ProjectStatusDraft struct {
	Status     string `json:"status"`
	UpdateTime int64  `json:"update_time"`
	Decisions  int    `json:"decisions"`
	// Placeholders counts the ids of names that are not ASCII still standing in for a short name
	// that drafting again can replace: running semantic understanding on their catalog gives them
	// one. A placeholder a confirmation locked is not counted. They do not block.
	Placeholders int `json:"placeholders"`
}

type ProjectStatusConfirmed struct {
	Commit string `json:"commit"`
	Time   int64  `json:"time"`
	// Current is whether the confirmation is of the draft as it now is.
	Current bool `json:"current"`
}

type ProjectStatusCheck struct {
	Commit string `json:"commit"`
	Status string `json:"status"`
	// Current is whether the check is of the current confirmation.
	Current bool `json:"current"`
}

type ProjectStatusRelease struct {
	Version   string `json:"version"`
	NetworkID string `json:"network_id"`
	// InUse is whether consumers use the release's network: the lineage's latest switch was to it.
	InUse bool `json:"in_use"`
}

type ProjectStatusBlocker struct {
	Code       string `json:"code"`
	Count      int    `json:"count,omitempty"`
	WaitingFor string `json:"waiting_for"`
}

// ProjectStatusNextAction is a request a client can send, its path relative to the API root and
// filled in. Allowed says whether the caller may send it; Reason says why not.
type ProjectStatusNextAction struct {
	Action  string         `json:"action"`
	Method  string         `json:"method"`
	Path    string         `json:"path"`
	Body    map[string]any `json:"body,omitempty"`
	Allowed bool           `json:"allowed"`
	Reason  string         `json:"reason,omitempty"`
	// RetryAfterS is when to read the status again, for waiting.
	RetryAfterS int `json:"retry_after_s,omitempty"`
	// DiscardsDecisions is how many decisions drafting again would discard.
	DiscardsDecisions int `json:"discards_decisions,omitempty"`
	// Accepts says what sending the action accepts, as its body does.
	Accepts []string `json:"accepts,omitempty"`
	// Requires names what the request needs that a client may choose: version, for reviewing a
	// release, whose path carries SuggestedVersion, the patch after the lineage's newest release.
	Requires         []string `json:"requires,omitempty"`
	SuggestedVersion string   `json:"suggested_version,omitempty"`
}

// ACTION_REQUIRES_VERSION is the version a release review is of.
const ACTION_REQUIRES_VERSION = "version"

// Why an option of a pending item cannot be taken.
const (
	ACTION_REASON_ARCHIVED           = "archived"
	ACTION_REASON_END_NEEDS_IDENTITY = "end_needs_identity"
	// ACTION_REASON_NO_KEY_PROPERTY is an object type none of whose properties can identify records:
	// it can only be left out.
	ACTION_REASON_NO_KEY_PROPERTY = "no_key_property"
)

// What a pending item can be settled by.
const (
	ACTION_CHOOSE_IDENTITY = "choose_identity"
	ACTION_UNADOPT         = "unadopt"
	ACTION_ADOPT_END       = "adopt_end"
	ACTION_ADOPT           = "adopt"
)

// PENDING_REASON_UNDECIDED is a pending item drafting left out and no one decided on; it does not
// block confirming.
const PENDING_REASON_UNDECIDED = "undecided"

// PENDING_PLACEHOLDER_UPDATE_TIME stands in a body template for the draft's update time as the
// sender last read it: each edit moves it on.
const PENDING_PLACEHOLDER_UPDATE_TIME = "$draft_update_time"

// PENDING_PLACEHOLDER_CANDIDATE stands for one of an option's candidates.
const PENDING_PLACEHOLDER_CANDIDATE = "$candidate"

// ProjectPending lists what waits for a person before the draft can be confirmed, each with the
// requests that settle it, in one order: by kind, then item ID.
type ProjectPending struct {
	ProjectID       string        `json:"project_id"`
	DraftUpdateTime int64         `json:"draft_update_time"`
	Items           []PendingItem `json:"items"`
}

type PendingItem struct {
	Kind   string `json:"kind"`
	ItemID string `json:"item_id"`
	Name   string `json:"name"`
	// Blocking is set for what confirming refuses until settled; an undecided item does not block.
	Blocking bool `json:"blocking"`
	// Reason is a blocker code, needs_identity or unadopted_ends, or undecided.
	Reason string `json:"reason"`
	// Because is why drafting left an undecided item out: needs_identity, a relation's
	// proposed_because, or not_adopted.
	Because string          `json:"because,omitempty"`
	Options []PendingOption `json:"options"`
	// Recommendation is the option a written rule recommends, with the rule; null when no rule does.
	Recommendation *PendingRecommendation `json:"recommendation"`
	// Evidence is what drafting counted for the item: the key for an object type, the join for a
	// relation.
	Evidence any `json:"evidence,omitempty"`
}

// PendingOption is a request that settles a pending item. Candidates, for choosing an identity,
// are the property lists to send in place of $candidate, the one drafting counted first.
type PendingOption struct {
	Action     string         `json:"action"`
	Method     string         `json:"method"`
	Path       string         `json:"path"`
	Body       map[string]any `json:"body"`
	Allowed    bool           `json:"allowed"`
	Reason     string         `json:"reason,omitempty"`
	Candidates [][]string     `json:"candidates,omitempty"`
}

type PendingRecommendation struct {
	Action string `json:"action"`
	Basis  string `json:"basis"`
	RuleID string `json:"rule_id"`
}
