// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package repository

import (
	"context"
	"oss-gateway/internal/model"
	"oss-gateway/pkg/outboundtls"

	"gorm.io/gorm"
)

// SaveWithTrust commits the registration and its certificate decision together.
func (r *storageRepository) SaveWithTrust(ctx context.Context, storage *model.StorageConfig, create bool, apply func(outboundtls.Store) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := apply(outboundtls.SQLStore{DB: tx.Statement.ConnPool}); err != nil {
			return err
		}
		if create {
			return tx.Create(storage).Error
		}
		return tx.Save(storage).Error
	})
}
