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
	// GenerateDraft starts drafting in the background and returns the draft, drafting. A draft
	// people made decisions on is replaced only when discardDecisions is set.
	GenerateDraft(ctx context.Context, token string, projectID string, caller AccountInfo,
		discardDecisions bool) (*Draft, error)
	// GetDraft returns the project's current draft.
	GetDraft(ctx context.Context, token string, projectID string) (*Draft, error)
	// EditDraftItem applies a person's edit to one item of a ready draft, records it as
	// decisions and returns the draft.
	EditDraftItem(ctx context.Context, token string, projectID string, kind string, itemID string,
		edit DraftItemEdit, caller AccountInfo) (*Draft, error)
	// ChooseIdentity sets the properties that identify an object type's records, counting a
	// single-field key over the full table as the caller, and records it as a decision.
	ChooseIdentity(ctx context.Context, token string, projectID string, itemID string,
		identity DraftIdentity, caller AccountInfo) (*Draft, error)
	// RestoreExcluded maps a field the privacy rules excluded back to a property, reading the
	// field from vega-backend as the caller, and records it as a decision.
	RestoreExcluded(ctx context.Context, token string, projectID string, restore DraftRestore,
		caller AccountInfo) (*Draft, error)
	// ConfirmDraft commits the adopted items of a ready draft to the project branch as BKN
	// definitions, for the project owner, and records the confirmation on the draft.
	ConfirmDraft(ctx context.Context, token string, projectID string, confirm DraftConfirm,
		caller AccountInfo) (*Draft, error)
	// GetCheck returns the check of the project's last confirmation.
	GetCheck(ctx context.Context, token string, projectID string) (*Check, error)
	// RunCheck checks the last confirmation again in the background, for the project owner, and
	// returns the check, running.
	RunCheck(ctx context.Context, token string, projectID string, caller AccountInfo) (*Check, error)
	// FailInterrupted fails the drafts and checks a previous run of the service left going.
	FailInterrupted(ctx context.Context) error
}
