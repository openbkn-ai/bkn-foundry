// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const hour = int64(3600 * 1000)

func testOp(now int64) op {
	next := 0
	return op{
		now:   now,
		actor: interfaces.AccountInfo{ID: "u1", Type: "user", Name: "Alice"},
		newID: func() string { next++; return fmt.Sprintf("new-%d", next) },
		title: func(typeName, objectName string) string { return typeName + "：" + objectName },
	}
}

var ruleType = interfaces.KnEventType{Code: "low_stock", Name: "低库存", Source: interfaces.KnEventSourceRule, Subject: "sku", Severity: "high"}
var reportType = interfaces.KnEventType{Code: "late_order", Name: "订单超期", Source: interfaces.KnEventSourceReport, Subject: "order", MergeHours: 2}

func actions(changes []change) []string {
	out := []string{}
	for _, entry := range changes {
		out = append(out, entry.event.ObjectKey+":"+entry.transition.Action)
	}
	return out
}

func TestActionsFor(t *testing.T) {
	for _, test := range []struct {
		name  string
		event interfaces.KnEvent
		want  []string
	}{
		{"unverified", interfaces.KnEvent{Status: interfaces.KnEventUnverified}, []string{"verify", "reject"}},
		{"verified", interfaces.KnEvent{Status: interfaces.KnEventVerified}, []string{"close"}},
		{"closed report", interfaces.KnEvent{Status: interfaces.KnEventClosed, Source: interfaces.KnEventSourceReport, EndedAt: 5}, []string{"reopen"}},
		{"closed rule still abnormal", interfaces.KnEvent{Status: interfaces.KnEventClosed, Source: interfaces.KnEventSourceRule}, []string{"reopen"}},
		{"closed rule recovered", interfaces.KnEvent{Status: interfaces.KnEventClosed, Source: interfaces.KnEventSourceRule, EndedAt: 5}, []string{}},
		{"rejected", interfaces.KnEvent{Status: interfaces.KnEventRejected}, []string{}},
		{"duplicate", interfaces.KnEvent{Status: interfaces.KnEventDuplicate}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := actionsFor(&test.event); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("actions %v, want %v", got, test.want)
			}
		})
	}
}

func TestChangeEvent(t *testing.T) {
	for _, test := range []struct {
		name       string
		event      interfaces.KnEvent
		action     string
		reason     string
		wantErr    error
		wantStatus string
		wantEnded  int64
	}{
		{name: "verify", event: interfaces.KnEvent{Status: "unverified", Source: "report"}, action: "verify", wantStatus: "verified"},
		{name: "reject needs a reason", event: interfaces.KnEvent{Status: "unverified"}, action: "reject", reason: "  ", wantErr: errReasonRequired{}},
		{name: "reject", event: interfaces.KnEvent{Status: "unverified"}, action: "reject", reason: "not real", wantStatus: "rejected"},
		{name: "close needs a reason", event: interfaces.KnEvent{Status: "verified"}, action: "close", wantErr: errReasonRequired{}},
		{name: "closing a report ends it", event: interfaces.KnEvent{Status: "verified", Source: "report"}, action: "close", reason: "done", wantStatus: "closed", wantEnded: 100},
		{name: "closing a rule event leaves its period open", event: interfaces.KnEvent{Status: "verified", Source: "rule"}, action: "close", reason: "handled", wantStatus: "closed"},
		{name: "reopen needs a reason", event: interfaces.KnEvent{Status: "closed", Source: "report", EndedAt: 9}, action: "reopen", wantErr: errReasonRequired{}},
		{name: "reopening a report clears its end", event: interfaces.KnEvent{Status: "closed", Source: "report", EndedAt: 9}, action: "reopen", reason: "again", wantStatus: "verified"},
		{name: "a recovered rule event stays closed", event: interfaces.KnEvent{Status: "closed", Source: "rule", EndedAt: 9}, action: "reopen", reason: "x", wantErr: errActionNotAllowed{}},
		{name: "unverified cannot close", event: interfaces.KnEvent{Status: "unverified"}, action: "close", reason: "x", wantErr: errActionNotAllowed{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := test.event
			event.Revision = 1
			entry, err := changeEvent(&event, test.action, test.reason, testOp(100))
			if test.wantErr != nil {
				if err == nil || reflect.TypeOf(err) != reflect.TypeOf(test.wantErr) {
					t.Fatalf("error %v, want %T", err, test.wantErr)
				}
				if event.Status != test.event.Status || event.Revision != 1 {
					t.Fatalf("a refused action changed the event: %+v", event)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if event.Status != test.wantStatus || event.EndedAt != test.wantEnded || event.Revision != 2 || event.UpdateTime != 100 {
				t.Fatalf("event %+v, want status %s ended %d revision 2", event, test.wantStatus, test.wantEnded)
			}
			want := interfaces.KnEventTransition{
				Action: test.action, FromStatus: test.event.Status, ToStatus: test.wantStatus,
				Actor: "u1", ActorType: "user", ActorName: "Alice", Note: test.reason, Time: 100,
			}
			if entry.transition != want {
				t.Fatalf("transition %+v, want %+v", entry.transition, want)
			}
			if test.action == "verify" && (event.VerifiedBy != "u1" || event.VerifiedByName != "Alice" || event.VerifiedAt != 100) {
				t.Fatalf("verifier not recorded: %+v", event)
			}
		})
	}
	var notAllowed errActionNotAllowed
	_, err := changeEvent(&interfaces.KnEvent{Status: "rejected"}, "verify", "", testOp(1))
	if !errors.As(err, &notAllowed) || notAllowed.action != "verify" || notAllowed.status != "rejected" {
		t.Fatalf("conflict %v does not name the action and status", err)
	}
}

func TestReportEvent(t *testing.T) {
	open := func(id, status string, occurred int64) *interfaces.KnEvent {
		return &interfaces.KnEvent{ID: id, EventType: "late_order", ObjectKey: "PO-1", Status: status, OccurredAt: occurred}
	}
	for _, test := range []struct {
		name        string
		eventType   interfaces.KnEventType
		input       interfaces.KnEventReportInput
		open        []*interfaces.KnEvent
		wantStatus  string
		wantMerged  string
		wantActions []string
	}{
		{
			name: "unverified", eventType: reportType,
			input:      interfaces.KnEventReportInput{Title: " PO-1 超期 ", ObjectKey: "PO-1", OccurredAt: 10 * hour},
			wantStatus: "unverified", wantActions: []string{"PO-1:reported"},
		},
		{
			name: "verified by the reporter", eventType: reportType,
			input:      interfaces.KnEventReportInput{Title: "t", ObjectKey: "PO-1", Verify: true},
			wantStatus: "verified", wantActions: []string{"PO-1:reported", "PO-1:verify"},
		},
		{
			name: "a repeat in the window is a duplicate", eventType: reportType,
			input:      interfaces.KnEventReportInput{Title: "t", ObjectKey: "PO-1", OccurredAt: 10 * hour, Verify: true},
			open:       []*interfaces.KnEvent{open("old", "verified", 9*hour), open("older", "unverified", 10*hour)},
			wantStatus: "duplicate", wantMerged: "old", wantActions: []string{"PO-1:reported"},
		},
		{
			name: "outside the window", eventType: reportType,
			input:      interfaces.KnEventReportInput{Title: "t", ObjectKey: "PO-1", OccurredAt: 10 * hour},
			open:       []*interfaces.KnEvent{open("old", "verified", 7*hour)},
			wantStatus: "unverified", wantActions: []string{"PO-1:reported"},
		},
		{
			name: "a closed event takes no duplicates", eventType: reportType,
			input:      interfaces.KnEventReportInput{Title: "t", ObjectKey: "PO-1", OccurredAt: 10 * hour},
			open:       []*interfaces.KnEvent{open("old", "closed", 10*hour)},
			wantStatus: "unverified", wantActions: []string{"PO-1:reported"},
		},
		{
			name: "no merge window", eventType: interfaces.KnEventType{Code: "late_order", Name: "订单超期", Source: "report"},
			input:      interfaces.KnEventReportInput{Title: "t", ObjectKey: "PO-1", OccurredAt: 10 * hour},
			open:       []*interfaces.KnEvent{open("old", "verified", 10*hour)},
			wantStatus: "unverified", wantActions: []string{"PO-1:reported"},
		},
		{
			name: "no object", eventType: reportType,
			input:      interfaces.KnEventReportInput{Title: "t", OccurredAt: 10 * hour},
			open:       []*interfaces.KnEvent{{ID: "old", EventType: "late_order", Status: "verified", OccurredAt: 10 * hour}},
			wantStatus: "unverified", wantActions: []string{":reported"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			event, merged, changes := reportEvent(test.eventType, test.input, test.open, testOp(20*hour))
			if event.Status != test.wantStatus || event.MergedInto != test.wantMerged {
				t.Fatalf("status %s merged %q, want %s %q", event.Status, event.MergedInto, test.wantStatus, test.wantMerged)
			}
			if (merged != nil) != (test.wantMerged != "") || (merged != nil && merged.ID != test.wantMerged) {
				t.Fatalf("merged event %+v, want %q", merged, test.wantMerged)
			}
			if got := actions(changes); !reflect.DeepEqual(got, test.wantActions) {
				t.Fatalf("changes %v, want %v", got, test.wantActions)
			}
			if changes[0].transition.ToStatus != map[bool]string{true: "duplicate", false: "unverified"}[test.wantMerged != ""] {
				t.Fatalf("reported transition %+v", changes[0].transition)
			}
			if event.ReportedBy != "u1" || event.ReportedByName != "Alice" || event.Source != "report" ||
				event.EventTypeName != test.eventType.Name || event.Revision != 1 || event.RecordedAt != 20*hour {
				t.Fatalf("event %+v", event)
			}
			if test.input.OccurredAt == 0 && event.OccurredAt != 20*hour {
				t.Fatalf("occurred_at %d, want the recording time", event.OccurredAt)
			}
			if test.input.ObjectKey != "" && (event.ObjectType != test.eventType.Subject || event.ObjectName != test.input.ObjectKey) {
				t.Fatalf("object %s/%s, want %s/%s", event.ObjectType, event.ObjectName, test.eventType.Subject, test.input.ObjectKey)
			}
			if test.wantStatus == "verified" && (event.VerifiedBy != "u1" || event.VerifiedByType != "user" || event.VerifiedAt != 20*hour) {
				t.Fatalf("the reporter did not verify: %+v", event)
			}
		})
	}
}

func ruleEvent(key, status, version string, ended int64) *interfaces.KnEvent {
	return &interfaces.KnEvent{
		ID: "ev-" + key, EventType: "low_stock", Source: "rule", ObjectKey: key, Status: status,
		RuleVersion: version, EndedAt: ended, Revision: 1,
		ObjectAttributes: map[string]interfaces.KnEventAttribute{
			"warehouse": {Key: "W1", Label: "一号仓"}, "owner": {Key: "P1", Label: "张三"},
		},
	}
}

func TestApplyRuleCheck(t *testing.T) {
	ok := func(version string, keys ...string) interfaces.KnEventRuleCheckInput {
		input := interfaces.KnEventRuleCheckInput{EventType: "low_stock", RuleVersion: version, Status: "ok"}
		for _, key := range keys {
			input.Hits = append(input.Hits, interfaces.KnEventRuleHit{Key: key})
		}
		return input
	}
	okCheck := &interfaces.KnEventRuleCheck{Status: "ok", Time: 50}
	for _, test := range []struct {
		name        string
		previous    *interfaces.KnEventRuleCheck
		mine        []*interfaces.KnEvent
		input       interfaces.KnEventRuleCheckInput
		wantActions []string
		wantBacklog bool
		wantHits    int
		wantStatus  map[string]string
	}{
		{
			name: "the first decision finds a backlog", input: ok("v1", "A", "B", "A"),
			wantActions: []string{"A:detected", "B:detected"}, wantBacklog: true, wantHits: 2,
		},
		{
			name: "undecided checks before do not count", previous: &interfaces.KnEventRuleCheck{Status: "unknown", Time: 40},
			input: ok("v1", "A"), wantActions: []string{"A:detected"}, wantBacklog: true, wantHits: 1,
		},
		{
			name: "after a decision hits are new", previous: okCheck, input: ok("v1", "A"),
			wantActions: []string{"A:detected"}, wantHits: 1,
		},
		{
			name: "after an undecided check hits are still new", previous: &interfaces.KnEventRuleCheck{Status: "unknown", LastOK: 30, Time: 40},
			input: ok("v1", "A"), wantActions: []string{"A:detected"}, wantHits: 1,
		},
		{
			name: "a repeated hit refreshes, a miss recovers", previous: okCheck,
			mine:        []*interfaces.KnEvent{ruleEvent("A", "verified", "v1", 0), ruleEvent("B", "verified", "v1", 0)},
			input:       ok("v1", "A", "C"),
			wantActions: []string{"A:checked", "B:recovered", "C:detected"}, wantHits: 2,
			wantStatus: map[string]string{"A": "verified", "B": "closed"},
		},
		{
			name: "a miss after a rule change", previous: okCheck,
			mine:        []*interfaces.KnEvent{ruleEvent("A", "verified", "v1", 0), ruleEvent("B", "verified", "v1", 0)},
			input:       ok("v2", "A"),
			wantActions: []string{"A:checked", "B:recovered-after-update"}, wantHits: 1,
			wantStatus: map[string]string{"A": "verified", "B": "closed"},
		},
		{
			name: "a handled event recovers when the rule says so", previous: okCheck,
			mine:        []*interfaces.KnEvent{ruleEvent("A", "closed", "v1", 0), ruleEvent("B", "closed", "v1", 0)},
			input:       ok("v1", "A"),
			wantActions: []string{"A:checked", "B:recovered"}, wantHits: 1,
			wantStatus: map[string]string{"A": "closed", "B": "closed"},
		},
		{
			name: "only covered objects recover", previous: okCheck,
			mine: []*interfaces.KnEvent{ruleEvent("A", "verified", "v1", 0), ruleEvent("B", "verified", "v1", 0)},
			input: func() interfaces.KnEventRuleCheckInput {
				input := ok("v1")
				input.Covered = []string{"B"}
				return input
			}(),
			wantActions: []string{"B:recovered"},
			wantStatus:  map[string]string{"A": "verified", "B": "closed"},
		},
		{
			name: "an empty covered list reads nothing", previous: okCheck,
			mine: []*interfaces.KnEvent{ruleEvent("A", "verified", "v1", 0)},
			input: func() interfaces.KnEventRuleCheckInput {
				input := ok("v1")
				input.Covered = []string{}
				return input
			}(),
			wantActions: []string{}, wantStatus: map[string]string{"A": "verified"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			changes, check := applyRuleCheck(ruleType, test.previous, test.mine, test.input, testOp(100))
			if got := actions(changes); !reflect.DeepEqual(got, test.wantActions) {
				t.Fatalf("changes %v, want %v", got, test.wantActions)
			}
			for _, entry := range changes {
				event := entry.event
				if entry.transition.ActorType != "rule" || entry.transition.Time != 100 {
					t.Fatalf("transition %+v is not the rule's", entry.transition)
				}
				switch entry.transition.Action {
				case actionDetected:
					if event.Status != "verified" || event.VerifiedByType != "rule" || event.Backlog != test.wantBacklog ||
						event.Title != "低库存："+event.ObjectKey || event.ObjectType != "sku" || event.Severity != "high" ||
						event.FirstDetectedAt != 100 || event.CheckStatus != "ok" || event.EventTypeName != "低库存" ||
						event.RuleVersion != test.input.RuleVersion || event.ObjectAttributes == nil {
						t.Fatalf("detected event %+v", event)
					}
				case actionRecovered, actionRecoveredAfterUpdate:
					if event.EndedAt != 100 || event.CheckStatus != "ok" || event.Revision != 2 {
						t.Fatalf("recovered event %+v", event)
					}
				case interfaces.KnEventChecked:
					if event.EndedAt != 0 || event.LastCheckedAt != 100 || event.RuleVersion != test.input.RuleVersion || event.Revision != 2 {
						t.Fatalf("checked event %+v", event)
					}
				}
			}
			for _, event := range test.mine {
				if want, listed := test.wantStatus[event.ObjectKey]; listed && event.Status != want {
					t.Fatalf("%s status %s, want %s", event.ObjectKey, event.Status, want)
				}
			}
			want := interfaces.KnEventRuleCheck{EventType: "low_stock", Status: "ok", RuleVersion: test.input.RuleVersion,
				Hits: test.wantHits, LastOK: 100, CheckedBy: "u1", Time: 100}
			if check != want {
				t.Fatalf("check %+v, want %+v", check, want)
			}
		})
	}
}

func TestRuleHitMergesAttributes(t *testing.T) {
	event := ruleEvent("A", "verified", "v1", 0)
	input := interfaces.KnEventRuleCheckInput{EventType: "low_stock", RuleVersion: "v1", Status: "ok", Hits: []interfaces.KnEventRuleHit{{
		Key: "A", Name: "SKU A",
		Attributes: map[string]interfaces.KnEventAttribute{"warehouse": {Key: "W2", Label: "二号仓"}, "region": {Key: "R1", Label: "华东"}},
	}}}
	changes, _ := applyRuleCheck(ruleType, &interfaces.KnEventRuleCheck{Status: "ok", Time: 1}, []*interfaces.KnEvent{event}, input, testOp(100))
	want := map[string]interfaces.KnEventAttribute{
		"warehouse": {Key: "W2", Label: "二号仓"}, "owner": {Key: "P1", Label: "张三"}, "region": {Key: "R1", Label: "华东"},
	}
	if len(changes) != 1 || !reflect.DeepEqual(event.ObjectAttributes, want) {
		t.Fatalf("attributes %v, want %v", event.ObjectAttributes, want)
	}
}

func TestUndecidedRuleCheckIsNotARecovery(t *testing.T) {
	mine := []*interfaces.KnEvent{ruleEvent("A", "verified", "v1", 0), ruleEvent("B", "closed", "v1", 0)}
	previous := &interfaces.KnEventRuleCheck{Status: "ok", Time: 60}
	input := interfaces.KnEventRuleCheckInput{EventType: "low_stock", RuleVersion: "v2", Status: "unknown", Message: "source offline",
		Hits: []interfaces.KnEventRuleHit{{Key: "C"}}}
	changes, check := applyRuleCheck(ruleType, previous, mine, input, testOp(100))
	if got := actions(changes); !reflect.DeepEqual(got, []string{"A:checked", "B:checked"}) {
		t.Fatalf("changes %v", got)
	}
	for _, event := range mine {
		if event.EndedAt != 0 || event.CheckStatus != "unknown" || event.LastCheckedAt != 100 || event.RuleVersion != "v1" {
			t.Fatalf("undecided check changed %+v", event)
		}
	}
	if mine[0].Status != "verified" || mine[1].Status != "closed" {
		t.Fatalf("statuses changed: %s %s", mine[0].Status, mine[1].Status)
	}
	want := interfaces.KnEventRuleCheck{EventType: "low_stock", Status: "unknown", RuleVersion: "v2", Message: "source offline",
		LastOK: 60, CheckedBy: "u1", Time: 100}
	if check != want {
		t.Fatalf("check %+v, want %+v", check, want)
	}
	// The next undecided check keeps when the rule last decided.
	_, again := applyRuleCheck(ruleType, &check, nil, input, testOp(200))
	if again.LastOK != 60 {
		t.Fatalf("last_ok %d, want 60", again.LastOK)
	}
}

func TestRuleEventTitleIsClipped(t *testing.T) {
	long := interfaces.KnEventRuleCheckInput{EventType: "low_stock", Status: "ok", Hits: []interfaces.KnEventRuleHit{{Key: "k", Name: string(make([]rune, 300))}}}
	changes, _ := applyRuleCheck(ruleType, nil, nil, long, testOp(1))
	if got := len([]rune(changes[0].event.Title)); got != interfaces.KnEventTitleMax {
		t.Fatalf("title has %d characters, want %d", got, interfaces.KnEventTitleMax)
	}
	unnamed := interfaces.KnEventRuleCheckInput{EventType: "low_stock", Status: "ok", Hits: []interfaces.KnEventRuleHit{{Key: "SKU-9"}}}
	changes, _ = applyRuleCheck(ruleType, nil, nil, unnamed, testOp(1))
	if changes[0].event.Title != "低库存：SKU-9" || changes[0].event.ObjectName != "SKU-9" {
		t.Fatalf("unnamed hit: title %q name %q", changes[0].event.Title, changes[0].event.ObjectName)
	}
}
