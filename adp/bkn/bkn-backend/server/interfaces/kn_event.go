// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Knowledge-network events record what happened in the business: a rule found
// an object in an abnormal state, or a person reported something. They belong
// to the network, not to an application; applications read the change feed
// and handle the events in their own tasks. Each kind of event is an object
// type of the network whose kinds include event, its event class, so events
// are queried, related and authorized like the network's other objects. The
// implementation is the knevent package, which serves from the professional
// edition up.

// Event statuses describe the event itself. How an application handles it is
// recorded by that application.
const (
	KnEventUnverified = "unverified"
	KnEventVerified   = "verified"
	KnEventRejected   = "rejected"
	KnEventDuplicate  = "duplicate"
	KnEventClosed     = "closed"
)

// Event sources.
const (
	KnEventSourceRule   = "rule"
	KnEventSourceReport = "report"
)

// Rule check statuses.
const (
	KnEventCheckOK      = "ok"
	KnEventCheckUnknown = "unknown"
)

// Event actions a person takes.
const (
	KnEventActionVerify = "verify"
	KnEventActionReject = "reject"
	KnEventActionClose  = "close"
	KnEventActionReopen = "reopen"
)

// KnEventChecked is the change action of a check that only refreshed an
// event's detection fields. It is part of the change feed, not of history.
const KnEventChecked = "checked"

// Limits of the event API.
const (
	KnEventTitleMax       = 256
	KnEventReasonMax      = 512
	KnEventKeyMax         = 250
	KnEventLabelMax       = 256
	KnEventAttributesMax  = 64
	KnEventRuleVersionMax = 64
	KnEventSeverityMax    = 16
	KnEventDetailMaxBytes = 1 << 20
	// KnEventHitDetailMaxBytes bounds the detail of one hit of a rule check.
	KnEventHitDetailMaxBytes = 16 << 10
	// KnEventExecutionIDMax bounds the ID of the action execution that recorded a change.
	KnEventExecutionIDMax    = 40
	KnEventRuleHitsMax       = 20000
	KnEventListDefaultLimit  = 50
	KnEventListMaxLimit      = 200
	KnEventChangeDefaultSize = 500
	KnEventChangeMaxSize     = 1000
	// KnEventClassIDMax is the longest ID of an event class, the column the
	// ledger keeps it in.
	KnEventClassIDMax = 64
	// KnEventHandlingNoteMax is the longest handling note of an event class.
	KnEventHandlingNoteMax = 500
	// KnEventStatsDefaultSpan is the span of the occurrence counts when no
	// start and end are given: the last seven days.
	KnEventStatsDefaultSpan = 7 * 24 * 60 * 60 * 1000
)

// KnEventAttribute is a value of the event object's property, supplied by the
// writer. The platform stores it without interpreting it; applications group
// events by the property they choose.
type KnEventAttribute struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// KnEventTransition is one entry of an event's history.
type KnEventTransition struct {
	Action     string `json:"action"`
	FromStatus string `json:"from_status"`
	ToStatus   string `json:"to_status"`
	Actor      string `json:"actor"`
	ActorType  string `json:"actor_type"`
	ActorName  string `json:"actor_name"`
	Note       string `json:"note"`
	Time       int64  `json:"time"`
	// ExecutionID is the action execution that made the change, when an action did: the check
	// of a record-event action type.
	ExecutionID string `json:"execution_id,omitempty"`
}

// KnEvent is one business event of a network.
type KnEvent struct {
	ID               string                      `json:"id"`
	KNID             string                      `json:"kn_id"`
	EventType        string                      `json:"event_type"`
	EventTypeName    string                      `json:"event_type_name"`
	Title            string                      `json:"title"`
	Status           string                      `json:"status"`
	Severity         string                      `json:"severity"`
	Source           string                      `json:"source"`
	RuleVersion      string                      `json:"rule_version"`
	ObjectType       string                      `json:"object_type"`
	ObjectKey        string                      `json:"object_key"`
	ObjectName       string                      `json:"object_name"`
	ObjectAttributes map[string]KnEventAttribute `json:"object_attributes"`
	OccurredAt       int64                       `json:"occurred_at"`
	RecordedAt       int64                       `json:"recorded_at"`
	EndedAt          int64                       `json:"ended_at"`
	FirstDetectedAt  int64                       `json:"first_detected_at"`
	LastCheckedAt    int64                       `json:"last_checked_at"`
	CheckStatus      string                      `json:"check_status"`
	Detail           json.RawMessage             `json:"detail"`
	Backlog          bool                        `json:"backlog"`
	MergedInto       string                      `json:"merged_into"`
	ReportedBy       string                      `json:"reported_by"`
	ReportedByName   string                      `json:"reported_by_name"`
	VerifiedBy       string                      `json:"verified_by"`
	VerifiedByType   string                      `json:"verified_by_type"`
	VerifiedByName   string                      `json:"verified_by_name"`
	VerifiedAt       int64                       `json:"verified_at"`
	Revision         int                         `json:"revision"`
	CreateTime       int64                       `json:"create_time"`
	UpdateTime       int64                       `json:"update_time"`
	Actions          []string                    `json:"actions"`
	History          []KnEventTransition         `json:"history,omitempty"`
}

// KnEventFilter narrows the event list. From and To bound the time the
// events occurred, from inclusive to exclusive; 0 leaves that end open.
type KnEventFilter struct {
	EventTypes []string
	Statuses   []string
	ObjectKey  string
	From       int64
	To         int64
	Cursor     string
	Limit      int
}

// KnEventStatsFilter chooses what the occurrence counts count: the events of
// the classes given, all when none is, about one object when ObjectKey is
// given, that occurred from From, inclusive, to To, exclusive. 0 for both is
// the last seven days; From alone counts up to now, and To alone the seven
// days before it.
type KnEventStatsFilter struct {
	EventTypes []string
	ObjectKey  string
	From       int64
	To         int64
}

// KnEventStats counts the events of each class that occurred in a span. A
// rejected or duplicate report does not count, nor does what a rule found
// already there when it was first checked.
type KnEventStats struct {
	From    int64              `json:"from"`
	To      int64              `json:"to"`
	Entries []KnEventStatEntry `json:"entries"`
}

// KnEventStatEntry is the occurrences of one class. OpenCount is how many of
// them are not over: unverified or verified. LastOccurredAt is when the latest
// of them occurred.
type KnEventStatEntry struct {
	EventType      string `json:"event_type"`
	Count          int    `json:"count"`
	OpenCount      int    `json:"open_count"`
	LastOccurredAt int64  `json:"last_occurred_at"`
}

// KnEventList is one page of events, newest first.
type KnEventList struct {
	Entries    []KnEvent `json:"entries"`
	NextCursor string    `json:"next_cursor"`
}

// KnEventReportInput is an event a person reports.
type KnEventReportInput struct {
	EventType        string                      `json:"event_type"`
	Title            string                      `json:"title"`
	OccurredAt       int64                       `json:"occurred_at"`
	ObjectKey        string                      `json:"object_key"`
	ObjectName       string                      `json:"object_name"`
	ObjectAttributes map[string]KnEventAttribute `json:"object_attributes"`
	Detail           json.RawMessage             `json:"detail"`
	// Verify verifies the event at once, as its reporter.
	Verify bool `json:"verify"`
}

// KnEventReasonInput carries the reason some actions require.
type KnEventReasonInput struct {
	Reason string `json:"reason"`
}

// KnEventRuleHit is an object a rule found.
type KnEventRuleHit struct {
	Key        string                      `json:"key"`
	Name       string                      `json:"name"`
	Attributes map[string]KnEventAttribute `json:"attributes"`
	// Detail is what the rule found about the object, such as the value of a metric. It is
	// written to the event the check opens; a later check does not change it.
	Detail json.RawMessage `json:"detail,omitempty"`
}

// KnEventRuleCheckInput is one check of a rule event class.
type KnEventRuleCheckInput struct {
	EventType string `json:"event_type"`
	// ActionTypeID is the record-event action type whose check this is. A class that has one in
	// the running version takes checks only from it; only the platform sends this field.
	ActionTypeID string           `json:"action_type_id,omitempty"`
	RuleVersion  string           `json:"rule_version"`
	Status       string           `json:"status"`
	Message      string           `json:"message"`
	Hits         []KnEventRuleHit `json:"hits"`
	// Covered names the objects the check read when it read only some; the
	// others are left as they are. Nil when it read them all.
	Covered []string `json:"covered"`
}

// KnEventRuleCheck is the last check of a rule event class in a network.
type KnEventRuleCheck struct {
	EventType   string `json:"event_type"`
	Status      string `json:"status"`
	RuleVersion string `json:"rule_version"`
	Hits        int    `json:"hits"`
	Message     string `json:"message"`
	// LastOK is when the rule last decided, after checks that could not.
	LastOK int64 `json:"last_ok"`
	// LastOKRuleVersion is the rule version of the check at LastOK: a rule
	// that has not decided since it changed finds a backlog when it does.
	LastOKRuleVersion string `json:"-"`
	CheckedBy         string `json:"checked_by"`
	Time              int64  `json:"time"`
}

// KnEventsResult is the events a write created or changed, as they are now.
type KnEventsResult struct {
	Events []KnEvent `json:"events"`
}

// KnEventRuleCheckResult is what a rule check changed and the check recorded.
type KnEventRuleCheckResult struct {
	Events []KnEvent        `json:"events"`
	Check  KnEventRuleCheck `json:"check"`
}

// KnEventChange is one entry of a network's change feed. Event is the event's
// current state, without history.
type KnEventChange struct {
	Seq     int64   `json:"seq"`
	EventID string  `json:"event_id"`
	Action  string  `json:"action"`
	Time    int64   `json:"time"`
	Event   KnEvent `json:"event"`
}

// KnEventChangeList is one page of the change feed. LastSeq is the largest
// sequence returned, or the requested one when nothing is new.
type KnEventChangeList struct {
	Entries []KnEventChange `json:"entries"`
	LastSeq int64           `json:"last_seq"`
}

// KnEventServiceV1 is the knowledge-network event surface. The caller's
// account is read from ACCOUNT_INFO_KEY; ListChanges is internal and takes none.
type KnEventServiceV1 interface {
	ListEvents(ctx context.Context, knID string, filter KnEventFilter) (KnEventList, error)
	GetEvent(ctx context.Context, knID, eventID string) (KnEvent, error)
	ReportEvent(ctx context.Context, knID string, input KnEventReportInput) (KnEventsResult, error)
	ChangeEvent(ctx context.Context, knID, eventID, action string, input KnEventReasonInput) (KnEventsResult, error)
	RecordRuleCheck(ctx context.Context, knID string, input KnEventRuleCheckInput) (KnEventRuleCheckResult, error)
	ListRuleChecks(ctx context.Context, knID string) ([]KnEventRuleCheck, error)
	CountEvents(ctx context.Context, knID string, filter KnEventStatsFilter) (KnEventStats, error)

	ListChanges(ctx context.Context, knID string, after int64, limit int) (KnEventChangeList, error)
}

// KnEventCleanupAccess removes a network's events in the transaction that
// deletes the network.
type KnEventCleanupAccess interface {
	DeleteKnEvents(ctx context.Context, tx *sql.Tx, knID string) error
}
