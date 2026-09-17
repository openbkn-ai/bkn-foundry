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
