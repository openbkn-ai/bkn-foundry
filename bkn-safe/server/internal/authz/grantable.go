// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// ErrOperationNotGrantable identifies an attempt to persist an allow or deny
// policy for an operation that is available only as a computed/read-only
// authorization result.
var ErrOperationNotGrantable = errors.New("operation is not grantable")

// ValidateGrantableOperations rejects only registered operations explicitly
// marked grantable=false. Unknown operations retain their existing validation
// behavior at the calling API, while legacy registry rows and in-memory test
// rows default to grantable.
func (en *Enforcer) ValidateGrantableOperations(ctx context.Context, resourceType string, operations []string) error {
	if len(operations) == 0 {
		return nil
	}
	if en.db == nil {
		return errors.New("authz enforcer has no operation registry")
	}
	unique := distinctOperations(operations)
	var rows []model.Operation
	query := en.db.WithContext(ctx).Where("resource_type_id = ?", resourceType)
	allOperations := false
	for _, operation := range unique {
		if operation == ActAll {
			allOperations = true
			break
		}
	}
	if !allOperations {
		query = query.Where("id IN ?", unique)
	}
	if err := query.Order("id").Find(&rows).Error; err != nil {
		return err
	}
	notGrantable := make(map[string]bool, len(rows))
	for _, operation := range rows {
		if !operation.IsGrantable() {
			notGrantable[operation.ID] = true
		}
	}
	for _, operation := range operations {
		if operation == ActAll && len(notGrantable) > 0 {
			for _, row := range rows {
				if notGrantable[row.ID] {
					return fmt.Errorf("%w: %s/%s is covered by wildcard", ErrOperationNotGrantable, resourceType, row.ID)
				}
			}
		}
		if notGrantable[operation] {
			return fmt.Errorf("%w: %s/%s", ErrOperationNotGrantable, resourceType, operation)
		}
	}
	return nil
}

func (en *Enforcer) validateGrantablePolicy(ctx context.Context, object, operation string) error {
	if object == "*" {
		// Preserve the existing break-glass super-admin rule only. A specific
		// operation on the global object matches every resource type, so it must
		// not become a back door around an operation's grantable=false contract.
		if operation == ActAll {
			return nil
		}
		var rows []model.Operation
		if err := en.db.WithContext(ctx).Where("id = ?", operation).Order("resource_type_id").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if !row.IsGrantable() {
				return fmt.Errorf("%w: */%s covers %s/%s", ErrOperationNotGrantable, operation, row.ResourceTypeID, row.ID)
			}
		}
		return nil
	}
	resourceType, _, ok := strings.Cut(object, ":")
	if !ok || resourceType == "" {
		return nil
	}
	return en.ValidateGrantableOperations(ctx, resourceType, []string{operation})
}
