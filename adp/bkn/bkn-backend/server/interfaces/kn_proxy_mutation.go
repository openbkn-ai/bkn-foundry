// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"
)

// KNProxyMutationPublisher serializes a main-branch child-resource mutation
// with the knowledge-network proxy publication lifecycle. The callback receives
// the transaction and context that own authorization compensation tracking.
type KNProxyMutationPublisher interface {
	PublishKNChildMutation(ctx context.Context, changes *KN, mergeMode string,
		mutate func(context.Context, *sql.Tx) error) error
	PublishKNCapabilityMutation(ctx context.Context, knID, branch string, removedBindingIDs []string,
		mutate func(context.Context, *sql.Tx) (*KNCapabilityMutationResult, error)) (*KNCapabilityMutationResult, error)
}

// KNCapabilityMutationResult carries the binding rows returned by an attach
// and the affected-row count returned by a detach through one transaction.
type KNCapabilityMutationResult struct {
	Bindings     []*CapabilityBinding
	RowsAffected int64
}

type KNServiceWithProxyMutation interface {
	KNService
	KNProxyMutationPublisher
}
