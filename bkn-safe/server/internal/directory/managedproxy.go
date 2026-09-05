// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// withoutManagedProxyAccounts keeps system-owned proxy identities out of every
// generic user-directory projection. Callers that need proxy lifecycle data use
// the dedicated internal managed-proxy API instead.
func withoutManagedProxyAccounts(db *gorm.DB) *gorm.DB {
	return db.Where(
		"NOT EXISTS (SELECT 1 FROM managed_proxy_accounts mpa WHERE mpa.proxy_account_id = users.id)",
	)
}

func managedProxyMembershipFilter(userIDColumn string) string {
	return "NOT EXISTS (SELECT 1 FROM managed_proxy_accounts mpa WHERE mpa.proxy_account_id = " + userIDColumn + ")"
}

// rejectManagedUserIDs prevents generic membership-removal paths from mutating
// a proxy identity while preserving their existing idempotency for unknown
// ordinary user ids.
func (s *Service) rejectManagedUserIDs(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&model.ManagedProxyAccount{}).
		Where("proxy_account_id IN ?", ids).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: managed proxy accounts are not directory users", ErrUnknownUser)
	}
	return nil
}
