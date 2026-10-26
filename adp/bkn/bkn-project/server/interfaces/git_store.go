// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// GIT_MAIN_BRANCH is the branch that mirrors the live definitions of a lineage.
const GIT_MAIN_BRANCH = "main"

// GitAuthor names who made a commit.
type GitAuthor struct {
	Name  string
	Email string
}

// PathDiff lists the definition paths that differ between two sets, each sorted.
type PathDiff struct {
	Added   []string `json:"added"`
	Changed []string `json:"changed"`
	Removed []string `json:"removed"`
}

// Empty reports whether the two sets are the same.
func (d *PathDiff) Empty() bool {
	return len(d.Added) == 0 && len(d.Changed) == 0 && len(d.Removed) == 0
}

// GitStore keeps the definitions of each release lineage in its own bare git
// repository. File maps are keyed by the path inside the bkn/ directory of a
// commit, which is the path of the file in the bkn-backend export tar.
// A ref is anything git resolves to a commit: a branch, a tag or a hash.
// Errors wrap the go-git sentinels: git.ErrRepositoryNotExists for a lineage
// without a repository (or an id that cannot name one), git.ErrRepositoryAlreadyExists,
// git.ErrBranchExists and plumbing.ErrReferenceNotFound.
//
//go:generate mockgen -source ../interfaces/git_store.go -destination ../interfaces/mock/mock_git_store.go
type GitStore interface {
	// InitLineage creates the repository of a lineage with one commit on main holding files.
	InitLineage(ctx context.Context, lineageID string, files map[string][]byte, message string, author GitAuthor) (string, error)
	// CommitFiles replaces the bkn/ directory on branch with files and returns the new head.
	// When files equal what branch already holds, no commit is made and the head is returned.
	CommitFiles(ctx context.Context, lineageID string, branch string, files map[string][]byte, message string, author GitAuthor) (string, error)
	// CreateBranch creates branch at the commit from resolves to.
	CreateBranch(ctx context.Context, lineageID string, branch string, from string) error
	// Tag creates a lightweight tag at the commit ref resolves to.
	Tag(ctx context.Context, lineageID string, name string, ref string) error
	// ReadTree returns the bkn/ files of the commit ref resolves to.
	ReadTree(ctx context.Context, lineageID string, ref string) (map[string][]byte, error)
	// Diff lists the bkn/ paths that changed from refA to refB.
	Diff(ctx context.Context, lineageID string, refA string, refB string) (*PathDiff, error)
	// MergeBase returns the best common ancestor of refA and refB.
	MergeBase(ctx context.Context, lineageID string, refA string, refB string) (string, error)
	// ResolveRef returns the hash of the commit ref resolves to.
	ResolveRef(ctx context.Context, lineageID string, ref string) (string, error)
	// FastForward moves branch to the commit to resolves to. It refuses when the
	// current head of branch is not an ancestor of that commit.
	FastForward(ctx context.Context, lineageID string, branch string, to string) (string, error)
	// CommitMerge writes a merge commit on branch whose parents are the head of
	// branch and the commit other resolves to, with files as its bkn/ directory and
	// every other top-level entry taken from the head of branch. It always writes a
	// commit, even when the tree does not change, so other becomes an ancestor of branch.
	CommitMerge(ctx context.Context, lineageID string, branch string, other string, files map[string][]byte,
		message string, author GitAuthor) (string, error)
	// WithLineageLock runs fn while holding the write lock of the lineage, so a
	// read-decide-write sequence is not interleaved with other writes. fn must do
	// all its work through the store it is given, which serves only this lineage and
	// does not take the lock again; calling the outer store for the same lineage
	// from fn deadlocks. The given store must not be used after fn returns.
	WithLineageLock(ctx context.Context, lineageID string, fn func(store GitStore) error) error
}
