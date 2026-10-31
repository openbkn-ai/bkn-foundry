// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"context"
	"errors"
	"log/slog"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Historical seed identifiers, not a runtime identity or configuration owner.
const (
	retiredProvenanceOwnerID      = "bdd59f76-19c3-58b0-bf5f-082c4c3cbddb"
	retiredProvenanceOwnerAccount = "openbkn-business-provenance"
	retiredProvenanceOwnerName    = "OpenBKN Business Provenance Service"
)

// ReconcileRetiredBusinessProvenanceOwner withdraws only the recognized old
// built-in account. It runs independently of seed_on_start, like other startup
// data withdrawals. Agent/Prompt creator metadata and audit history are retained.
func ReconcileRetiredBusinessProvenanceOwner(db *gorm.DB, enforcer *authz.Enforcer) error {
	// The usual path has nothing to retire; avoid cloning/reloading the complete
	// authorization model on every subsequent startup.
	var count int64
	if err := db.Model(&model.User{}).Where("id = ?", retiredProvenanceOwnerID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	removed := false
	err := enforcer.Transaction(context.Background(), func(tx *authz.PolicyTransaction) error {
		db := tx.DB()
		var user model.User
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", retiredProvenanceOwnerID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		// A collision or customer-modified identity is not ours to delete.
		if user.Account != retiredProvenanceOwnerAccount || user.Name != retiredProvenanceOwnerName ||
			user.Source != model.SourceLocal || user.AccountType != model.AccountTypeApp ||
			user.PasswordHash != "" || user.Email != "" || user.Telephone != "" || user.MustChangePassword {
			return nil
		}
		var managed int64
		if err := db.Model(&model.ManagedProxyAccount{}).Where("proxy_account_id = ?", user.ID).Count(&managed).Error; err != nil {
			return err
		}
		if managed != 0 {
			return nil
		}
		if err := tx.RemoveAccessor(user.ID); err != nil {
			return err
		}
		if err := db.Where("owner_user_id = ?", user.ID).Delete(&model.APIKey{}).Error; err != nil {
			return err
		}
		if err := db.Where("user_id = ?", user.ID).Delete(&model.UserDepartment{}).Error; err != nil {
			return err
		}
		if err := db.Where("member_id = ?", user.ID).Delete(&model.GroupMember{}).Error; err != nil {
			return err
		}
		if err := db.Delete(&user).Error; err != nil {
			return err
		}
		removed = true
		return nil
	})
	if err == nil && removed {
		slog.Info("removed retired business provenance account", "user_id", retiredProvenanceOwnerID)
	}
	return err
}
