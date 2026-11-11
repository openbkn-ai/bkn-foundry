//go:build integration

// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"errors"
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
