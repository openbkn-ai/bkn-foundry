package audit

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestListUsesIDAsSameTimestampKeysetTiebreaker(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 8, 1, 10, 0, 0, 123456789, time.UTC)
	for _, id := range []string{"audit-c", "audit-b", "audit-a"} {
		if err := db.Create(&model.AuditLog{ID: id, ActorID: "admin-a", CreatedAt: createdAt}).Error; err != nil {
			t.Fatal(err)
		}
	}
	store := New(db)

	logs, _, err := store.List(context.Background(), Filter{To: createdAt, BeforeID: "audit-a", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || logs[0].ID != "audit-b" || logs[1].ID != "audit-c" {
		t.Fatalf("same-timestamp records were skipped or reordered: %+v", logs)
	}
}

func TestListFiltersAllRowsFromOneRequest(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create([]model.AuditLog{
		{ID: "audit-a", RequestID: "request-batch", TargetID: "grant-a"},
		{ID: "audit-b", RequestID: "request-batch", TargetID: "grant-b"},
		{ID: "audit-c", RequestID: "request-other", TargetID: "grant-c"},
	}).Error; err != nil {
		t.Fatal(err)
	}

	logs, total, err := New(db).List(context.Background(), Filter{RequestID: "request-batch", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(logs) != 2 {
		t.Fatalf("request_id filter: total=%d logs=%+v, want the two batch target rows", total, logs)
	}
	for _, log := range logs {
		if log.RequestID != "request-batch" {
			t.Fatalf("request_id filter leaked a different request row: %+v", log)
		}
	}
}

func TestRecordPreservesOperationAuditIdentityAndCorrelationFacts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	entry := Entry{
		ActorID: "user-a", ActorNameSnapshot: "User A", ActorType: "user",
		AuthMethod: "unknown", RequestID: "req-a", SourceChannel: "api",
		Method: "POST", Resource: "api-keys", Action: "create", TargetID: "key-a",
		TargetName: "Cursor key", Status: 201,
	}
	if err := store.Record(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	var got model.AuditLog
	if err := db.First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.ActorID != entry.ActorID || got.ActorNameSnapshot != entry.ActorNameSnapshot ||
		got.ActorType != entry.ActorType || got.AuthMethod != entry.AuthMethod ||
		got.RequestID != entry.RequestID || got.SourceChannel != entry.SourceChannel ||
		got.Action != entry.Action || got.TargetName != entry.TargetName {
		t.Fatalf("operation audit facts were not preserved: %+v", got)
	}
}

func TestEnqueueMakesPendingAuditEventQueryable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	entry := Entry{
		ActorID: "security-user", ActorType: "user", AuthMethod: "oauth", RequestID: "role-create-1",
		SourceChannel: "api", Method: "POST", Resource: "roles", Action: "create", TargetID: "role-1", Status: 201,
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return store.Enqueue(tx, entry)
	}); err != nil {
		t.Fatal(err)
	}

	logs, total, err := store.List(context.Background(), Filter{RequestID: entry.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("pending audit event: total=%d logs=%+v, want one event", total, logs)
	}
	if logs[0].ChainState != model.AuditChainStatePending || logs[0].Seq != nil {
		t.Fatalf("pending audit event state = %+v, want pending with no sequence", logs[0])
	}
}
