// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package database

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestApplyPool(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	ApplyPool(conn, config.DBConfig{MaxOpenConns: 7})
	if got := conn.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7", got)
	}
	// 0 keeps database/sql's default: no cap.
	ApplyPool(conn, config.DBConfig{})
	if got := conn.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("an unset cap must leave the pool alone, got %d", got)
	}
}

func TestMigrateKeepsLegacyOperationsGrantable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE operations (
		resource_type_id TEXT NOT NULL,
		id TEXT NOT NULL,
		name TEXT,
		description TEXT,
		parent_operation_id TEXT,
		implied_operation_ids TEXT,
		PRIMARY KEY (resource_type_id, id)
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO operations (resource_type_id, id, name) VALUES ('report', 'view', 'View')`).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var operation model.Operation
	if err := db.First(&operation, "resource_type_id = ? AND id = ?", "report", "view").Error; err != nil {
		t.Fatal(err)
	}
	if operation.Grantable == nil || !operation.IsGrantable() {
		t.Fatalf("legacy operation grantable = %v, want true", operation.Grantable)
	}
	if operation.DerivedToOperationID != "" {
		t.Fatalf("legacy operation derived_to_operation_id = %q, want empty", operation.DerivedToOperationID)
	}
}

func TestResourceIdentifierColumnWidths(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		model any
		field string
		want  int
	}{
		{&model.ProxyGrantSource{}, "ResourceID", 257},
		{&model.ProxyGrantPolicy{}, "ResourceID", 257},
		{&model.ProxyGrantAuditLog{}, "ResourceID", 257},
		{&model.AuthorizationGrant{}, "Object", 322},
		{&model.PermissionRequest{}, "ResourceID", 257},
		{&model.ResourceParent{}, "ResourceID", 257},
		{&model.AuthzDecision{}, "ResourceID", 257},
	}
	for _, test := range tests {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(test.model); err != nil {
			t.Fatal(err)
		}
		field := statement.Schema.LookUpField(test.field)
		if field == nil || field.Size != test.want {
			t.Errorf("%T.%s size = %v, want %d", test.model, test.field, field, test.want)
		}
	}
}

func TestMigrateAddsDepartmentMembershipIndexToLegacyTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Exec(`CREATE TABLE user_departments (
 user_id TEXT NOT NULL, department_id TEXT NOT NULL,
 PRIMARY KEY (user_id, department_id))`).Error; err != nil {
		t.Fatal(err)
	}
	membership := model.UserDepartment{UserID: "existing-user", DepartmentID: "existing-department"}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(db); err != nil {
			t.Fatal(err)
		}
	}
	var columns []struct{ Name string }
	if err := db.Raw("PRAGMA index_info(idx_user_departments_department_id)").Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 || columns[0].Name != "department_id" {
		t.Fatalf("missing department-first index after upgrade: %+v", columns)
	}
	var saved []model.UserDepartment
	if err := db.Find(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0] != membership {
		t.Fatalf("migration changed existing memberships: %+v", saved)
	}
	if err := db.Create(&membership).Error; err == nil {
		t.Fatal("migration lost composite primary-key uniqueness")
	}
}
