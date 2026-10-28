// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package database

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestMigrateAddsResourceCleanupIndexesToExistingTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	grant := model.AuthorizationGrant{GrantID: "g1", Object: "function:f1"}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	for _, index := range []struct {
		model any
		name  string
	}{
		{&model.AuthorizationGrant{}, "idx_authorization_grant_object"},
		{&model.ProxyGrantSource{}, "idx_proxy_grant_resource"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("fresh install missing %s", index.name)
		}
		if err := db.Migrator().DropIndex(index.model, index.name); err != nil {
			t.Fatal(err)
		}
	}
	// An older database already has these tables, but not the resource indexes.
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if err := db.First(&model.AuthorizationGrant{}, "grant_id = ?", "g1").Error; err != nil {
		t.Fatalf("upgrade lost grant: %v", err)
	}
	for _, query := range []struct {
		sql, index string
	}{
		{"SELECT * FROM authorization_grant WHERE object = 'function:f1' ORDER BY grant_id", "idx_authorization_grant_object"},
		{"SELECT count(*) FROM proxy_grant_source WHERE resource_type = 'function' AND resource_id = 'f1' AND lifecycle_status = 'active'", "idx_proxy_grant_resource"},
	} {
		var plan []struct{ Detail string }
		if err := db.Raw("EXPLAIN QUERY PLAN " + query.sql).Scan(&plan).Error; err != nil {
			t.Fatal(err)
		}
		var details string
		for _, row := range plan {
			details += row.Detail
		}
		if !strings.Contains(details, "SEARCH") || !strings.Contains(details, query.index) {
			t.Errorf("query did not search by resource index: %s", details)
		}
	}
}

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
