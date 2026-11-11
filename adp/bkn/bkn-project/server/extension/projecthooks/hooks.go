// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package projecthooks is the contract through which an edition adds sources beyond tables to
// projects: what document items add to a draft, to a release, to a lineage switch and to archiving
// a project. It imports nothing of the project service but its interfaces, so an implementation
// does not either. A change here is a change of the contract with the professional edition.
package projecthooks

import (
	"context"
	"database/sql"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Hooks is what an edition adds to projects. A nil field keeps the community behaviour.
type Hooks struct {
	Drafts       Drafts
	Publications Publications
	Projects     Projects
}

var (
	current    Hooks
	registered bool
)

// Register sets the hooks of the process once, while assembling; a nil field keeps the community
// behaviour. A second registration panics: two editions' hooks must never both be registered.
func Register(hooks Hooks) {
	entitlement.MustBeAssembling("bkn-project edition hooks")
	if registered {
		panic("projecthooks: hooks registered twice")
	}
	current, registered = hooks, true
}

// SetForTest replaces the hooks for one test and returns what restores them.
func SetForTest(hooks Hooks) (restore func()) {
	if !testing.Testing() {
		panic("projecthooks: SetForTest outside a test binary")
	}
	previous, wasRegistered := current, registered
	current, registered = hooks, true
	return func() { current, registered = previous, wasRegistered }
}

// Current is the hooks registered.
func Current() Hooks { return current }

// ReleaseTarget is the release a publication is for.
type ReleaseTarget struct {
	ProjectID, LineageID, Branch, BranchCommit, NetworkID string
	// Version is the release's version; a lineage's network keeps its id across releases.
	Version string
	// DeclaredProjectID is the project the release request names, if any.
	DeclaredProjectID string
	Publisher         interfaces.AccountInfo
}

// Publications is what document items add to a release.
type Publications interface {
	// Review is the check a release review adds for the document items of the draft, and folds what
	// it inspected into the review signature. ok is false when the draft has no document items.
	Review(ctx context.Context, gs interfaces.GitStore, token string, draft *interfaces.Draft, target ReleaseTarget) (check interfaces.ReleaseCheck, sign func(signature string) string, ok bool)
	// Prepare readies publishing the release with the Git store already locked for it. A nil
	// Publication means the release carries nothing of the edition. Errors are public.
	Prepare(ctx context.Context, gs interfaces.GitStore, token string, target ReleaseTarget) (Publication, error)
}

// Publication is the part of one release the edition publishes.
type Publication interface {
	// ProjectID is the project the publication belongs to.
	ProjectID() string
	// Mount runs once the network is imported. Errors are public.
	Mount(ctx context.Context, token string) error
	// Activate runs once the release is recorded; a failure does not fail the release.
	Activate(ctx context.Context) error
}

// Projects is what document items add to a project.
type Projects interface {
	// Archiving runs in the transaction that archives a project, after its row is updated.
	Archiving(ctx context.Context, tx *sql.Tx, projectID string, updateTime int64) error
	// Archived runs once the archive is committed; a failure leaves it to be retried.
	Archived(ctx context.Context, token, projectID string, caller interfaces.AccountInfo)
}
