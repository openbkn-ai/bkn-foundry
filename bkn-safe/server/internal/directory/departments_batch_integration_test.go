//go:build integration

// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

func TestEmptyDepartmentMySQLSerializesConcurrentMembership(t *testing.T) {
	for _, mode := range []string{"department-members", "user-departments"} {
		t.Run(mode, func(t *testing.T) {
			db1, db2 := newDepartmentHierarchyMySQLTest(t)
			for _, table := range []any{&model.User{}, &model.UserDepartment{}, &model.ManagedProxyAccount{}} {
				if err := db1.Migrator().CreateTable(table); err != nil {
					t.Fatal(err)
				}
				table := table
				t.Cleanup(func() {
					if err := db1.Migrator().DropTable(table); err != nil {
						t.Error(err)
					}
				})
			}
			if err := db1.Create(&model.User{ID: "u", Account: "u", Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			tx := db1.WithContext(ctx).Begin()
			defer func() { _ = tx.Rollback().Error }()
			if _, err := lockDepartmentHierarchy(tx); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if mode == "department-members" {
					done <- New(db2).AddDepartmentMembers(ctx, "a", []string{"u"})
				} else {
					done <- New(db2).SetUserDepartments(ctx, "u", []string{"a"})
				}
			}()
			select {
			case err := <-done:
				t.Fatalf("membership bypassed forest lock: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			retained, err := New(tx).DeleteEmptyDepartments(ctx, []string{"a"})
			if err != nil || len(retained) != 0 {
				t.Fatalf("delete: %v %v", retained, err)
			}
			if err := tx.Commit().Error; err != nil {
				t.Fatal(err)
			}
			err = <-done
			if !errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, ErrUnknownDepartment) {
				t.Fatalf("expected deleted department rejection, got %v", err)
			}
			var count int64
			db1.Model(&model.UserDepartment{}).Count(&count)
			if count != 0 {
				t.Fatal("orphan department membership created")
			}
		})
	}
}

func TestEmptyDepartmentMySQLUsesCurrentMembershipRead(t *testing.T) {
	db1, db2 := newDepartmentHierarchyMySQLTest(t)
	if err := db1.Migrator().CreateTable(&model.UserDepartment{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db1.Migrator().DropTable(&model.UserDepartment{}) })
	tx := db1.Begin()
	defer func() { _ = tx.Rollback().Error }()
	var count int64
	// Establish an older REPEATABLE READ snapshot before the other writer commits.
	if err := tx.Model(&model.UserDepartment{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if err := db2.Create(&model.UserDepartment{UserID: "u", DepartmentID: "a"}).Error; err != nil {
		t.Fatal(err)
	}
	retained, err := New(tx).DeleteEmptyDepartments(t.Context(), []string{"a"})
	if err != nil || len(retained) != 1 || retained[0] != "a" {
		t.Fatalf("new member hidden by old snapshot: retained=%v err=%v", retained, err)
	}
}

func TestEmptyDepartmentMySQLIndexUpgradeBoundsMembershipLocks(t *testing.T) {
	db1, db2 := newDepartmentHierarchyMySQLTest(t)
	// Start with the old schema so this covers upgrading existing installations,
	// not only creating a new table with the index already present.
	if err := db1.Exec(`CREATE TABLE user_departments (
 user_id VARCHAR(64) NOT NULL, department_id VARCHAR(64) NOT NULL,
 PRIMARY KEY (user_id, department_id)) ENGINE=InnoDB`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db1.Migrator().DropTable(&model.UserDepartment{}); err != nil {
			t.Error(err)
		}
	})
	rows := []model.UserDepartment{{UserID: "target-user", DepartmentID: "a"}}
	for i := range 1000 {
		rows = append(rows, model.UserDepartment{UserID: fmt.Sprintf("unrelated-%04d", i), DepartmentID: "b"})
	}
	if err := db1.CreateInBatches(&rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	const indexName = "idx_user_departments_department_id"
	const explainSQL = "EXPLAIN SELECT department_id FROM user_departments WHERE department_id IN ('a', 'a-missing') FOR UPDATE"
	type planRow struct {
		Type string
		Key  *string
		Rows int64
	}
	var before planRow
	if err := db1.Raw(explainSQL).Scan(&before).Error; err != nil {
		t.Fatal(err)
	}
	if before.Type != "ALL" {
		t.Fatalf("legacy fixture should scan the table, got %+v", before)
	}
	// AutoMigrate is the same additive model migration used during core startup.
	for range 2 {
		if err := db1.AutoMigrate(&model.UserDepartment{}); err != nil {
			t.Fatal(err)
		}
	}
	if !db1.Migrator().HasIndex(&model.UserDepartment{}, indexName) {
		t.Fatal("upgrade did not add the department-first index")
	}
	var after planRow
	if err := db1.Raw(explainSQL).Scan(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Key == nil || *after.Key != indexName || (after.Type != "range" && after.Type != "ref") {
		t.Fatalf("department locking read must use its bounded index range, got %+v", after)
	}
	var count int64
	if err := db1.Model(&model.UserDepartment{}).Count(&count).Error; err != nil || count != int64(len(rows)) {
		t.Fatalf("migration changed memberships: count=%d err=%v", count, err)
	}
	t.Logf("legacy plan=%s rows=%d; indexed plan=%s key=%s rows=%d", before.Type, before.Rows, after.Type, *after.Key, after.Rows)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx := db1.WithContext(ctx).Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer func() { _ = tx.Rollback().Error }()
	retained, err := New(tx).DeleteEmptyDepartments(ctx, []string{"a"})
	if err != nil || len(retained) != 1 || retained[0] != "a" {
		t.Fatalf("occupied department not retained: %v, %v", retained, err)
	}
	// Isolate membership-table locking from the deliberately global forest lock.
	// API membership edits still coordinate with that forest lock by design.
	writeCtx, stopWrite := context.WithTimeout(ctx, 2*time.Second)
	defer stopWrite()
	if err := db2.WithContext(writeCtx).Model(&model.UserDepartment{}).
		Where("user_id = ? AND department_id = ?", "unrelated-0500", "b").
		Update("department_id", "b").Error; err != nil {
		t.Fatalf("locking department a blocked an unrelated existing membership: %v", err)
	}
	if err := db2.WithContext(writeCtx).Create(&model.UserDepartment{UserID: "zz-new-member", DepartmentID: "b"}).Error; err != nil {
		t.Fatalf("locking department a blocked an unrelated membership insert: %v", err)
	}
	// Keep the lock protecting matching members: the fix must not weaken current
	// reads or allow a member to disappear under the retained-department check.
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		done <- db2.WithContext(ctx).Where("user_id = ? AND department_id = ?", "target-user", "a").Delete(&model.UserDepartment{}).Error
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("matching membership write bypassed the lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("matching membership remained blocked after transaction ended")
	}
}
