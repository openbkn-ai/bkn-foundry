// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"database/sql"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

var (
	knChildPresenceMu sync.RWMutex
	knChildPresence   interfaces.KNChildPresence
)

// SetKNChildPresence installs the check that keeps a child's policy while some branch of the
// network still defines the child. Without it, deletes on the draft or a version branch leave
// policies alone and deletes elsewhere remove them, as before.
func SetKNChildPresence(presence interfaces.KNChildPresence) {
	knChildPresenceMu.Lock()
	defer knChildPresenceMu.Unlock()
	knChildPresence = presence
}

func currentKNChildPresence() interfaces.KNChildPresence {
	knChildPresenceMu.RLock()
	defer knChildPresenceMu.RUnlock()
	return knChildPresence
}

// absentKNChildIDs returns the children no branch of the network defines. It must run after the
// delete committed. When the check fails it returns none: the policies stay, which at worst
// leaves an orphan, never a live definition without its grants.
func absentKNChildIDs(ctx context.Context, resourceType, knID string, childIDs []string) []string {
	presence := currentKNChildPresence()
	if presence == nil || len(childIDs) == 0 {
		return childIDs
	}
	present, err := presence.PresentKNChildIDs(ctx, resourceType, knID, childIDs)
	if err != nil {
		logger.GetLogger().Errorw("authorization cleanup skipped: child presence check failed",
			"resource_type", resourceType, "kn_id", knID, "child_count", len(childIDs), "error", err.Error())
		return nil
	}
	kept := make(map[string]bool, len(present))
	for _, id := range present {
		kept[id] = true
	}
	absent := make([]string, 0, len(childIDs))
	for _, id := range childIDs {
		if !kept[id] {
			absent = append(absent, id)
		}
	}
	return absent
}

// RemovePresentKNChildResources drops from cleanup targets the child resources some branch of
// their network still defines. The proxy outbox worker calls it after the delete committed.
func RemovePresentKNChildResources(ctx context.Context,
	targets []interfaces.AuthorizationCleanupTarget) []interfaces.AuthorizationCleanupTarget {

	if currentKNChildPresence() == nil {
		return targets
	}
	result := make([]interfaces.AuthorizationCleanupTarget, 0, len(targets))
	for _, target := range targets {
		var knOrder []string
		byKN := map[string][]string{}
		for _, resourceID := range target.ResourceIDs {
			knID, childID, ok := splitKNChildResourceID(resourceID)
			if !ok {
				// Not a child resource id: leave the decision to the cleanup as it was.
				knID, childID = "", resourceID
			}
			if _, seen := byKN[knID]; !seen {
				knOrder = append(knOrder, knID)
			}
			byKN[knID] = append(byKN[knID], childID)
		}
		resourceIDs := make([]string, 0, len(target.ResourceIDs))
		for _, knID := range knOrder {
			if knID == "" {
				resourceIDs = append(resourceIDs, byKN[knID]...)
				continue
			}
			resourceIDs = append(resourceIDs,
				interfaces.KNChildResourceIDs(knID, absentKNChildIDs(ctx, target.ResourceType, knID, byKN[knID]))...)
		}
		if len(resourceIDs) > 0 {
			result = append(result, interfaces.AuthorizationCleanupTarget{
				ResourceType: target.ResourceType, ResourceIDs: resourceIDs})
		}
	}
	return result
}

func splitKNChildResourceID(resourceID string) (string, string, bool) {
	knID, _, found := strings.Cut(resourceID, "/")
	if !found || !interfaces.IsValidAuthorizationID(knID) {
		return "", "", false
	}
	childID, ok := interfaces.KNChildIDFromResourceID(knID, resourceID)
	return knID, childID, ok
}

// TrackBranchChildren wraps a branch copier so that, before it overwrites, prunes or deletes the
// definitions of a branch, every child on that branch is queued on the cleanup tracker of the
// context. Queuing all of them is safe: the cleanup keeps the policy of any child some branch
// still defines after the commit, such as one the new content kept.
func TrackBranchChildren(bc interfaces.BranchCopier) interfaces.BranchCopier {
	if bc == nil {
		return nil
	}
	return childTrackingBranchCopier{BranchCopier: bc}
}

type childTrackingBranchCopier struct {
	interfaces.BranchCopier
}

func (c childTrackingBranchCopier) ReplaceBranch(ctx context.Context, tx *sql.Tx, knID, src, dst string) error {
	trackBranchChildren(ctx, knID, dst)
	return c.BranchCopier.ReplaceBranch(ctx, tx, knID, src, dst)
}

func (c childTrackingBranchCopier) DeleteBranch(ctx context.Context, tx *sql.Tx, knID, branch string) error {
	trackBranchChildren(ctx, knID, branch)
	return c.BranchCopier.DeleteBranch(ctx, tx, knID, branch)
}

func (c childTrackingBranchCopier) PruneBranch(ctx context.Context, tx *sql.Tx, knID, branch string,
	keep map[string][]string) error {
	trackBranchChildren(ctx, knID, branch)
	return c.BranchCopier.PruneBranch(ctx, tx, knID, branch, keep)
}

// trackBranchChildren queues the children the branch has before the operation. It reads
// committed rows, which the locked network keeps as they were. Without a tracker or a presence
// check it does nothing; when the read fails the policies stay, at worst as orphans.
func trackBranchChildren(ctx context.Context, knID, branch string) {
	if _, ok := ctx.Value(authorizationCleanupTrackerKey{}).(*AuthorizationCleanupTracker); !ok ||
		currentKNChildPresence() == nil || logics.KNA == nil {
		return
	}
	candidates, err := logics.KNA.ListKNChildResourceCandidates(ctx, []string{knID}, branch)
	if err != nil {
		logger.GetLogger().Errorw("authorization cleanup skipped: listing branch children failed",
			"kn_id", knID, "branch", branch, "error", err.Error())
		return
	}
	byType := map[string][]string{}
	for _, candidate := range candidates {
		if candidate.KNID == knID && candidate.ResourceID != "" {
			byType[candidate.Type] = append(byType[candidate.Type], candidate.ResourceID)
		}
	}
	for resourceType, childIDs := range byType {
		TrackKNChildAuthorizationCleanup(ctx, resourceType, knID, branch, childIDs)
	}
}

// AuthorizationCleanupFrom returns the cleanup tracker of the context, if any.
func AuthorizationCleanupFrom(ctx context.Context) (*AuthorizationCleanupTracker, bool) {
	tracker, ok := ctx.Value(authorizationCleanupTrackerKey{}).(*AuthorizationCleanupTracker)
	return tracker, ok
}

// Discard drops the queued cleanups, once another owner (a proxy outbox event) carries them.
func (tracker *AuthorizationCleanupTracker) Discard() {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.entries = map[string]trackedAuthorizationCleanup{}
}

// CleanupAbsentKNChildAuthorization removes, after a committed delete on branch, the policies of
// the children no branch of the network defines any more. Without a presence check it keeps the
// former rule: a delete on the draft or a version branch leaves the policies alone.
func CleanupAbsentKNChildAuthorization(ctx context.Context, ps interfaces.PermissionService,
	resourceType, knID, branch string, childIDs []string) error {
	if currentKNChildPresence() == nil && interfaces.IsReservedBranch(branch) {
		return nil
	}
	return CleanupKNChildAuthorization(ctx, ps, resourceType, knID,
		absentKNChildIDs(ctx, resourceType, knID, childIDs))
}
