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
	PublishKNChildMutation(ctx context.Context, changes *KN,
		mutate func(context.Context, *sql.Tx) error) error
}

type KNServiceWithProxyMutation interface {
	KNService
	KNProxyMutationPublisher
}
