//go:build integration

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// BKN_SAFE_TEST_MYSQL_DSN must reference a disposable MySQL/MariaDB test schema.
// Only a newly created departments table is used and dropped. If that table
// already exists, creation fails and nothing is dropped or changed.
func newDepartmentHierarchyMySQLTest(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("BKN_SAFE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set BKN_SAFE_TEST_MYSQL_DSN to a disposable MySQL/MariaDB test schema")
	}
	open := func() *gorm.DB {
		t.Helper()
		db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Fatalf("open integration database: %v", err)
		}
		conn, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return db
	}
	db1, db2 := open(), open()
	// CreateTable deliberately has no IF NOT EXISTS. Never adopt or clean up
	// an existing table, including after an unsuccessful setup.
	if err := db1.Set("gorm:table_options", "ENGINE=InnoDB").Migrator().CreateTable(&model.Department{}); err != nil {
		t.Fatalf("create isolated test table (departments must not already exist): %v", err)
	}
	t.Cleanup(func() {
		if err := db1.Migrator().DropTable(&model.Department{}); err != nil {
			t.Errorf("drop integration test table: %v", err)
		}
	})
	for _, id := range []string{"a", "b"} {
		if err := db1.Create(&model.Department{ID: id, Name: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db1, db2
}

func TestDepartmentHierarchyMySQLConcurrentMoves(t *testing.T) {
	db1, db2 := newDepartmentHierarchyMySQLTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for range 10 {
		if err := db1.WithContext(ctx).Model(&model.Department{}).Where("id IN ?", []string{"a", "b"}).Update("parent_id", "").Error; err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			results <- New(db1).UpdateDepartment(ctx, "a", map[string]any{"parent_id": "b"})
		}()
		go func() {
			<-start
			results <- New(db2).UpdateDepartment(ctx, "b", map[string]any{"parent_id": "a"})
		}()
		close(start)
		first, second := <-results, <-results
		if first != nil {
			first, second = second, first
		}
		if first != nil || !errors.Is(second, ErrDepartmentCycle) {
			t.Fatalf("concurrent moves = %v / %v; want one success and one cycle rejection", first, second)
		}
		var deps []model.Department
		if err := db1.WithContext(ctx).Order("id").Find(&deps).Error; err != nil {
			t.Fatal(err)
		}
		if len(deps) != 2 || (deps[0].ParentID != "" && deps[1].ParentID != "") {
			t.Fatalf("invalid hierarchy after concurrent moves: %+v", deps)
		}
	}
}

func TestDepartmentHierarchyMySQLLockBlocksConflictingMove(t *testing.T) {
	db1, db2 := newDepartmentHierarchyMySQLTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx := db1.WithContext(ctx).Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer func() { _ = tx.Rollback().Error }()
	if _, err := lockDepartmentHierarchy(tx); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		result <- New(db2).UpdateDepartment(ctx, "b", map[string]any{"parent_id": "a"})
	}()
	<-started
	// A read-only snapshot would let the second connection finish here.
	// This exercises the real FOR UPDATE lock, without a SQLite-only writer.
	select {
	case err := <-result:
		t.Fatalf("move completed before the hierarchy lock was released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Model(&model.Department{}).Where("id = ?", "a").Update("parent_id", "b").Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrDepartmentCycle) {
			t.Fatalf("conflicting move after commit = %v, want cycle rejection", err)
		}
	case <-ctx.Done():
		t.Fatalf("conflicting move did not finish: %v", ctx.Err())
	}
}
