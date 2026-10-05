package audit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
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

func TestKafkaRequestOperationDoesNotDuplicateRecorderCoverageGap(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	publisher := &auditPublisherStub{disposition: auditpublisher.DroppedQueueFull}
	recorder := NewKafkaRecorder(publisher, "test")
	op := NewKafkaRequestOperation(Entry{
		ActorID: "admin-1", ActorNameSnapshot: "Administrator",
		RequestID: "req-safe-gap-once", Method: "POST", Resource: "role-bindings", Action: "bind_role",
	}, recorder.Record)
	if err := op.Enqueue(db, "role-binding:user-1:role-1", "Alice · Reviewer role binding", 204); err != nil {
		t.Fatal(err)
	}
	op.MarkHandled()
	if count := strings.Count(output.String(), `"msg":"safe audit coverage gap"`); count != 1 {
		t.Fatalf("coverage-gap log count = %d, want 1: %s", count, output.String())
	}
}
