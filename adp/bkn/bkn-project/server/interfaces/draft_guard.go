// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"
)

// DraftGuard is the project owner and draft a guarded write was read against.
type DraftGuard struct {
	Owner           AccountInfo
	DraftExists     bool
	DraftUpdateTime int64
}

// DraftLock locks and checks, in the transaction of a guarded draft write, what else the write was
// read against; false refuses the write.
type DraftLock func(ctx context.Context, tx *sql.Tx) (bool, error)

// GuardedDraftAccess writes a draft only if the project row, the draft and what lock checks are
// as read, in one transaction. No network or AI call happens while the locks are held.
type GuardedDraftAccess interface {
	CommitDraftGuarded(ctx context.Context, draft *Draft, guard DraftGuard, lock DraftLock) (bool, error)
}
