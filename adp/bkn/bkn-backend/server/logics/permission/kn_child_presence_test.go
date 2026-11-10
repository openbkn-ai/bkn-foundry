// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

type presenceStub struct {
	present map[string][]string // knID + "\x00" + resourceType -> child ids still defined
	err     error
	asked   int
}

func (p *presenceStub) PresentKNChildIDs(_ context.Context, resourceType, knID string, childIDs []string) ([]string, error) {
	p.asked++
	if p.err != nil {
		return nil, p.err
	}
	var result []string
	for _, id := range childIDs {
		if slices.Contains(p.present[knID+"\x00"+resourceType], id) {
			result = append(result, id)
		}
	}
	return result, nil
}

func usePresence(t *testing.T, presence interfaces.KNChildPresence) {
	t.Helper()
	SetKNChildPresence(presence)
	t.Cleanup(func() { SetKNChildPresence(nil) })
}

// A child's policy is shared by every branch of the network, so it goes only when no branch
// defines the child any more, whichever branch the delete came from.
func TestCleanupKeepsPoliciesOfChildrenAnotherBranchStillDefines(t *testing.T) {
	for _, branch := range []string{interfaces.MAIN_BRANCH, interfaces.DRAFT_BRANCH, interfaces.VersionBranch(3)} {
		t.Run(branch, func(t *testing.T) {
			usePresence(t, &presenceStub{present: map[string][]string{
				"kn-1\x00" + interfaces.RESOURCE_TYPE_OBJECT_TYPE: {"ot-kept"}}})
			ps := bmock.NewMockPermissionService(gomock.NewController(t))
			ctx, tracker, _ := WithAuthorizationCleanupTracker(context.Background())
			TrackKNChildAuthorizationCleanup(ctx, interfaces.RESOURCE_TYPE_OBJECT_TYPE, "kn-1", branch,
				[]string{"ot-kept", "ot-gone"})

			ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, []string{"kn-1/ot-gone"}).Return(nil)
			if err := tracker.Cleanup(ctx, ps); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCleanupSkipsSafeWhenEveryChildIsStillDefined(t *testing.T) {
	usePresence(t, &presenceStub{present: map[string][]string{
		"kn-1\x00" + interfaces.RESOURCE_TYPE_ACTION_TYPE: {"at-1"}}})
	ps := bmock.NewMockPermissionService(gomock.NewController(t))
	ctx, tracker, _ := WithAuthorizationCleanupTracker(context.Background())
	TrackKNChildAuthorizationCleanup(ctx, interfaces.RESOURCE_TYPE_ACTION_TYPE, "kn-1", interfaces.DRAFT_BRANCH, []string{"at-1"})
	if err := tracker.Cleanup(ctx, ps); err != nil {
		t.Fatal(err)
	}
}

// When the check cannot run, the policies stay: an orphan policy is harmless next to a live
// definition that lost its grants.
func TestCleanupKeepsEveryPolicyWhenThePresenceCheckFails(t *testing.T) {
	presence := &presenceStub{err: errors.New("database unavailable")}
	usePresence(t, presence)
	ps := bmock.NewMockPermissionService(gomock.NewController(t))
	ctx, tracker, _ := WithAuthorizationCleanupTracker(context.Background())
	TrackKNChildAuthorizationCleanup(ctx, interfaces.RESOURCE_TYPE_OBJECT_TYPE, "kn-1", interfaces.DRAFT_BRANCH, []string{"ot-1"})
	if err := tracker.Cleanup(ctx, ps); err != nil {
		t.Fatalf("a failed presence check must not fail the committed delete: %v", err)
	}
	if presence.asked != 1 {
		t.Fatalf("presence asked %d times", presence.asked)
	}
}

// The outbox worker gets resource ids, not child ids, and checks them the same way.
func TestRemovePresentKNChildResourcesFiltersResourceIDs(t *testing.T) {
	usePresence(t, &presenceStub{present: map[string][]string{
		"kn-1\x00" + interfaces.RESOURCE_TYPE_OBJECT_TYPE: {"ot-kept"}}})
	targets := []interfaces.AuthorizationCleanupTarget{
		{ResourceType: interfaces.RESOURCE_TYPE_OBJECT_TYPE, ResourceIDs: []string{"kn-1/ot-gone", "kn-1/ot-kept"}},
		{ResourceType: interfaces.RESOURCE_TYPE_RELATION_TYPE, ResourceIDs: []string{"kn-1/rt-1"}},
	}
	got := RemovePresentKNChildResources(context.Background(), targets)
	want := []interfaces.AuthorizationCleanupTarget{
		{ResourceType: interfaces.RESOURCE_TYPE_OBJECT_TYPE, ResourceIDs: []string{"kn-1/ot-gone"}},
		{ResourceType: interfaces.RESOURCE_TYPE_RELATION_TYPE, ResourceIDs: []string{"kn-1/rt-1"}},
	}
	if !slices.EqualFunc(got, want, func(a, b interfaces.AuthorizationCleanupTarget) bool {
		return a.ResourceType == b.ResourceType && slices.Equal(a.ResourceIDs, b.ResourceIDs)
	}) {
		t.Fatalf("RemovePresentKNChildResources() = %#v, want %#v", got, want)
	}

	usePresence(t, &presenceStub{err: errors.New("database unavailable")})
	if got := RemovePresentKNChildResources(context.Background(), targets); len(got) != 0 {
		t.Fatalf("a failed check must keep every policy, got %#v", got)
	}
}

func TestCleanupAbsentKNChildAuthorization(t *testing.T) {
	ps := bmock.NewMockPermissionService(gomock.NewController(t))
	// Without a presence check a version branch keeps the old rule and touches nothing.
	if err := CleanupAbsentKNChildAuthorization(context.Background(), ps, interfaces.RESOURCE_TYPE_METRIC,
		"kn-1", interfaces.DRAFT_BRANCH, []string{"m1"}); err != nil {
		t.Fatal(err)
	}
	usePresence(t, &presenceStub{present: map[string][]string{"kn-1\x00" + interfaces.RESOURCE_TYPE_METRIC: {"m-kept"}}})
	ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC, []string{"kn-1/m-gone"}).Return(nil)
	if err := CleanupAbsentKNChildAuthorization(context.Background(), ps, interfaces.RESOURCE_TYPE_METRIC,
		"kn-1", interfaces.DRAFT_BRANCH, []string{"m-kept", "m-gone"}); err != nil {
		t.Fatal(err)
	}
	// Everything still defined: Safe is not called at all.
	if err := CleanupAbsentKNChildAuthorization(context.Background(), ps, interfaces.RESOURCE_TYPE_METRIC,
		"kn-1", interfaces.MAIN_BRANCH, []string{"m-kept"}); err != nil {
		t.Fatal(err)
	}
}
