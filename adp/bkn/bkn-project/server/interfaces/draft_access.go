// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/draft_access.go -destination ../interfaces/mock/mock_draft_access.go
type DraftAccess interface {
	// GetDraft returns the project's draft, and false when it has none.
	GetDraft(ctx context.Context, projectID string) (*Draft, bool, error)
	// StartDraft writes draft, in the drafting state, as the project's draft unless the project
	// is drafting already; it reports whether it did. A draft drafting since before staleBefore
	// is taken over: its drafting has ended without being recorded.
	StartDraft(ctx context.Context, draft *Draft, staleBefore int64) (bool, error)
	// FinishDraft records how drafting ended: ready with its items, or failed with the reason.
	FinishDraft(ctx context.Context, draft *Draft) error
	// FailDrafting marks every draft still drafting as failed with reason; drafting runs in the
	// service process, so drafts left drafting after a restart will never finish.
	FailDrafting(ctx context.Context, reason string, updateTime int64) (int64, error)
}
