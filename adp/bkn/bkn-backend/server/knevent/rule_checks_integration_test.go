// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build integration

package knevent

import (
	"context"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// ruleCheckService is the service with a rule tool, at 2026-11-15 08:00 in
// Shanghai, an hour before the rule's first check.
func ruleCheckService(t *testing.T, tools *toolsStub) (*service, *clock) {
	s, _, c := integrationService(t)
	s.tools = tools
	c.now.Store(at.UnixMilli())
	return s, c
}

// nine is 09:00 in Shanghai on the day after at, plus days.
func nine(days int) int64 {
	return time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC).AddDate(0, 0, days).UnixMilli()
}

func openKeys(t *testing.T, s *service, kn string) []string {
	t.Helper()
	list := must(s.ListEvents(userContext(), kn, interfaces.KnEventFilter{EventTypes: []string{"late"}}))
	keys := []string{}
	for _, event := range list.Entries {
		if event.EndedAt == 0 {
			keys = append(keys, event.ObjectKey)
		}
	}
	sort.Strings(keys)
	return keys
}

func lastCheck(t *testing.T, s *service, kn string) interfaces.KnEventRuleCheck {
	t.Helper()
	for _, check := range must(s.ListRuleChecks(userContext(), kn)) {
		if check.EventType == "late" {
			return check
		}
	}
	t.Fatal("the rule has no check")
	return interfaces.KnEventRuleCheck{}
}

func reply(keys ...string) *interfaces.CallerToolReply {
	entries := []string{}
	for _, key := range keys {
		entries = append(entries, `{"key":"`+key+`","name":"Order `+key+`"}`)
	}
	return tableReply(`{"columns":[{"name":"key"},{"name":"name"}],"entries":[` + strings.Join(entries, ",") + `]}`)
}

func TestIntegrationARuleWithAToolIsCheckedOnItsSchedule(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: reply("o1", "o2")}
	s, c := ruleCheckService(t, tools)
	ctx := userContext()
	checker := newRuleChecker(s)

	created := must(s.CreateEventType(ctx, "kn1", ruleInput()))
	if created.Trial == nil || created.Trial.Hits != 2 || created.NextCheckAt != nine(0) ||
		created.CheckSubject == nil || created.CheckSubject.ID != "u1" {
		t.Fatalf("created %+v", created)
	}
	stored := must(s.GetEventType(ctx, "kn1", "late"))
	if stored.Trial != nil || !sameChecks(stored, created) || stored.CheckSubject.Name != "Alice" ||
		stored.RuleVersion != ruleVersion(*created.RuleTool, 7) || stored.NextCheckAt != nine(0) {
		t.Fatalf("stored %+v", stored)
	}
	// The trial wrote nothing.
	if keys := openKeys(t, s, "kn1"); len(keys) != 0 {
		t.Fatalf("the trial opened %v", keys)
	}

	// Not due yet.
	checker.round(context.Background())
	if len(tools.arguments) != 1 {
		t.Fatalf("checked before its time: %v", tools.arguments)
	}
	c.now.Store(nine(0) + 1000)
	tools.accounts = nil
	checker.round(context.Background())
	if keys := openKeys(t, s, "kn1"); !reflect.DeepEqual(keys, []string{"o1", "o2"}) {
		t.Fatalf("open %v", keys)
	}
	if !reflect.DeepEqual(tools.accounts, []string{"user:u1", "user:u1"}) {
		t.Fatalf("checked as %v, want the person who saved the rule", tools.accounts)
	}
	check := lastCheck(t, s, "kn1")
	if check.Status != interfaces.KnEventCheckOK || check.CheckedBy != "u1" || check.Hits != 2 ||
		check.RuleVersion != stored.RuleVersion || check.Time != nine(0)+1000 {
		t.Fatalf("check %+v", check)
	}
	if got := must(s.GetEventType(ctx, "kn1", "late")); got.NextCheckAt != nine(1) || got.CheckFailures != 0 {
		t.Fatalf("after the check: next %d failures %d", got.NextCheckAt, got.CheckFailures)
	}
	// Only once a time: the next round finds nothing due.
	checker.round(context.Background())
	if len(tools.arguments) != 2 {
		t.Fatalf("checked %d times", len(tools.arguments))
	}

	// The tool changed in the execution factory: a new version, and what the
	// rule no longer finds recovers after the update.
	tools.tool.UpdateTime = 8
	tools.reply = reply("o1")
	c.now.Store(nine(1))
	checker.round(context.Background())
	if keys := openKeys(t, s, "kn1"); !reflect.DeepEqual(keys, []string{"o1"}) {
		t.Fatalf("open %v", keys)
	}
	feed := must(s.ListChanges(context.Background(), "kn1", 0, 0))
	if actions := changeActions(feed); !contains(actions, "o2:"+actionRecoveredAfterUpdate) {
		t.Fatalf("feed %v", actions)
	}

	// Applications may not check the same rule.
	_, err := s.RecordRuleCheck(ctx, "kn1", interfaces.KnEventRuleCheckInput{EventType: "late", Status: "ok"})
	if statusOf(t, err) != http.StatusConflict || errorCode(t, err) != berrors.BknBackend_KnEvent_CheckedByPlatform {
		t.Fatalf("an application's check: %v", err)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func TestIntegrationACheckThatCannotDecideLeavesTheEventsAndRetries(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: reply("o1", "o2")}
	s, c := ruleCheckService(t, tools)
	checker := newRuleChecker(s)
	must(s.CreateEventType(userContext(), "kn1", ruleInput()))
	c.now.Store(nine(0))
	checker.round(context.Background())

	// The subject lost the data.
	tools.reply = &interfaces.CallerToolReply{StatusCode: http.StatusForbidden, Error: "no access"}
	start := nine(1)
	for failures, wait := range []int64{1, 2, 4} {
		c.now.Store(start)
		checker.round(context.Background())
		got := must(s.GetEventType(userContext(), "kn1", "late"))
		if got.CheckFailures != failures+1 || got.NextCheckAt != start+wait*60_000 {
			t.Fatalf("failure %d: failures %d, next in %d ms", failures+1, got.CheckFailures, got.NextCheckAt-start)
		}
		start = got.NextCheckAt
	}
	check := lastCheck(t, s, "kn1")
	if check.Status != interfaces.KnEventCheckUnknown || check.Message != checkText("NoPermission", nil, "") || check.LastOK != nine(0) {
		t.Fatalf("check %+v", check)
	}
	if keys := openKeys(t, s, "kn1"); !reflect.DeepEqual(keys, []string{"o1", "o2"}) {
		t.Fatalf("an undecided check closed events: open %v", keys)
	}

	// Decides again: the count goes back to zero and the next time is the schedule's.
	tools.reply = reply("o1")
	c.now.Store(start)
	checker.round(context.Background())
	got := must(s.GetEventType(userContext(), "kn1", "late"))
	if got.CheckFailures != 0 || got.NextCheckAt != nine(2) {
		t.Fatalf("after a check that decided: failures %d next %d", got.CheckFailures, got.NextCheckAt)
	}
	if keys := openKeys(t, s, "kn1"); !reflect.DeepEqual(keys, []string{"o1"}) {
		t.Fatalf("open %v", keys)
	}
}

func TestIntegrationAManualCheckRunsAsTheSubjectAndKeepsTheSchedule(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: reply("o1")}
	s, _ := ruleCheckService(t, tools)
	must(s.CreateEventType(userContext(), "kn1", ruleInput()))
	tools.accounts = nil

	other := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "u2", Type: "user"})
	result := must(s.CheckEventType(other, "kn1", "late"))
	if len(result.Events) != 1 || result.Check.CheckedBy != "u1" {
		t.Fatalf("result %+v", result)
	}
	if !reflect.DeepEqual(tools.accounts, []string{"user:u1", "user:u1"}) {
		t.Fatalf("checked as %v", tools.accounts)
	}
	if got := must(s.GetEventType(userContext(), "kn1", "late")); got.NextCheckAt != nine(0) {
		t.Fatalf("a manual check moved the next check to %d", got.NextCheckAt)
	}

	// A type checked by the applications cannot be checked by the platform.
	must(s.CreateEventType(userContext(), "kn1", interfaces.KnEventTypeInput{Code: "low", Name: "Low", Source: "rule"}))
	if _, err := s.CheckEventType(userContext(), "kn1", "low"); statusOf(t, err) != http.StatusBadRequest {
		t.Fatalf("checking a type without a tool: %v", err)
	}
}

func TestIntegrationChecksOfATypeDoNotOverlap(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: reply("o1")}
	s, c := ruleCheckService(t, tools)
	must(s.CreateEventType(userContext(), "kn1", ruleInput()))
	c.now.Store(nine(0))
	checks := len(tools.arguments)

	// A check of the type is running elsewhere.
	ran, err := s.store.alone(context.Background(), checkLock("kn1", "late"), func() error {
		if _, err := s.CheckEventType(userContext(), "kn1", "late"); statusOf(t, err) != http.StatusConflict ||
			errorCode(t, err) != berrors.BknBackend_KnEvent_RuleChecking {
			t.Errorf("a second check: %v", err)
		}
		newRuleChecker(s).round(context.Background())
		return nil
	})
	if err != nil || !ran {
		t.Fatal(ran, err)
	}
	if len(tools.arguments) != checks {
		t.Fatal("a check ran while another did")
	}
	// The scheduled check left it due, and takes it now.
	if got := must(s.GetEventType(userContext(), "kn1", "late")); got.NextCheckAt != nine(0) {
		t.Fatalf("next %d", got.NextCheckAt)
	}

	// Another replica is starting the scheduled checks.
	ran, err = s.store.alone(context.Background(), ruleCheckLock, func() error {
		newRuleChecker(s).round(context.Background())
		return nil
	})
	if err != nil || !ran || len(tools.arguments) != checks {
		t.Fatalf("two replicas started checks: %v %v %d", ran, err, len(tools.arguments)-checks)
	}
	newRuleChecker(s).round(context.Background())
	if len(tools.arguments) != checks+1 {
		t.Fatalf("the due check did not run: %d", len(tools.arguments)-checks)
	}
}

func TestIntegrationManyDueChecksRunAtOnce(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: reply("o1")}
	s, c := ruleCheckService(t, tools)
	for _, code := range []string{"a", "b", "c", "d", "e", "f"} {
		input := ruleInput()
		input.Code = code
		must(s.CreateEventType(userContext(), "kn1", input))
	}
	c.now.Store(nine(0))
	newRuleChecker(s).round(context.Background())
	for _, code := range []string{"a", "b", "c", "d", "e", "f"} {
		if got := must(s.GetEventType(userContext(), "kn1", code)); got.NextCheckAt != nine(1) {
			t.Fatalf("%s: next %d", code, got.NextCheckAt)
		}
	}
	if len(must(s.ListRuleChecks(userContext(), "kn1"))) != 6 {
		t.Fatal("not every due rule was checked")
	}
}

// changingTools saves the rule again while its tool runs, once.
type changingTools struct {
	*toolsStub
	once   sync.Once
	change func()
}

func (c *changingTools) ExecuteCallerTool(ctx context.Context, box, tool string, arguments map[string]any, wait time.Duration) (*interfaces.CallerToolReply, error) {
	reply, err := c.toolsStub.ExecuteCallerTool(ctx, box, tool, arguments, wait)
	if account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo); account.ID == "u1" {
		c.once.Do(c.change)
	}
	return reply, err
}

func TestIntegrationARuleChangedDuringItsCheckIsNotRecorded(t *testing.T) {
	stub := &toolsStub{tool: cypherTool(), reply: reply("o1")}
	s, _ := ruleCheckService(t, stub)
	must(s.CreateEventType(userContext(), "kn1", ruleInput()))
	tools := &changingTools{toolsStub: stub}
	s.tools = tools
	// u2 moves the rule to 10:00 while u1's check runs.
	other := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "u2", Type: "user"})
	tools.change = func() {
		moved := ruleInput()
		moved.CheckSchedule.Cron = "0 10 * * *"
		must(s.UpdateEventType(other, "kn1", "late", moved))
	}
	if _, err := s.CheckEventType(userContext(), "kn1", "late"); statusOf(t, err) != http.StatusConflict {
		t.Fatalf("a check of a rule that changed: %v", err)
	}
	if checks := must(s.ListRuleChecks(userContext(), "kn1")); len(checks) != 0 {
		t.Fatalf("recorded %+v", checks)
	}
	got := must(s.GetEventType(userContext(), "kn1", "late"))
	if got.CheckSubject.ID != "u2" || got.CheckSchedule.Cron != "0 10 * * *" ||
		got.NextCheckAt != time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("the save was undone: %+v", got)
	}
}

func TestIntegrationRemovingTheToolStopsTheChecks(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: reply("o1")}
	s, c := ruleCheckService(t, tools)
	must(s.CreateEventType(userContext(), "kn1", ruleInput()))
	c.now.Store(nine(0))
	newRuleChecker(s).round(context.Background())
	calls := len(tools.arguments)

	updated := must(s.UpdateEventType(userContext(), "kn1", "late", interfaces.KnEventTypeInput{Name: "Late order", RuleVersion: "v-app"}))
	if updated.RuleTool != nil || updated.NextCheckAt != 0 || updated.CheckSubject != nil || updated.RuleVersion != "v-app" {
		t.Fatalf("updated %+v", updated)
	}
	c.now.Store(nine(5))
	newRuleChecker(s).round(context.Background())
	if len(tools.arguments) != calls {
		t.Fatal("a rule without a tool was checked")
	}
	// The open event stays until the applications check again.
	if keys := openKeys(t, s, "kn1"); !reflect.DeepEqual(keys, []string{"o1"}) {
		t.Fatalf("open %v", keys)
	}
	must(s.RecordRuleCheck(userContext(), "kn1", interfaces.KnEventRuleCheckInput{EventType: "late", Status: "ok", RuleVersion: "v-app"}))
	if keys := openKeys(t, s, "kn1"); len(keys) != 0 {
		t.Fatalf("open %v", keys)
	}
}
