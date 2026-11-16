// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func appliedSeed(t *testing.T) (*authz.Enforcer, func() model.User) {
	t.Helper()
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}
	return e, func() model.User {
		var user model.User
		if err := db.First(&user, "id = ?", DatasetStorageAccountID).Error; err != nil {
			t.Fatalf("dataset storage account: %v", err)
		}
		return user
	}
}

func TestSeedsDatasetStorageAccountThatCanOnlyCreateCatalogs(t *testing.T) {
	e, read := appliedSeed(t)
	user := read()
	if user.Account != datasetStorageAccount || user.AccountType != model.AccountTypeApp || !user.Enabled ||
		user.PasswordHash != "" || user.Source != model.SourceLocal || user.MustChangePassword {
		t.Fatalf("dataset storage account malformed: %+v", user)
	}
	if ok, err := e.Check(DatasetStorageAccountID, "catalog", "any-new-catalog", "create"); err != nil || !ok {
		t.Fatalf("catalog create = %v, %v; want allowed", ok, err)
	}
	// Access inside a catalog comes from creating it, never from the seed.
	for _, op := range []string{"view_detail", "resource_manage", "query_data", "data_write", "authorize", "delete"} {
		if ok, err := e.Check(DatasetStorageAccountID, "catalog", "someone-elses", op); err != nil || ok {
			t.Errorf("catalog %s = %v, %v; want denied", op, ok, err)
		}
	}
	if ok, err := e.Check(DatasetStorageAccountID, "knowledge_network", "*", "create"); err != nil || ok {
		t.Errorf("knowledge network create = %v, %v; want denied", ok, err)
	}
}

func TestDatasetStorageAccountSeedKeepsAdministratorChanges(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.User{}).Where("id = ?", DatasetStorageAccountID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}
	var users []model.User
	if err := db.Where("id = ? OR account = ?", DatasetStorageAccountID, datasetStorageAccount).Find(&users).Error; err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Enabled {
		t.Fatalf("re-seed changed the disabled account: %+v", users)
	}
}

func TestDatasetStorageAccountSeedRecreatesADeletedAccount(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}
	if err := e.Transaction(t.Context(), func(tx *authz.PolicyTransaction) error {
		if err := tx.RemoveAccessor(DatasetStorageAccountID); err != nil {
			return err
		}
		return tx.DB().Delete(&model.User{}, "id = ?", DatasetStorageAccountID).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.Check(DatasetStorageAccountID, "catalog", "any-new-catalog", "create"); err != nil || !ok {
		t.Fatalf("catalog create after re-seed = %v, %v; want allowed", ok, err)
	}
}

func TestDatasetStorageAccountSeedLeavesAConflictingLoginAlone(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	other := model.User{ID: "someone-else", Account: datasetStorageAccount, Name: "Someone", Enabled: true, Source: model.SourceLocal, AccountType: model.AccountTypeOther}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("a conflicting login must not stop startup: %v", err)
	}
	var count int64
	if err := db.Model(&model.User{}).Where("id = ?", DatasetStorageAccountID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("seeded beside a conflicting login: count=%d err=%v", count, err)
	}
	for _, accessor := range []string{DatasetStorageAccountID, other.ID} {
		if ok, err := e.Check(accessor, "catalog", "any-new-catalog", "create"); err != nil || ok {
			t.Errorf("%s catalog create = %v, %v; want denied", accessor, ok, err)
		}
	}
}
