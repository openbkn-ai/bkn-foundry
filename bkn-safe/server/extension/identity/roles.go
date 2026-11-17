// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package identity

import (
	"errors"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ImportRole writes only the name and description of an extension-owned custom
// role. It never copies permissions or memberships and never adopts a local role
// by name. The caller owns the immutable external-ID mapping and transaction.
func ImportRole(tx *gorm.DB, id, name, description string) error {
	name = strings.TrimSpace(name)
	if id == "" || len(id) > 64 || name == "" || len(name) > 128 || len(description) > 1024 {
		return adminwrite.ErrInvalid
	}
	key := strings.ToLower(name)
	var conflict int64
	if err := tx.Model(&model.Role{}).Where("id <> ? AND (name_key = ? OR (name_key IS NULL AND LOWER(TRIM(name)) = ?))", id, key, key).Count(&conflict).Error; err != nil {
		return err
	}
	if conflict > 0 {
		return adminwrite.ErrRoleNameExisted
	}
	var role model.Role
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&role, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&model.Role{ID: id, Name: name, NameKey: &key, Description: description, Source: model.RoleSourceCustom}).Error
	}
	if err != nil {
		return err
	}
	if role.Source != model.RoleSourceCustom {
		return adminwrite.ErrImmutable
	}
	return tx.Model(&role).Updates(map[string]any{"name": name, "name_key": key, "description": description}).Error
}
