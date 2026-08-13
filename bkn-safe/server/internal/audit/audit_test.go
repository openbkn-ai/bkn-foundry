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
