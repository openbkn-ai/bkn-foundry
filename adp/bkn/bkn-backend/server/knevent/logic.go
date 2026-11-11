// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

// What happens to a network's events, ported from the Workshop prototype
// (studio/src/workshop-lab/data/events.ts). A rule hit opens one event per
// object and abnormal period, a repeated hit only updates it and a recovery
// closes it; a report starts unverified unless the reporter verifies it, and
// a repeat within the merge window is a duplicate of the first.
//
// The functions change loaded events in place and return the changes to
// append to the change feed; the store writes them in one transaction.

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// History and change-feed actions besides the person's event actions.
const (
	actionReported             = "reported"
	actionDetected             = "detected"
	actionRecovered            = "recovered"
	actionRecoveredAfterUpdate = "recovered-after-update"

	actorRule = "rule"
)

// op is what a change needs besides its input.
type op struct {
	now   int64
	actor interfaces.AccountInfo
	newID func() string
	// title words a rule event's title from its type and object names.
	title func(typeName, objectName string) string
}

func (o op) by(action, from, to, note string) interfaces.KnEventTransition {
	return interfaces.KnEventTransition{
		Action: action, FromStatus: from, ToStatus: to,
		Actor: o.actor.ID, ActorType: o.actor.Type, ActorName: o.actor.Name,
		Note: note, Time: o.now,
	}
}

func byRule(action, from, to string, at int64) interfaces.KnEventTransition {
	return interfaces.KnEventTransition{Action: action, FromStatus: from, ToStatus: to, ActorType: actorRule, Time: at}
}

// change is one entry to append to the change feed for an event, whose state
// after the write is stored with it.
type change struct {
	event      *interfaces.KnEvent
	transition interfaces.KnEventTransition
}

// isOpen reports whether a reported event can still take duplicates.
func isOpen(event *interfaces.KnEvent) bool {
	return event.Status == interfaces.KnEventUnverified || event.Status == interfaces.KnEventVerified
}

// needsReason reports whether an event action must say why.
func needsReason(action string) bool {
	switch action {
	case interfaces.KnEventActionReject, interfaces.KnEventActionClose, interfaces.KnEventActionReopen:
		return true
	}
	return false
}

// actionsFor is what a person may do with an event in its state. A closed
// event opens again while what it is about goes on: a rule event until its
// rule finds it over, a reported one at any time.
func actionsFor(event *interfaces.KnEvent) []string {
	switch event.Status {
	case interfaces.KnEventUnverified:
		return []string{interfaces.KnEventActionVerify, interfaces.KnEventActionReject}
	case interfaces.KnEventVerified:
		return []string{interfaces.KnEventActionClose}
	case interfaces.KnEventClosed:
		if event.Source == interfaces.KnEventSourceReport || event.EndedAt == 0 {
			return []string{interfaces.KnEventActionReopen}
		}
	}
	return []string{}
}

// touch marks an existing event as changed by this write.
func touch(event *interfaces.KnEvent, at int64) {
	event.Revision++
	event.UpdateTime = at
}

// mergeAttributes updates attributes by property name.
func mergeAttributes(event *interfaces.KnEvent, attributes map[string]interfaces.KnEventAttribute) {
	if len(attributes) == 0 {
		return
	}
	if event.ObjectAttributes == nil {
		event.ObjectAttributes = make(map[string]interfaces.KnEventAttribute, len(attributes))
	}
	for name, value := range attributes {
		event.ObjectAttributes[name] = value
	}
}

func clip(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

// lastDecided is when the rule last decided, or 0 if it never has.
func lastDecided(previous *interfaces.KnEventRuleCheck) int64 {
	if previous == nil {
		return 0
	}
	if previous.Status == interfaces.KnEventCheckOK {
		return previous.Time
	}
	return previous.LastOK
}

// applyRuleCheck records one check of a rule event type. mine holds the
// type's events whose abnormal period is open, oldest first. Hits open events
// for objects without one; open periods the check covered but did not hit are
// over. A check that could not decide changes no event's state. Every event
// the check touched gets one change, a pure refresh being "checked".
func applyRuleCheck(
	eventType interfaces.KnEventType,
	previous *interfaces.KnEventRuleCheck,
	mine []*interfaces.KnEvent,
	input interfaces.KnEventRuleCheckInput,
	o op,
) ([]change, interfaces.KnEventRuleCheck) {
	at := o.now
	version := input.RuleVersion
	lastOK := lastDecided(previous)
	changes := make([]change, 0, len(mine)+len(input.Hits))

	if input.Status == interfaces.KnEventCheckUnknown {
		// Undecided is not a recovery: open events keep their state.
		for _, event := range mine {
			event.LastCheckedAt = at
			event.CheckStatus = interfaces.KnEventCheckUnknown
			touch(event, at)
			changes = append(changes, change{event: event, transition: byRule(interfaces.KnEventChecked, event.Status, event.Status, at)})
		}
		return changes, interfaces.KnEventRuleCheck{
			EventType: eventType.Code, Status: interfaces.KnEventCheckUnknown, RuleVersion: version,
			Message: input.Message, LastOK: lastOK, CheckedBy: o.actor.ID, Time: at,
		}
	}

	hits := make(map[string]interfaces.KnEventRuleHit, len(input.Hits))
	for _, hit := range input.Hits {
		if _, dup := hits[hit.Key]; !dup {
			hits[hit.Key] = hit
		}
	}
	var covered map[string]bool
	if input.Covered != nil {
		covered = make(map[string]bool, len(input.Covered))
		for _, key := range input.Covered {
			covered[key] = true
		}
	}
	seen := make(map[string]bool, len(mine)+len(hits))
	// The first check that decides, after a rule is set up, finds what has long
	// been wrong: a backlog. Checks that could not decide before it do not count.
	first := lastOK == 0
	for _, event := range mine {
		key := event.ObjectKey
		seen[key] = true
		if covered != nil && !covered[key] {
			continue
		}
		updated := event.RuleVersion != version
		event.LastCheckedAt = at
		event.CheckStatus = interfaces.KnEventCheckOK
		touch(event, at)
		if hit, found := hits[key]; found {
			event.RuleVersion = version
			mergeAttributes(event, hit.Attributes)
			changes = append(changes, change{event: event, transition: byRule(interfaces.KnEventChecked, event.Status, event.Status, at)})
			continue
		}
		from := event.Status
		event.EndedAt = at
		event.Status = interfaces.KnEventClosed
		action := actionRecovered
		if updated {
			action = actionRecoveredAfterUpdate
		}
		changes = append(changes, change{event: event, transition: byRule(action, from, event.Status, at)})
	}
	for _, hit := range input.Hits {
		if seen[hit.Key] {
			continue
		}
		seen[hit.Key] = true
		name := hit.Name
		if name == "" {
			name = hit.Key
		}
		attributes := hit.Attributes
		if attributes == nil {
			attributes = map[string]interfaces.KnEventAttribute{}
		}
		event := &interfaces.KnEvent{
			ID:               o.newID(),
			EventType:        eventType.Code,
			EventTypeName:    eventType.Name,
			Title:            clip(o.title(eventType.Name, name), interfaces.KnEventTitleMax),
			Status:           interfaces.KnEventVerified,
			Severity:         eventType.Severity,
			Source:           interfaces.KnEventSourceRule,
			RuleVersion:      version,
			ObjectType:       eventType.Subject,
			ObjectKey:        hit.Key,
			ObjectName:       name,
			ObjectAttributes: attributes,
			OccurredAt:       at,
			RecordedAt:       at,
			FirstDetectedAt:  at,
			LastCheckedAt:    at,
			CheckStatus:      interfaces.KnEventCheckOK,
			Backlog:          first,
			VerifiedByType:   actorRule,
			VerifiedAt:       at,
			Revision:         1,
			CreateTime:       at,
			UpdateTime:       at,
		}
		changes = append(changes, change{event: event, transition: byRule(actionDetected, "", event.Status, at)})
	}
	return changes, interfaces.KnEventRuleCheck{
		EventType: eventType.Code, Status: interfaces.KnEventCheckOK, RuleVersion: version,
		Hits: len(hits), LastOK: at, CheckedBy: o.actor.ID, Time: at,
	}
}

// reportEvent records an event a person reports. open holds the type's open
// events about the same object, oldest first. It returns the new event, the
// one it repeats when it is a duplicate, and the changes to append.
func reportEvent(
	eventType interfaces.KnEventType,
	input interfaces.KnEventReportInput,
	open []*interfaces.KnEvent,
	o op,
) (*interfaces.KnEvent, *interfaces.KnEvent, []change) {
	at := o.now
	occurred := input.OccurredAt
	if occurred == 0 {
		occurred = at
	}
	var merged *interfaces.KnEvent
	if window := int64(eventType.MergeHours * 3600 * 1000); window > 0 && input.ObjectKey != "" {
		for _, event := range open {
			if event.EventType == eventType.Code && event.ObjectKey == input.ObjectKey && isOpen(event) &&
				abs(event.OccurredAt-occurred) <= window {
				merged = event
				break
			}
		}
	}
	status := interfaces.KnEventUnverified
	if merged != nil {
		status = interfaces.KnEventDuplicate
	}
	name := input.ObjectName
	if name == "" {
		name = input.ObjectKey
	}
	attributes := input.ObjectAttributes
	if attributes == nil {
		attributes = map[string]interfaces.KnEventAttribute{}
	}
	event := &interfaces.KnEvent{
		ID:               o.newID(),
		EventType:        eventType.Code,
		EventTypeName:    eventType.Name,
		Title:            strings.TrimSpace(input.Title),
		Status:           status,
		Severity:         eventType.Severity,
		Source:           interfaces.KnEventSourceReport,
		ObjectKey:        input.ObjectKey,
		ObjectName:       name,
		ObjectAttributes: attributes,
		OccurredAt:       occurred,
		RecordedAt:       at,
		Detail:           input.Detail,
		ReportedBy:       o.actor.ID,
		ReportedByName:   o.actor.Name,
		Revision:         1,
		CreateTime:       at,
		UpdateTime:       at,
	}
	if input.ObjectKey != "" {
		event.ObjectType = eventType.Subject
	}
	changes := []change{{event: event, transition: o.by(actionReported, "", status, "")}}
	if merged != nil {
		event.MergedInto = merged.ID
	} else if input.Verify {
		event.Status = interfaces.KnEventVerified
		event.VerifiedBy = o.actor.ID
		event.VerifiedByType = o.actor.Type
		event.VerifiedByName = o.actor.Name
		event.VerifiedAt = at
		changes = append(changes, change{event: event, transition: o.by(interfaces.KnEventActionVerify, status, event.Status, "")})
	}
	return event, merged, changes
}

// errActionNotAllowed says an action does not apply to an event's status.
type errActionNotAllowed struct {
	action string
	status string
}

func (e errActionNotAllowed) Error() string {
	return "action " + e.action + " is not allowed in status " + e.status
}

// errReasonRequired says an action needs a reason.
type errReasonRequired struct{ action string }

func (e errReasonRequired) Error() string { return "action " + e.action + " requires a reason" }

// changeEvent verifies, rejects, closes or reopens an event.
func changeEvent(event *interfaces.KnEvent, action, reason string, o op) (change, error) {
	if !slices.Contains(actionsFor(event), action) {
		return change{}, errActionNotAllowed{action: action, status: event.Status}
	}
	reason = strings.TrimSpace(reason)
	if needsReason(action) && reason == "" {
		return change{}, errReasonRequired{action: action}
	}
	at := o.now
	from := event.Status
	switch action {
	case interfaces.KnEventActionVerify:
		event.Status = interfaces.KnEventVerified
		event.VerifiedBy = o.actor.ID
		event.VerifiedByType = o.actor.Type
		event.VerifiedByName = o.actor.Name
		event.VerifiedAt = at
	case interfaces.KnEventActionReject:
		event.Status = interfaces.KnEventRejected
	case interfaces.KnEventActionReopen:
		event.Status = interfaces.KnEventVerified
		// A reported event's end was its closing, which no longer holds.
		if event.Source == interfaces.KnEventSourceReport {
			event.EndedAt = 0
		}
	case interfaces.KnEventActionClose:
		event.Status = interfaces.KnEventClosed
		// Handling an event is not its rule recovering: a rule event's period ends on its own.
		if event.Source != interfaces.KnEventSourceRule && event.EndedAt == 0 {
			event.EndedAt = at
		}
	}
	touch(event, at)
	return change{event: event, transition: o.by(action, from, event.Status, reason)}, nil
}

func abs(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
