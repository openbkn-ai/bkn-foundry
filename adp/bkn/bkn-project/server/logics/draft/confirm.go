// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// pendingItems lists what waits for a person before a draft can be confirmed: adopted object
// types without an identity, and adopted relations whose ends are not both adopted. An object
// type that is not adopted waits for no one; it is not written.
func pendingItems(items *interfaces.DraftItems) map[string][]string {
	pending := map[string][]string{}
	for _, ot := range items.ObjectTypes {
		if ot.Adopted && ot.NeedsIdentity {
			pending["needs_identity"] = append(pending["needs_identity"], ot.ID)
		}
	}
	for _, rt := range items.RelationTypes {
		if rt.Adopted && !(objectTypeAdopted(items, rt.Source) && objectTypeAdopted(items, rt.Target)) {
			pending["unadopted_ends"] = append(pending["unadopted_ends"], rt.ID)
		}
	}
	return pending
}

// ConfirmDraft commits the adopted items of a ready draft to the project branch.
//
// The draft is written before the branch and again after it. The first write claims the
// confirmation, refusing a draft someone changed meanwhile, and records the paths it is about to
// write apart from those it wrote before. The second records the commit and the paths written,
// which is what makes the draft confirmed; a refused commit instead writes the draft back as it
// was read, so it can be confirmed again as it is. Only when the service stops between committing
// and recording do the paths stay, and the next confirmation takes them as its own.
func (s *draftService) ConfirmDraft(ctx context.Context, token string, projectID string,
	confirm interfaces.DraftConfirm, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ConfirmDraft")
	defer span.End()

	entry, draft, err := s.readyDraftOf(ctx, token, projectID, confirm.UpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not confirmable")
		return nil, err
	}
	if entry.Owner.ID != caller.ID {
		span.SetStatus(codes.Error, "Not the owner")
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Draft_NotOwner)
	}
	if pending := pendingItems(&draft.Items); len(pending) > 0 {
		span.SetStatus(codes.Error, "Items pending")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_HasPending).
			WithErrorDetails(pending)
	}
	adopted := 0
	for _, ot := range draft.Items.ObjectTypes {
		if ot.Adopted {
			adopted++
		}
	}
	if adopted == 0 {
		span.SetStatus(codes.Error, "Nothing adopted")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_NothingAdopted)
	}
	compiled, err := compileDraft(&draft.Items)
	if err != nil {
		span.SetStatus(codes.Error, "Compile failed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidItem).
			WithErrorDetails(err.Error())
	}
	paths := make([]string, 0, len(compiled.Files))
	for p := range compiled.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	// What this draft wrote on the branch before is its own to replace or remove; nothing else is.
	var owned, earlierPending []string
	previous := draft.Items.Confirmation
	confirmation := &interfaces.DraftConfirmation{}
	if previous != nil {
		owned = unionSorted(previous.Files, previous.Pending)
		earlierPending = previous.Pending
		*confirmation = *previous
	}

	// Claim: not confirmed while under way, and what it is about to write recorded apart. The
	// rest stays the last confirmation's until this one commits.
	readUpdateTime := draft.UpdateTime
	claimTime := max(time.Now().UnixMilli(), readUpdateTime+1)
	draft.UpdateTime = claimTime
	confirmation.UpdateTime, confirmation.Pending = 0, unionSorted(earlierPending, paths)
	draft.Items.Confirmation = confirmation
	if err := s.writeDraft(ctx, draft, readUpdateTime); err != nil {
		span.SetStatus(codes.Error, "Claim confirmation failed")
		return nil, err
	}

	message := fmt.Sprintf("confirm ontology draft of project %s: %d object types, %d definitions",
		projectID, adopted, len(paths))
	result, err := s.ls.AddDefinitions(ctx, entry.KNID, entry.Branch, compiled.Files, owned, message,
		interfaces.GitAuthor{Name: caller.ID})
	if err != nil {
		// Nothing was written: the draft goes back to what was read, with the last confirmation
		// and what it left pending, so the person can fix the cause and confirm it as it is.
		draft.UpdateTime, draft.Items.Confirmation = readUpdateTime, previous
		// Whether or not the caller is still there: the draft must not stay claimed.
		if werr := s.writeDraft(context.WithoutCancel(ctx), draft, claimTime); werr != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("Restore draft of project %s: %v", projectID, werr))
		}
		span.SetStatus(codes.Error, "Commit definitions failed")
		return nil, err
	}

	// Record the commit; the draft's update time stays, so it reads as confirmed.
	confirmation.UpdateTime, confirmation.Commit = draft.UpdateTime, result.Commit
	confirmation.By, confirmation.Time = caller, draft.UpdateTime
	confirmation.Files, confirmation.Pending = paths, nil
	confirmation.SkippedValueTypes = compiled.SkippedValueTypes
	if err := s.writeDraft(context.WithoutCancel(ctx), draft, draft.UpdateTime); err != nil {
		span.SetStatus(codes.Error, "Record confirmation failed")
		return nil, err
	}
	// What was written is checked in the background; the check never undoes the confirmation.
	s.startCheck(context.WithoutCancel(ctx), projectID, result.Commit, checkInputOf(&draft.Items), caller)
	span.SetStatus(codes.Ok, "")
	return draft, nil
}

// writeDraft writes the draft unless someone changed it since readUpdateTime.
func (s *draftService) writeDraft(ctx context.Context, draft *interfaces.Draft, readUpdateTime int64) error {
	written, err := s.da.UpdateDraft(ctx, draft, readUpdateTime)
	if err != nil {
		return internalError(ctx, "Update draft failed", err)
	}
	if !written {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_Stale)
	}
	return nil
}

func unionSorted(a []string, b []string) []string {
	seen := map[string]bool{}
	var all []string
	for _, list := range [][]string{a, b} {
		for _, p := range list {
			if !seen[p] {
				seen[p] = true
				all = append(all, p)
			}
		}
	}
	sort.Strings(all)
	return all
}
