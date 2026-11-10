// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"
)

// Reserved branches of a versioned network: the draft, and one immutable branch per version.
const (
	DRAFT_BRANCH          = "draft"
	VERSION_BRANCH_PREFIX = "v"
)

// BranchCopier copies the versioned rows of a network between branches, inside the caller's
// transaction.
//
//go:generate mockgen -source ../interfaces/kn_version.go -destination ../interfaces/mock/mock_kn_version.go
type BranchCopier interface {
	// ReplaceBranch deletes every versioned row of dst and copies src into it. The network row
	// of dst is updated in place, never deleted, because the main row doubles as the network lock.
	ReplaceBranch(ctx context.Context, tx *sql.Tx, knID, src, dst string) error
	// DeleteBranch deletes every versioned row of the branch, its network row included.
	DeleteBranch(ctx context.Context, tx *sql.Tx, knID, branch string) error
}
