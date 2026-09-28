package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestKafkaRequestOperationPublishesOnlyAfterCommit(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	var published []Entry
	op := NewKafkaRequestOperation(Entry{RequestID: "req-safe-commit", Resource: "roles", Action: "create"}, func(_ context.Context, entry Entry) error {
		published = append(published, entry)
		return nil
	})
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := op.Enqueue(tx, "role-committed", "Committed role", 201); err != nil {
			return err
		}
		if len(published) != 0 {
			t.Fatal("published before business commit")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(published) != 0 {
		t.Fatal("published before MarkHandled")
	}
	op.MarkHandled()
	op.MarkHandled()
	if len(published) != 1 || published[0].TargetID != "role-committed" || published[0].Status != 201 {
		t.Fatalf("committed target was not published exactly once: %+v", published)
	}
	var count int64
	if err := db.Model(&model.AuditLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("new Safe Audit wrote %d legacy rows", count)
	}
}

func TestKafkaRequestOperationRollbackDoesNotPublish(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	op := NewKafkaRequestOperation(Entry{RequestID: "req-safe-rollback", Resource: "roles", Action: "create"}, func(context.Context, Entry) error {
		called = true
		return nil
	})
	rollback := errors.New("rollback")
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := op.Enqueue(tx, "role-not-committed", "", 201); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("transaction error = %v", err)
	}
	if called || op.Handled() {
		t.Fatal("rolled-back operation was published or marked handled")
	}
}
