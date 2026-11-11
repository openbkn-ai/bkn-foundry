// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build integration

package knevent

// These tests run the service against MariaDB with the platform's migration
// scripts of the event tables, in the order they are applied:
//
//	M=<bkn-foundry>/migrations/bkn-backend/mariadb/0.2.2
//	KN_EVENT_TEST_MIGRATION=$M/01-kn-events.sql:$M/03-kn-event-class.sql \
//	KN_EVENT_TEST_DB=127.0.0.1:13306 I18N_MODE_UT=true \
//	go test -tags integration ./knevent/
//
// Each test creates its own database and drops it afterwards.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// applyScript runs a migration script statement by statement.
func applyScript(t *testing.T, db *sql.DB, script string) {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(strings.ToUpper(trimmed), "USE ") {
			continue
		}
		lines = append(lines, line)
	}
	for _, statement := range strings.Split(strings.Join(lines, "\n"), ";\n") {
		if statement = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(statement), ";")); statement != "" {
			if _, err := db.Exec(statement); err != nil {
				t.Fatalf("migration statement failed: %v\n%s", err, statement)
			}
		}
	}
}

func integrationDB(t *testing.T) *sql.DB {
	t.Helper()
	paths := os.Getenv("KN_EVENT_TEST_MIGRATION")
	if paths == "" {
		t.Skip("KN_EVENT_TEST_MIGRATION is not set")
	}
	var scripts []string
	for _, path := range filepath.SplitList(paths) {
		script, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		scripts = append(scripts, string(script))
	}
	host, portText, err := net.SplitHostPort(envOr("KN_EVENT_TEST_DB", "127.0.0.1:13306"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	setting := libdb.DBSetting{Host: host, Port: port,
		Username: envOr("KN_EVENT_TEST_DB_USER", "root"), Password: envOr("KN_EVENT_TEST_DB_PASSWORD", "root")}
	admin := libdb.InitDB(&setting)
	name := fmt.Sprintf("knev_it_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	setting.DBName = name
	db := libdb.InitDB(&setting)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP DATABASE " + name)
		_ = admin.Close()
	})
	// Upgrade scripts must survive a second run.
	for _, script := range scripts {
		applyScript(t, db, script)
		applyScript(t, db, script)
	}
	return db
}

type clock struct{ now atomic.Int64 }

func (c *clock) tick(by int64) int64 { return c.now.Add(by) }

func integrationService(t *testing.T) (*service, *sql.DB, *clock) {
	db := integrationDB(t)
	c := &clock{}
	c.now.Store(1_000_000)
	var next atomic.Int64
	classes, _, _ := newTestClasses()
	s := &service{
		store:       newStore(db),
		classes:     classes,
		permissions: &permissionsStub{},
		networks:    networksStub{"kn1": true, "kn2": true},
		names:       namesStub{name: "Alice"},
		now:         func() int64 { return c.now.Load() },
		newID:       func() string { return fmt.Sprintf("ev-%06d", next.Add(1)) },
	}
	return s, db, c
}

// must stops the test on an unexpected error; it panics because Go cannot
// pass a call's two results alongside *testing.T.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func changeActions(list interfaces.KnEventChangeList) []string {
	out := []string{}
	for _, entry := range list.Entries {
		out = append(out, entry.Event.ObjectKey+":"+entry.Action)
	}
	return out
}

func createTypes(t *testing.T, s *service, ctx context.Context, kn string) {
	t.Helper()
	must(s.CreateEventType(ctx, kn, interfaces.KnEventTypeInput{Code: "late_order", Name: "订单超期", Source: "report", Subject: "order", MergeHours: 1}))
	must(s.CreateEventType(ctx, kn, interfaces.KnEventTypeInput{Code: "low_stock", Name: "低库存", Source: "rule", Subject: "sku", Severity: "high",
		Rule: json.RawMessage(`{"where":"qty < 10"}`), RuleVersion: "v1"}))
}

func TestIntegrationEventTypes(t *testing.T) {
	s, _, _ := integrationService(t)
	ctx := userContext()
	createTypes(t, s, ctx, "kn1")
	if _, err := s.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "late_order", Name: "again", Source: "report"}); statusOf(t, err) != 409 {
		t.Fatalf("duplicate code: %v", err)
	}
	types := must(s.ListEventTypes(ctx, "kn1"))
	if len(types) != 2 || types[0].Code != "late_order" || types[1].Code != "low_stock" ||
		string(types[1].Rule) != `{"where": "qty < 10"}` && string(types[1].Rule) != `{"where":"qty < 10"}` || types[0].Creator != "u1" {
		t.Fatalf("types %+v", types)
	}
	if string(types[0].Rule) != "{}" {
		t.Fatalf("an absent rule reads %s", types[0].Rule)
	}
	if _, err := s.UpdateEventType(ctx, "kn1", "low_stock", interfaces.KnEventTypeInput{Name: "x", Source: "report"}); statusOf(t, err) != 400 {
		t.Fatalf("changing the source: %v", err)
	}
	updated := must(s.UpdateEventType(ctx, "kn1", "low_stock", interfaces.KnEventTypeInput{Name: "库存不足", RuleVersion: "v2"}))
	if updated.Source != "rule" || updated.CreateTime != types[1].CreateTime || updated.RuleVersion != "v2" {
		t.Fatalf("updated %+v", updated)
	}
	if got := must(s.GetEventType(ctx, "kn1", "low_stock")); got.Name != "库存不足" || string(got.Rule) != "{}" {
		t.Fatalf("stored %+v", got)
	}
	if _, err := s.GetEventType(ctx, "kn2", "low_stock"); statusOf(t, err) != 404 {
		t.Fatalf("type of another network: %v", err)
	}
}

func TestIntegrationReportedEvents(t *testing.T) {
	s, _, c := integrationService(t)
	ctx := userContext()
	createTypes(t, s, ctx, "kn1")
	if _, err := s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "low_stock", Title: "x"}); statusOf(t, err) != 409 {
		t.Fatalf("reporting a rule type: %v", err)
	}
	if _, err := s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "nope", Title: "x"}); statusOf(t, err) != 404 {
		t.Fatalf("unknown type: %v", err)
	}
	first := must(s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "late_order", Title: "PO-1 超期", ObjectKey: "PO-1",
		ObjectAttributes: map[string]interfaces.KnEventAttribute{"customer": {Key: "C001", Label: "某客户"}}, Detail: json.RawMessage(`{"days":3}`), Verify: true}))
	c.tick(10 * 60 * 1000)
	repeat := must(s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "late_order", Title: "PO-1 又超期", ObjectKey: "PO-1"}))
	if len(repeat.Events) != 2 || repeat.Events[0].Status != "duplicate" || repeat.Events[0].MergedInto != first.Events[0].ID ||
		repeat.Events[1].ID != first.Events[0].ID || repeat.Events[1].EventTypeName != "订单超期" {
		t.Fatalf("repeat %+v", repeat.Events)
	}
	c.tick(1)
	other := must(s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "late_order", Title: "PO-2", ObjectKey: "PO-2"}))

	event := must(s.GetEvent(ctx, "kn1", first.Events[0].ID))
	if event.Status != "verified" || event.VerifiedByName != "Alice" || event.ReportedByName != "Alice" ||
		event.ObjectAttributes["customer"].Label != "某客户" || event.ObjectType != "order" || string(event.Detail) != `{"days": 3}` && string(event.Detail) != `{"days":3}` ||
		!reflect.DeepEqual(event.Actions, []string{"close"}) {
		t.Fatalf("event %+v", event)
	}
	if len(event.History) != 2 || event.History[0].Action != "reported" || event.History[1].Action != "verify" || event.History[1].ActorName != "Alice" {
		t.Fatalf("history %+v", event.History)
	}
	if _, err := s.GetEvent(ctx, "kn2", first.Events[0].ID); statusOf(t, err) != 404 {
		t.Fatalf("event of another network: %v", err)
	}

	// Actions on the unverified report.
	if _, err := s.ChangeEvent(ctx, "kn1", other.Events[0].ID, "reject", interfaces.KnEventReasonInput{}); statusOf(t, err) != 400 {
		t.Fatalf("reject without a reason: %v", err)
	}
	if _, err := s.ChangeEvent(ctx, "kn1", other.Events[0].ID, "close", interfaces.KnEventReasonInput{Reason: "x"}); statusOf(t, err) != 409 {
		t.Fatalf("closing an unverified event: %v", err)
	}
	c.tick(1)
	must(s.ChangeEvent(ctx, "kn1", other.Events[0].ID, "verify", interfaces.KnEventReasonInput{}))
	c.tick(1)
	closed := must(s.ChangeEvent(ctx, "kn1", other.Events[0].ID, "close", interfaces.KnEventReasonInput{Reason: "shipped"}))
	if closed.Events[0].EndedAt != c.now.Load() || !reflect.DeepEqual(closed.Events[0].Actions, []string{"reopen"}) || closed.Events[0].Revision != 3 {
		t.Fatalf("closed %+v", closed.Events[0])
	}
	c.tick(1)
	reopened := must(s.ChangeEvent(ctx, "kn1", other.Events[0].ID, "reopen", interfaces.KnEventReasonInput{Reason: "not shipped"}))
	if reopened.Events[0].Status != "verified" || reopened.Events[0].EndedAt != 0 {
		t.Fatalf("reopened %+v", reopened.Events[0])
	}

	// Paging, newest first, with filters.
	page := must(s.ListEvents(ctx, "kn1", interfaces.KnEventFilter{Limit: 2}))
	if len(page.Entries) != 2 || page.Entries[0].ObjectKey != "PO-2" || page.Entries[1].Status != "duplicate" || page.NextCursor == "" {
		t.Fatalf("first page %+v", page)
	}
	rest := must(s.ListEvents(ctx, "kn1", interfaces.KnEventFilter{Limit: 2, Cursor: page.NextCursor}))
	if len(rest.Entries) != 1 || rest.Entries[0].ID != first.Events[0].ID || rest.NextCursor != "" || rest.Entries[0].History != nil {
		t.Fatalf("last page %+v", rest)
	}
	filtered := must(s.ListEvents(ctx, "kn1", interfaces.KnEventFilter{Statuses: []string{"duplicate", "unverified"}, ObjectKey: "PO-1"}))
	if len(filtered.Entries) != 1 || filtered.Entries[0].Status != "duplicate" {
		t.Fatalf("filtered %+v", filtered)
	}
	if none := must(s.ListEvents(ctx, "kn1", interfaces.KnEventFilter{EventTypes: []string{"low_stock"}})); len(none.Entries) != 0 || none.Entries == nil {
		t.Fatalf("other type %+v", none)
	}

	// The type's current name reaches every event, history keeps its own names.
	must(s.UpdateEventType(ctx, "kn1", "late_order", interfaces.KnEventTypeInput{Name: "交期延误", MergeHours: 1}))
	changes := must(s.ListChanges(context.Background(), "kn1", 0, 0))
	want := []string{"PO-1:reported", "PO-1:verify", "PO-1:reported", "PO-2:reported", "PO-2:verify", "PO-2:close", "PO-2:reopen"}
	if got := changeActions(changes); !reflect.DeepEqual(got, want) {
		t.Fatalf("changes %v, want %v", got, want)
	}
	for index, entry := range changes.Entries {
		if entry.Seq != int64(index+1) || entry.Event.EventTypeName != "交期延误" || entry.Event.History != nil {
			t.Fatalf("change %d: %+v", index, entry)
		}
	}
	// Changes of one event carry its state now, not when they were written.
	if changes.Entries[5].Event.Status != "verified" || changes.Entries[5].Event.Revision != 4 {
		t.Fatalf("close change carries %+v", changes.Entries[5].Event)
	}
	if changes.LastSeq != 7 {
		t.Fatalf("last_seq %d", changes.LastSeq)
	}

	if err := s.DeleteEventType(ctx, "kn1", "late_order"); statusOf(t, err) != 409 {
		t.Fatalf("deleting a type in use: %v", err)
	}
	if err := s.DeleteEventType(ctx, "kn1", "missing"); statusOf(t, err) != 404 {
		t.Fatalf("deleting a missing type: %v", err)
	}
}

func TestIntegrationRuleChecks(t *testing.T) {
	s, _, c := integrationService(t)
	ctx := userContext()
	createTypes(t, s, ctx, "kn1")
	hit := func(key, warehouse string) interfaces.KnEventRuleHit {
		return interfaces.KnEventRuleHit{Key: key, Attributes: map[string]interfaces.KnEventAttribute{"warehouse": {Key: warehouse, Label: warehouse}}}
	}
	check := func(input interfaces.KnEventRuleCheckInput) interfaces.KnEventRuleCheckResult {
		t.Helper()
		c.tick(60 * 1000)
		input.EventType = "low_stock"
		return must(s.RecordRuleCheck(ctx, "kn1", input))
	}
	if _, err := s.RecordRuleCheck(ctx, "kn1", interfaces.KnEventRuleCheckInput{EventType: "late_order", Status: "ok"}); statusOf(t, err) != 409 {
		t.Fatalf("checking a report type: %v", err)
	}

	undecided := check(interfaces.KnEventRuleCheckInput{Status: "unknown", Message: "source offline"})
	if len(undecided.Events) != 0 || undecided.Check.LastOK != 0 {
		t.Fatalf("undecided %+v", undecided)
	}
	backlog := check(interfaces.KnEventRuleCheckInput{Status: "ok", RuleVersion: "v1", Hits: []interfaces.KnEventRuleHit{hit("A", "W1"), hit("B", "W1")}})
	if len(backlog.Events) != 2 || !backlog.Events[0].Backlog || backlog.Events[0].Title != "低库存：A" || backlog.Events[0].Status != "verified" ||
		backlog.Events[0].EventTypeName != "低库存" || backlog.Check.Hits != 2 {
		t.Fatalf("backlog %+v", backlog)
	}
	next := check(interfaces.KnEventRuleCheckInput{Status: "ok", RuleVersion: "v1",
		Hits: []interfaces.KnEventRuleHit{{Key: "A", Attributes: map[string]interfaces.KnEventAttribute{"region": {Key: "R1"}}}, hit("C", "W2")}})
	if got := []string{next.Events[0].ObjectKey + ":" + next.Events[0].Status, next.Events[1].ObjectKey + ":" + next.Events[1].Status,
		next.Events[2].ObjectKey + ":" + next.Events[2].Status}; !reflect.DeepEqual(got, []string{"A:verified", "B:closed", "C:verified"}) {
		t.Fatalf("second check %v", got)
	}
	if next.Events[2].Backlog || len(next.Events[0].ObjectAttributes) != 2 || next.Events[1].EndedAt != c.now.Load() {
		t.Fatalf("second check events %+v", next.Events)
	}
	check(interfaces.KnEventRuleCheckInput{Status: "unknown"})

	changes := must(s.ListChanges(context.Background(), "kn1", 0, 0))
	want := []string{"A:detected", "B:detected", "A:checked", "B:recovered", "C:detected", "A:checked", "C:checked"}
	if got := changeActions(changes); !reflect.DeepEqual(got, want) {
		t.Fatalf("changes %v, want %v", got, want)
	}
	stored := must(s.GetEvent(ctx, "kn1", next.Events[0].ID))
	if stored.CheckStatus != "unknown" || len(stored.History) != 1 || stored.History[0].Action != "detected" || stored.History[0].ActorType != "rule" {
		t.Fatalf("A after an undecided check %+v", stored)
	}
	checks := must(s.ListRuleChecks(ctx, "kn1"))
	if len(checks) != 1 || checks[0].Status != "unknown" || checks[0].LastOK == 0 || checks[0].LastOK >= checks[0].Time || checks[0].CheckedBy != "u1" {
		t.Fatalf("checks %+v", checks)
	}
}

func TestIntegrationChangeFeedPages(t *testing.T) {
	s, _, _ := integrationService(t)
	ctx := userContext()
	createTypes(t, s, ctx, "kn1")
	createTypes(t, s, ctx, "kn2")
	for index := 0; index < 5; index++ {
		must(s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "late_order", Title: "t", ObjectKey: fmt.Sprint(index)}))
	}
	must(s.ReportEvent(ctx, "kn2", interfaces.KnEventReportInput{EventType: "late_order", Title: "t"}))
	var seen []int64
	after := int64(0)
	for {
		page := must(s.ListChanges(context.Background(), "kn1", after, 2))
		if len(page.Entries) == 0 {
			if page.LastSeq != after {
				t.Fatalf("empty page last_seq %d, want %d", page.LastSeq, after)
			}
			break
		}
		for _, entry := range page.Entries {
			seen = append(seen, entry.Seq)
		}
		after = page.LastSeq
	}
	if !reflect.DeepEqual(seen, []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("seq %v", seen)
	}
	if other := must(s.ListChanges(context.Background(), "kn2", 0, 0)); len(other.Entries) != 1 || other.Entries[0].Seq != 1 {
		t.Fatalf("kn2 feed %+v", other)
	}
	if _, err := s.ListChanges(context.Background(), "gone", 0, 0); statusOf(t, err) != 404 {
		t.Fatalf("missing network: %v", err)
	}
}

// Concurrent writers get contiguous numbers in commit order: a reader
// following the feed meanwhile never sees a gap.
func TestIntegrationConcurrentWritesNumberInCommitOrder(t *testing.T) {
	s, _, _ := integrationService(t)
	ctx := userContext()
	createTypes(t, s, ctx, "kn1")
	const writers, each = 8, 15
	done := make(chan struct{})
	readerErr := make(chan error, 1)
	go func() {
		after := int64(0)
		for {
			page, err := s.ListChanges(context.Background(), "kn1", after, 1000)
			if err != nil {
				readerErr <- err
				return
			}
			for _, entry := range page.Entries {
				if entry.Seq != after+1 {
					readerErr <- fmt.Errorf("after %d the feed showed %d", after, entry.Seq)
					return
				}
				after = entry.Seq
			}
			select {
			case <-done:
				if len(page.Entries) == 0 {
					readerErr <- nil
					return
				}
			default:
			}
		}
	}()
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				var err error
				if i%3 == 0 {
					_, err = s.RecordRuleCheck(ctx, "kn1", interfaces.KnEventRuleCheckInput{EventType: "low_stock", Status: "ok",
						Hits: []interfaces.KnEventRuleHit{{Key: fmt.Sprintf("w%d", w)}}})
				} else {
					_, err = s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "late_order", Title: "t",
						ObjectKey: fmt.Sprintf("w%d-%d", w, i), Verify: i%2 == 0})
				}
				if err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(done)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if err := <-readerErr; err != nil {
		t.Fatal(err)
	}
	feed := must(s.ListChanges(context.Background(), "kn1", 0, 1000))
	for index, entry := range feed.Entries {
		if entry.Seq != int64(index+1) {
			t.Fatalf("seq %d at %d", entry.Seq, index)
		}
	}
	// Each check hit only its writer's object and recovered the others, so
	// serialised checks leave exactly one abnormal period open, and no object
	// ever had two open at once.
	events := must(s.ListEvents(ctx, "kn1", interfaces.KnEventFilter{EventTypes: []string{"low_stock"}, Limit: 200}))
	open := 0
	for _, event := range events.Entries {
		if event.EndedAt == 0 {
			open++
		}
	}
	if open != 1 || len(events.Entries) > writers*((each+2)/3) {
		t.Fatalf("%d rule events, %d open; want one open", len(events.Entries), open)
	}
}

func TestIntegrationNetworkDeletionRemovesEverything(t *testing.T) {
	s, db, _ := integrationService(t)
	ctx := userContext()
	for _, kn := range []string{"kn1", "kn2"} {
		createTypes(t, s, ctx, kn)
		must(s.ReportEvent(ctx, kn, interfaces.KnEventReportInput{EventType: "late_order", Title: "t", ObjectKey: "PO"}))
		must(s.RecordRuleCheck(ctx, kn, interfaces.KnEventRuleCheckInput{EventType: "low_stock", Status: "ok", Hits: []interfaces.KnEventRuleHit{{Key: "A"}}}))
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.DeleteKnEvents(context.Background(), tx, "kn1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{typeTable, eventTable, changeTable, checkTable, headTable} {
		for kn, want := range map[string]bool{"kn1": false, "kn2": true} {
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE f_kn_id = ?", kn).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (count > 0) != want {
				t.Fatalf("%s has %d rows of %s after deleting kn1", table, count, kn)
			}
		}
	}
	// A network created again with the same ID starts a new feed.
	createTypes(t, s, ctx, "kn1")
	must(s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "late_order", Title: "t"}))
	if feed := must(s.ListChanges(context.Background(), "kn1", 0, 0)); len(feed.Entries) != 1 || feed.Entries[0].Seq != 1 {
		t.Fatalf("feed after re-creation %+v", feed)
	}
}

// deletableNetwork is kn1 until it is deleted.
type deletableNetwork struct{ gone atomic.Bool }

func (n *deletableNetwork) CheckKNExistByID(_ context.Context, knID, _ string) (string, bool, error) {
	return knID, knID == "kn1" && !n.gone.Load(), nil
}

func TestIntegrationTypeCreatedDuringNetworkDeletionIsNotLeftBehind(t *testing.T) {
	s, db, _ := integrationService(t)
	ctx := userContext()
	createTypes(t, s, ctx, "kn1")
	network := &deletableNetwork{}
	s.networks = network
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.store.DeleteKnEvents(context.Background(), tx, "kn1"); err != nil {
		t.Fatal(err)
	}
	created := make(chan error, 1)
	go func() {
		_, err := s.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "late_ship", Name: "n", Source: "report"})
		created <- err
	}()
	// The create has passed its network check and waits for the deletion.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var waiting int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE DB = DATABASE() AND (INFO LIKE ? OR INFO LIKE ?)",
			"%"+headTable+"%FOR UPDATE%", "INSERT INTO "+typeTable+"%").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the type create did not wait for the deletion")
		}
	}
	network.gone.Store(true)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-created; statusOf(t, err) != 404 {
		t.Fatalf("type create during deletion: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM "+typeTable+" WHERE f_kn_id = ?", "kn1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%d event types of the deleted network are left", count)
	}
}

func TestIntegrationLargeRuleCheck(t *testing.T) {
	s, _, c := integrationService(t)
	ctx := userContext()
	createTypes(t, s, ctx, "kn1")
	hits := make([]interfaces.KnEventRuleHit, interfaces.KnEventRuleHitsMax)
	for index := range hits {
		hits[index] = interfaces.KnEventRuleHit{Key: fmt.Sprintf("SKU-%05d", index),
			Attributes: map[string]interfaces.KnEventAttribute{"warehouse": {Key: "W1", Label: "一号仓"}}}
	}
	started := time.Now()
	result := must(s.RecordRuleCheck(ctx, "kn1", interfaces.KnEventRuleCheckInput{EventType: "low_stock", Status: "ok", Hits: hits}))
	firstTook := time.Since(started)
	c.tick(1)
	started = time.Now()
	again := must(s.RecordRuleCheck(ctx, "kn1", interfaces.KnEventRuleCheckInput{EventType: "low_stock", Status: "ok", Hits: hits[:10]}))
	t.Logf("20000 hits: first check %v, next check (10 hits, 19990 recoveries) %v", firstTook, time.Since(started))
	if len(result.Events) != len(hits) || len(again.Events) != len(hits) {
		t.Fatalf("events %d then %d", len(result.Events), len(again.Events))
	}
	feed := must(s.ListChanges(context.Background(), "kn1", int64(len(hits)*2-1), 0))
	if len(feed.Entries) != 1 || feed.LastSeq != int64(len(hits)*2) {
		t.Fatalf("feed tail %+v", feed.LastSeq)
	}
}

// Expired check refreshes go, in batches, one replica at a time. Newer
// refreshes and every other change stay, and the feed reads on across the
// numbers the purge leaves.
func TestIntegrationPurgeExpiredCheckRefreshes(t *testing.T) {
	s, db, c := integrationService(t)
	ctx := userContext()
	createTypes(t, s, ctx, "kn1")
	createTypes(t, s, ctx, "kn2")
	hits := make([]interfaces.KnEventRuleHit, purgeBatch+200)
	for index := range hits {
		hits[index] = interfaces.KnEventRuleHit{Key: fmt.Sprintf("SKU-%05d", index)}
	}
	check := func(kn string) {
		t.Helper()
		c.tick(60 * 1000)
		must(s.RecordRuleCheck(ctx, kn, interfaces.KnEventRuleCheckInput{EventType: "low_stock", Status: "ok", Hits: hits}))
	}
	n := int64(len(hits))
	check("kn1") // kn1 1..n detected
	reported := must(s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "late_order", Title: "t", ObjectKey: "PO"}))
	check("kn1") // kn1 n+2..2n+1 checked, expired
	check("kn2") // kn2 1..n detected
	check("kn2") // kn2 n+1..2n checked, expired
	cutoff := c.tick(1)
	check("kn1") // kn1 2n+2..3n+1 checked, kept

	count := func(kn, action string) int64 {
		t.Helper()
		var rows int64
		if err := db.QueryRow("SELECT COUNT(*) FROM "+changeTable+" WHERE f_kn_id = ? AND f_action = ?", kn, action).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}

	held, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var locked int
	if err := held.QueryRowContext(context.Background(), "SELECT GET_LOCK(CONCAT(DATABASE(), ':', ?), 0)", purgeLock).Scan(&locked); err != nil || locked != 1 {
		t.Fatalf("hold the purge lock: %d %v", locked, err)
	}
	if deleted := must(s.store.purgeChecked(context.Background(), cutoff)); deleted != 0 || count("kn1", "checked") != 2*n {
		t.Fatalf("purged %d while another replica purges", deleted)
	}
	if _, err := held.ExecContext(context.Background(), "SELECT RELEASE_LOCK(CONCAT(DATABASE(), ':', ?))", purgeLock); err != nil {
		t.Fatal(err)
	}
	_ = held.Close()

	if deleted := must(s.store.purgeChecked(context.Background(), cutoff)); deleted != 2*n {
		t.Fatalf("purged %d, want %d", deleted, 2*n)
	}
	if got := []int64{count("kn1", "checked"), count("kn1", "detected"), count("kn1", "reported"), count("kn2", "checked"), count("kn2", "detected")}; !reflect.DeepEqual(got, []int64{n, n, 1, 0, n}) {
		t.Fatalf("rows left %v", got)
	}
	feed := must(s.ListChanges(context.Background(), "kn1", n+1, 2))
	if len(feed.Entries) != 2 || feed.Entries[0].Seq != 2*n+2 || feed.Entries[0].Action != "checked" || feed.LastSeq != 2*n+3 {
		t.Fatalf("feed across the purged numbers %+v", feed)
	}
	if event := must(s.GetEvent(ctx, "kn1", reported.Events[0].ID)); len(event.History) != 1 || event.History[0].Action != "reported" {
		t.Fatalf("history of the reported event %+v", event.History)
	}
	if deleted := must(s.store.purgeChecked(context.Background(), cutoff)); deleted != 0 {
		t.Fatalf("purged %d again", deleted)
	}
}
