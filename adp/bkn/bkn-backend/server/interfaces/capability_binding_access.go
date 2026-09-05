// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"
)

// CapabilityBindingAccess persists and queries t_kn_capability_binding rows (#1257).
//
//go:generate mockgen -source ../interfaces/capability_binding_access.go -destination ../interfaces/mock/mock_capability_binding_access.go
type CapabilityBindingAccess interface {
	CreateBindings(ctx context.Context, tx *sql.Tx, bindings []*CapabilityBinding) error
	GetBindingByID(ctx context.Context, knID, branch, bindingID string) (*CapabilityBinding, error)
	// GetBindingByCapability resolves the unique-key tuple, which is what makes a repeated
	// mount idempotent: the existing row is returned instead of a duplicate-key error.
	GetBindingByCapability(ctx context.Context, knID, branch, capabilityType, ownerID, capabilityID string) (*CapabilityBinding, error)
	DeleteBindingsByIDs(ctx context.Context, tx *sql.Tx, knID, branch string, bindingIDs []string) (int64, error)
	ListBindings(ctx context.Context, query CapabilityBindingsQueryParams) ([]*CapabilityBinding, error)
	GetBindingsTotal(ctx context.Context, query CapabilityBindingsQueryParams) (int, error)
	// GetBindingsTotalByType counts each capability type in one query, for the knowledge
	// network statistics block.
	GetBindingsTotalByType(ctx context.Context, knID, branch string) (map[string]int, error)
	DeleteBindingsByKnID(ctx context.Context, tx *sql.Tx, knID, branch string) (int64, error)
}
