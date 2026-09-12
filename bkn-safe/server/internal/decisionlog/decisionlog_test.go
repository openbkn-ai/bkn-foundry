// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package decisionlog

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	// A shared-cache in-memory database, and one connection, so the writer
	// goroutine and the test see the same tables.
	db, err := gorm.Open(sqlite.Open("file:decisionlog_"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.AuthzDecision{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAsyncRecordIsQueryableAfterFlush(t *testing.T) {
	db := testDB(t)
	s := New(db, Options{Enabled: true, AllowSampleRate: 1, FlushInterval: time.Hour})
	defer s.Close()
	s.Record(Entry{AccessorID: "u1", ResourceType: "knowledge_network", ResourceID: "kn-1", Operation: "view_detail", Decision: DecisionAllow, Basis: "direct", Source: "check"})
	s.Record(Entry{AccessorID: "u1", ResourceType: "knowledge_network", ResourceID: "kn-2", Operation: "delete", Decision: DecisionDeny, Basis: "default", Source: "check"})
	s.Record(Entry{AccessorID: "u2", ResourceType: "agent", ResourceID: "a-1", Operation: "use", Decision: DecisionAllow, Basis: "inherited", Source: "check"})
	s.Flush(context.Background())

	rows, total, err := s.List(context.Background(), Filter{AccessorID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("u1 decisions = %d/%d, want 2", len(rows), total)
	}
	for _, row := range rows {
		if row.ID == "" || row.CreatedAt.IsZero() || row.AccessorID != "u1" {
			t.Fatalf("row not fully populated: %+v", row)
		}
	}
	rows, total, err = s.List(context.Background(), Filter{Decision: DecisionDeny})
	if err != nil || total != 1 || rows[0].ResourceID != "kn-2" || rows[0].Basis != "default" {
		t.Fatalf("deny filter = %+v total=%d err=%v", rows, total, err)
	}
	if s.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0", s.Dropped())
	}
}

func TestTimeWindowFilterListsAnAccountsDecisionsInAPeriod(t *testing.T) {
	db := testDB(t)
	s := New(db, Options{Enabled: true, AllowSampleRate: 1, Synchronous: true})
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for i, d := range []time.Duration{-2 * time.Hour, -time.Hour, 0, time.Hour} {
		row := model.AuthzDecision{ID: string(rune('a' + i)), AccessorID: "u1", Decision: DecisionAllow, Source: "check", CreatedAt: base.Add(d)}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := s.List(context.Background(), Filter{AccessorID: "u1", From: base.Add(-time.Hour), To: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(rows) != 2 || rows[0].ID != "c" || rows[1].ID != "b" {
		t.Fatalf("window rows = %+v total=%d (want c then b)", rows, total)
	}
}

func TestFullQueueDropsWithoutBlocking(t *testing.T) {
	db := testDB(t)
	// Writer deliberately not started: nothing drains the queue.
	s := newStore(db, Options{Enabled: true, AllowSampleRate: 1, QueueSize: 2})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			s.Record(Entry{AccessorID: "u1", Decision: DecisionDeny, Source: "check"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record blocked on a full queue")
	}
	if s.Dropped() != 3 {
		t.Fatalf("dropped = %d, want 3", s.Dropped())
	}
}

func TestSamplingKeepsEveryDenyAndDropsSampledAllows(t *testing.T) {
	db := testDB(t)
	s := New(db, Options{Enabled: true, AllowSampleRate: 0, Synchronous: true})
	s.Record(Entry{AccessorID: "u1", Decision: DecisionAllow, Source: "check"})
	s.Record(Entry{AccessorID: "u1", Decision: DecisionDeny, Source: "check"})
	s.Record(Entry{AccessorID: "u1", Decision: DecisionNone, Source: "check"})
	rows, total, err := s.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("rows with allow sampling off = %d (%+v), want 2 (deny + none)", total, rows)
	}
	for _, row := range rows {
		if row.Decision == DecisionAllow {
			t.Fatalf("allow row recorded despite sample rate 0: %+v", row)
		}
	}
}

func TestDisabledAndNilStoresAcceptRecords(t *testing.T) {
	var nilStore *Store
	nilStore.Record(Entry{Decision: DecisionDeny})
	nilStore.Flush(context.Background())
	nilStore.Close()
	off := New(testDB(t), Options{})
	off.Record(Entry{Decision: DecisionDeny})
	if off.Enabled() || off.Dropped() != 0 {
		t.Fatal("disabled store must discard silently")
	}
}

func TestPurgeRemovesOnlyOldRows(t *testing.T) {
	db := testDB(t)
	s := New(db, Options{Enabled: true, AllowSampleRate: 1, Synchronous: true})
	now := time.Now().UTC()
	for i, age := range []time.Duration{100 * 24 * time.Hour, 80 * 24 * time.Hour, time.Hour} {
		row := model.AuthzDecision{ID: string(rune('a' + i)), AccessorID: "u1", Decision: DecisionAllow, CreatedAt: now.Add(-age)}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Purge(context.Background(), now.AddDate(0, 0, -90))
	if err != nil || n != 1 {
		t.Fatalf("purge = %d, %v; want 1", n, err)
	}
	_, total, err := s.List(context.Background(), Filter{})
	if err != nil || total != 2 {
		t.Fatalf("after purge total = %d err=%v, want 2", total, err)
	}
}
