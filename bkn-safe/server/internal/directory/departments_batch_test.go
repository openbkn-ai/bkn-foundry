// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"errors"
	"fmt"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/identity"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

func TestDeleteEmptyDepartmentsPreservesLocalChildrenAndMembers(t *testing.T) {
	s, db := newSvc(t)
	departments := []model.Department{{ID: "root", Name: "root"}, {ID: "empty", Name: "empty", ParentID: "root"}, {ID: "occupied", Name: "occupied", ParentID: "root"}, {ID: "local-parent", Name: "parent", ParentID: "root"}, {ID: "local-child", Name: "child", ParentID: "local-parent"}}
	if err := db.Create(&departments).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.UserDepartment{UserID: "local-user", DepartmentID: "occupied"}).Error; err != nil {
		t.Fatal(err)
	}
	retained, err := s.DeleteEmptyDepartments(t.Context(), []string{"root", "empty", "occupied", "local-parent"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(retained) != "[local-parent occupied root]" {
		t.Fatalf("retained: %v", retained)
	}
	var count int64
	db.Model(&model.Department{}).Count(&count)
	if count != 4 {
		t.Fatalf("remaining: %d", count)
	}
}

func TestDeleteEmptyDepartmentsCleanupRollbackAndMissingID(t *testing.T) {
	s, db := newSvc(t)
	departments := []model.Department{{ID: "parent", Name: "parent"}, {ID: "leaf", Name: "leaf", ParentID: "parent"}}
	if err := db.Create(&departments).Error; err != nil {
		t.Fatal(err)
	}
	failure := errors.New("companion cleanup failed")
	var calls []string
	if err := identity.RegisterCleanup(db, identity.Cleanup{Department: func(tx *gorm.DB, id string) error {
		calls = append(calls, id)
		if id == "parent" {
			return failure
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteEmptyDepartments(t.Context(), []string{"parent", "leaf"}); !errors.Is(err, failure) {
		t.Fatalf("cleanup error: %v", err)
	}
	if fmt.Sprint(calls) != "[leaf parent]" {
		t.Fatalf("cleanup order: %v", calls)
	}
	var count int64
	db.Model(&model.Department{}).Count(&count)
	if count != 2 {
		t.Fatal("cleanup failure did not roll back whole forest")
	}
	if _, err := s.DeleteEmptyDepartments(t.Context(), []string{"leaf", "unknown"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing ID: %v", err)
	}
	db.Model(&model.Department{}).Count(&count)
	if count != 2 {
		t.Fatal("missing ID caused partial deletion")
	}
}

func TestDeleteEmptyDepartmentsAcrossBatches(t *testing.T) {
	s, db := newSvc(t)
	rows := []model.Department{{ID: "root", Name: "root"}}
	ids := []string{"root"}
	for i := range 601 {
		id := fmt.Sprintf("leaf-%04d", i)
		rows = append(rows, model.Department{ID: id, Name: id, ParentID: "root"})
		ids = append(ids, id)
	}
	if err := db.CreateInBatches(&rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	retained, err := s.DeleteEmptyDepartments(t.Context(), ids)
	if err != nil || len(retained) != 0 {
		t.Fatalf("delete: %v retained=%v", err, retained)
	}
	var count int64
	db.Model(&model.Department{}).Count(&count)
	if count != 0 {
		t.Fatalf("remaining: %d", count)
	}
}
