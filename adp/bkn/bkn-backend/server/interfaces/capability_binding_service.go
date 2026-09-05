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

// CapabilityBindingService owns the knowledge-network side of Skill and Function binding (#1257).
//
//go:generate mockgen -source ../interfaces/capability_binding_service.go -destination ../interfaces/mock/mock_capability_binding_service.go
type CapabilityBindingService interface {
	// AttachCapabilities mounts capabilities onto a knowledge network branch. It is idempotent:
	// a capability already bound resolves to the existing row instead of failing.
	AttachCapabilities(ctx context.Context, tx *sql.Tx, knID, branch string,
		entries []*AttachCapabilityEntry) ([]*CapabilityBinding, error)
	// DetachCapabilities releases bindings by binding ID and returns how many rows went away.
	DetachCapabilities(ctx context.Context, tx *sql.Tx, knID, branch string, bindingIDs []string) (int64, error)
	ListCapabilities(ctx context.Context, query CapabilityBindingsQueryParams) (*CapabilityBindingsList, error)
	// GetCapabilityTotalsByType counts bindings per capability type for the statistics block.
	// The count is the number of rows; it carries no dangling judgement, which would require
	// calling the execution factory and does not belong on a counting path.
	GetCapabilityTotalsByType(ctx context.Context, knID, branch string) (map[string]int, error)
	// DeleteCapabilitiesByKnID clears the bindings of a network without a permission check,
	// for use by knowledge-network deletion. tx must be non-nil.
	DeleteCapabilitiesByKnID(ctx context.Context, tx *sql.Tx, knID, branch string) error
}
