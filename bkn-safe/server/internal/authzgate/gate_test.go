// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzgate

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/migrationcontract"
)

func gateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestFreshInstallSeedsRuntimeReceipt(t *testing.T) {
	db := gateTestDB(t)
	if !IsFreshAuthorizationStore(db) {
		t.Fatal("database without Casbin table must be fresh")
	}
	if err := db.AutoMigrate(&casbinPolicyRow{}, &safemodel.AuthorizationGrant{}); err != nil {
		t.Fatal(err)
	}
	if err := SeedFreshInstallMarker(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentMarker(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var marker migrationcontract.Marker
	if err := db.First(&marker).Error; err != nil {
		t.Fatal(err)
	}
	if marker.EETableState != migrationcontract.EETableAbsent ||
		marker.ActivatedGrantIDs != "[]" {
		t.Fatalf("marker = %+v", marker)
	}
}

func TestExistingStoreNeedsDeployMigrationReceipt(t *testing.T) {
	db := gateTestDB(t)
	if err := db.AutoMigrate(&casbinPolicyRow{}); err != nil {
		t.Fatal(err)
	}
	if IsFreshAuthorizationStore(db) {
		t.Fatal("existing Casbin store was inferred as fresh")
	}
	if err := SeedFreshInstallMarker(context.Background(), db, false); !errors.Is(err, ErrMigrationMarkerRequired) {
		t.Fatalf("migration gate error = %v", err)
	}
}

func TestGateRejectsTamperedReceipt(t *testing.T) {
	db := gateTestDB(t)
	marker := migrationcontract.Marker{
		Version:           migrationcontract.CurrentVersion,
		EETableState:      migrationcontract.EETableAbsent,
		CoreSourceSummary: "{}",
		ActivatedGrantIDs: "[]",
	}.Seal()
	if err := db.AutoMigrate(&migrationcontract.Marker{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&marker).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&migrationcontract.Marker{}).
		Where("version = ?", migrationcontract.CurrentVersion).
		Update("core_policy_count", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentMarker(context.Background(), db); !errors.Is(err, ErrMigrationMarkerRequired) {
		t.Fatalf("tampered marker error = %v", err)
	}
}
