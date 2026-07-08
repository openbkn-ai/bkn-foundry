// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"testing"

	"bkn-safe/internal/model"
)

func TestValidateDepartmentWrite(t *testing.T) {
	s, db := newSvc(t)
	ctx := context.Background()

	db.Create(&model.User{ID: "u-mgr", Account: "mgr", Name: "Manager", Enabled: true})
	db.Create(&model.Department{ID: "d-1", Name: "Eng", Code: "ENG"})

	if err := s.ValidateDepartmentWrite(ctx, DepartmentWriteInput{
		Name:      "R&D",
		ManagerID: "u-mgr",
		Code:      "RD",
		Email:     "rd@example.com",
	}, ""); err != nil {
		t.Fatalf("valid input: %v", err)
	}

	if err := s.ValidateDepartmentWrite(ctx, DepartmentWriteInput{Code: "ENG"}, ""); err != ErrDuplicateDepartmentCode {
		t.Fatalf("duplicate code on create = %v, want ErrDuplicateDepartmentCode", err)
	}

	if err := s.ValidateDepartmentWrite(ctx, DepartmentWriteInput{Code: "ENG"}, "d-1"); err != nil {
		t.Fatalf("same dept keeps code: %v", err)
	}

	if err := s.ValidateDepartmentWrite(ctx, DepartmentWriteInput{ManagerID: "ghost"}, ""); err == nil {
		t.Fatal("unknown manager should fail")
	}

	if err := s.ValidateDepartmentWrite(ctx, DepartmentWriteInput{Email: "not-an-email"}, ""); err == nil {
		t.Fatal("invalid email should fail")
	}
}

func TestGetDepartmentDetailResolvesManagerName(t *testing.T) {
	s, db := newSvc(t)
	ctx := context.Background()

	db.Create(&model.User{ID: "u-mgr", Account: "mgr", Name: "Alice", Enabled: true})
	db.Create(&model.Department{ID: "d-1", Name: "Eng", ManagerID: "u-mgr", Code: "ENG"})

	detail, err := s.GetDepartmentDetail(ctx, "d-1")
	if err != nil {
		t.Fatalf("GetDepartmentDetail: %v", err)
	}
	if detail.ManagerName != "Alice" || detail.Code != "ENG" {
		t.Fatalf("detail = %+v", detail)
	}
}
