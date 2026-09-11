// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authzmigration"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestFreshAuthorizationStoreSeedsMarkerAfterExtensionAssembly(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	fresh := authzmigration.IsFreshAuthorizationStore(db)
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(&safemodel.AuthorizationMigrationMarker{}) {
		t.Fatal("normal schema migration created the one-time authorization marker table")
	}
	// An Enterprise extension creates its private table between Boot and Run.
	if err := db.Exec("CREATE TABLE ee_permobject_rules (id TEXT PRIMARY KEY, accessor_id TEXT, resource_type TEXT, resource_id TEXT, op TEXT, effect TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, freshAuthorizationStore: fresh}
	if err := a.ensureAuthorizationMigrationReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	var marker safemodel.AuthorizationMigrationMarker
	if err := db.First(&marker).Error; err != nil {
		t.Fatal(err)
	}
	if marker.EETableState != authzmigration.EETablePresentEmpty {
		t.Fatalf("EE table state = %q", marker.EETableState)
	}
}

func TestExistingAuthorizationStoreCannotStartWithoutCurrentMarker(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE casbin_rule (id INTEGER PRIMARY KEY, ptype TEXT, v0 TEXT, v1 TEXT, v2 TEXT, v3 TEXT, v4 TEXT, v5 TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	fresh := authzmigration.IsFreshAuthorizationStore(db)
	if fresh {
		t.Fatal("existing Casbin store was inferred as fresh")
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(&safemodel.AuthorizationMigrationMarker{}) {
		t.Fatal("upgraded store created an empty marker table before the offline migration")
	}
	a := &App{db: db, freshAuthorizationStore: fresh}
	err = a.ensureAuthorizationMigrationReady(context.Background())
	if !errors.Is(err, authzmigration.ErrMigrationMarkerRequired) {
		t.Fatalf("migration gate error = %v", err)
	}
}
