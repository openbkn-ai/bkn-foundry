// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestCreateUserAccountConflict(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	for _, id := range []string{"original-department", "other-department"} {
		if err := db.Create(&model.Department{ID: id, Name: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	const path = "/api/safe/v1/admin/users"
	first := adminReq(t, r, http.MethodPost, path, map[string]any{
		"id": "original-user", "account": "duplicate-account", "name": "Original Name",
		"email": "original@example.com", "password": "original-password",
		"department_ids": []string{"original-department"},
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d, body = %s", first.Code, first.Body.String())
	}
	var before model.User
	if err := db.First(&before, "id = ?", "original-user").Error; err != nil {
		t.Fatal(err)
	}
	duplicate := adminReq(t, r, http.MethodPost, path, map[string]any{
		"id": "duplicate-user", "account": "duplicate-account", "name": "Changed Name",
		"email": "changed@example.com", "password": "changed-password",
		"department_ids": []string{"other-department"},
	})
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate create: want 409, got %d: %s", duplicate.Code, duplicate.Body.String())
	}
	var body struct {
		ErrorCode string `json:"error_code"`
		Details   struct {
			Reason string `json:"reason"`
		} `json:"error_details"`
	}
	if err := json.Unmarshal(duplicate.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ErrorCode != "BknSafe.Conflict" || body.Details.Reason != "account_already_exists" {
		t.Fatalf("unexpected conflict response: %s", duplicate.Body.String())
	}
	var users []model.User
	if err := db.Where("account = ?", "duplicate-account").Find(&users).Error; err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || !reflect.DeepEqual(users[0], before) {
		t.Fatal("duplicate request changed the original user or created an extra user")
	}
	var memberships []model.UserDepartment
	if err := db.Where("user_id IN ?", []string{"original-user", "duplicate-user"}).Find(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 1 || memberships[0].UserID != "original-user" || memberships[0].DepartmentID != "original-department" {
		t.Fatalf("duplicate request changed department memberships: %v", memberships)
	}

	// An existing account must not hide a genuine storage failure as a 409.
	if err := db.Callback().Create().Before("gorm:create").Register("test:user-storage-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New("injected storage failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	failed := adminReq(t, r, http.MethodPost, path, map[string]any{
		"account": "duplicate-account", "password": "original-password",
	})
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("storage failure: want 500, got %d: %s", failed.Code, failed.Body.String())
	}
}
