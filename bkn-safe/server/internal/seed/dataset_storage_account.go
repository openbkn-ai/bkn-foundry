// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"context"
	"errors"
	"log/slog"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// The storage identity BKN projects write document Datasets with. bkn-project
// uses these fixed values when no storage account is configured, and creates
// its own catalog under this identity; Vega gives a catalog's creator its
// creator grants. The account has no password and cannot sign in. Only the
// right to create catalogs is seeded: what it may do inside a catalog comes
// from creating it.
const (
	DatasetStorageAccountID = "e68bf3bc-3754-4b64-9fbd-e8919e544112"
	datasetStorageAccount   = "bkn-project-dataset-storage"
	datasetStorageName      = "BKN Project Document Storage"
)

// seedDatasetStorageAccount creates the account the first time only, so an
// administrator who disables it stays in control; bkn-project then reports its
// storage as unavailable. Another identity already holding the login name is
// left alone and only logged: it must not stop bkn-safe from starting. Replicas
// starting together insert the same row; the loser's insert does nothing.
func seedDatasetStorageAccount(db *gorm.DB, enforcer *authz.Enforcer) error {
	var existing model.User
	err := db.Where("id = ? OR account = ?", DatasetStorageAccountID, datasetStorageAccount).First(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.User{
			ID:          DatasetStorageAccountID,
			Account:     datasetStorageAccount,
			Name:        datasetStorageName,
			Enabled:     true,
			Source:      model.SourceLocal,
			AccountType: model.AccountTypeApp,
		}).Error; err != nil {
			return err
		}
	case err != nil:
		return err
	case existing.ID != DatasetStorageAccountID:
		slog.Warn("dataset storage account not seeded: its login name belongs to another account",
			"account", datasetStorageAccount, "user_id", existing.ID)
		return nil
	}
	var count int64
	if err := db.Model(&model.User{}).Where("id = ?", DatasetStorageAccountID).Count(&count).Error; err != nil || count == 0 {
		return err
	}
	return enforcer.Transaction(context.Background(), func(tx *authz.PolicyTransaction) error {
		return tx.GrantObjectPermission(DatasetStorageAccountID, "catalog", "*", "create")
	})
}
