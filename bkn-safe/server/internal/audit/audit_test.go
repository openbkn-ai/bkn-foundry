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
