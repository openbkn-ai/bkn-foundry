// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

type branchChildrenStub struct {
	interfaces.KNAccess
	children map[string][]interfaces.KNChildResourceCandidate
	err      error
}

func (s branchChildrenStub) ListKNChildResourceCandidates(_ context.Context, _ []string,
	branch string) ([]interfaces.KNChildResourceCandidate, error) {
	return s.children[branch], s.err
}

type copierStub struct {
	interfaces.BranchCopier
	calls []string
}

func (c *copierStub) PruneBranch(context.Context, *sql.Tx, string, string, map[string][]string) error {
	c.calls = append(c.calls, "prune")
	return nil
}

func (c *copierStub) DeleteBranch(context.Context, *sql.Tx, string, string) error {
	c.calls = append(c.calls, "delete")
	return nil
}

func useChildren(t *testing.T, kna interfaces.KNAccess) {
	t.Helper()
	previous := logics.KNA
	logics.SetKNAccess(kna)
	t.Cleanup(func() { logics.SetKNAccess(previous) })
}

// Every child the branch has before a prune is queued; the presence check after commit keeps
// the ones the package kept.
func TestTrackBranchChildrenQueuesWhatTheBranchHadBeforehand(t *testing.T) {
	usePresence(t, &presenceStub{})
	useChildren(t, branchChildrenStub{children: map[string][]interfaces.KNChildResourceCandidate{
		interfaces.DRAFT_BRANCH: {
			{Type: interfaces.RESOURCE_TYPE_OBJECT_TYPE, KNID: "kn-1", ResourceID: "ot-1"},
			{Type: interfaces.RESOURCE_TYPE_ACTION_TYPE, KNID: "kn-1", ResourceID: "at-1"},
			{Type: interfaces.RESOURCE_TYPE_OBJECT_TYPE, KNID: "kn-other", ResourceID: "ot-x"},
		},
	}})
	copier := &copierStub{}
	bc := TrackBranchChildren(copier)
	ctx, tracker, _ := WithAuthorizationCleanupTracker(context.Background())

	if err := bc.PruneBranch(ctx, nil, "kn-1", interfaces.DRAFT_BRANCH, nil); err != nil {
		t.Fatal(err)
	}
	got := tracker.PendingResources()
	if len(got) != 2 || !slices.Equal(got[0].ResourceIDs, []string{"kn-1/at-1"}) ||
		!slices.Equal(got[1].ResourceIDs, []string{"kn-1/ot-1"}) || !slices.Equal(copier.calls, []string{"prune"}) {
		t.Fatalf("pending = %#v, calls %v", got, copier.calls)
	}
}

func TestTrackBranchChildrenDoesNothingWithoutTrackerOrPresence(t *testing.T) {
	useChildren(t, branchChildrenStub{err: errors.New("must not be read")})
	copier := &copierStub{}
	bc := TrackBranchChildren(copier)

	// No presence check installed: an old deployment keeps its behaviour.
	ctx, tracker, _ := WithAuthorizationCleanupTracker(context.Background())
	if err := bc.DeleteBranch(ctx, nil, "kn-1", interfaces.VersionBranch(2)); err != nil {
		t.Fatal(err)
	}
	if len(tracker.PendingResources()) != 0 {
		t.Fatal("queued without a presence check")
	}
	// No tracker: nobody would clean up after commit.
	usePresence(t, &presenceStub{})
	if err := bc.DeleteBranch(context.Background(), nil, "kn-1", interfaces.VersionBranch(2)); err != nil {
		t.Fatal(err)
	}
	// A failed listing leaves the policies alone and does not fail the operation.
	if err := bc.DeleteBranch(ctx, nil, "kn-1", interfaces.VersionBranch(2)); err != nil {
		t.Fatal(err)
	}
	if len(tracker.PendingResources()) != 0 || len(copier.calls) != 3 {
		t.Fatalf("pending %#v calls %v", tracker.PendingResources(), copier.calls)
	}
}
