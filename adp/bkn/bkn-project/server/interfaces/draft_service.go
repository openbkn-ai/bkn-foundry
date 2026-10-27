// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// DraftService drafts a project's ontology from the table structure of its data catalogs.
// token is the caller's Authorization header; drafting reads vega-backend as the caller.
//
//go:generate mockgen -source ../interfaces/draft_service.go -destination ../interfaces/mock/mock_draft_service.go
type DraftService interface {
	// GenerateDraft starts drafting in the background and returns the draft, drafting.
	GenerateDraft(ctx context.Context, token string, projectID string, caller AccountInfo) (*Draft, error)
	// GetDraft returns the project's current draft.
	GetDraft(ctx context.Context, token string, projectID string) (*Draft, error)
	// FailInterrupted fails the drafts a previous run of the service left drafting.
	FailInterrupted(ctx context.Context) error
}
