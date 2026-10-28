// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// waitSeconds is how soon to read the status again while drafting or a check runs.
const waitSeconds = 5

// projectStatus computes where a project stands from its draft and check, as the caller sees it.
// A draft drafting, or a check running, since before its stale time has ended without being
// recorded and reads as failed. The same data gives the same status, but for computedAt and what
// the caller may do.
func projectStatus(project *interfaces.Project, draft *interfaces.Draft, check *interfaces.Check,
	caller interfaces.AccountInfo, draftStaleBefore int64, checkStaleBefore int64,
	computedAt int64) *interfaces.ProjectStatus {
	base := "/projects/" + project.ID
	s := &interfaces.ProjectStatus{
		ProjectID:   project.ID,
		Blockers:    []interfaces.ProjectStatusBlocker{},
		Stale:       []string{},
		NextActions: []interfaces.ProjectStatusNextAction{},
		Links: map[string]string{
			"draft": base + "/ontology/draft", "pending": base + "/pending", "check": base + "/ontology/check",
		},
		ComputedAt: computedAt,
	}
	isOwner := caller.ID == project.Owner.ID
	ownerOnly := func(a interfaces.ProjectStatusNextAction) interfaces.ProjectStatusNextAction {
		a.Allowed = isOwner
		if !isOwner {
			a.Reason = interfaces.ACTION_REASON_NOT_OWNER
		}
		return a
	}
	block := func(code string, count int, waitingFor string) {
		s.Blockers = append(s.Blockers, interfaces.ProjectStatusBlocker{Code: code, Count: count, WaitingFor: waitingFor})
	}
	act := func(a interfaces.ProjectStatusNextAction) { s.NextActions = append(s.NextActions, a) }

	if project.Archived {
		s.Stage = interfaces.PROJECT_STAGE_ARCHIVED
		act(ownerOnly(interfaces.ProjectStatusNextAction{
			Action: interfaces.ACTION_UNARCHIVE, Method: http.MethodPost, Path: base + ":unarchive",
		}))
		return s
	}

	// Drafting, the first time or again: refused without catalogs, and discarding decisions.
	generate := interfaces.ProjectStatusNextAction{
		Action: interfaces.ACTION_GENERATE_DRAFT, Method: http.MethodPost, Path: base + "/ontology/draft:generate",
		Allowed: true,
	}
	if draft != nil && len(draft.Items.Decisions) > 0 {
		generate.DiscardsDecisions = len(draft.Items.Decisions)
		generate.Body = map[string]any{"discard_decisions": true}
	}
	// Without catalogs nothing can be drafted. That blocks the project only where drafting is its
	// next step; a draft made before keeps its stage, and only drafting it again is refused.
	noCatalogs := len(project.CatalogIDs) == 0
	if noCatalogs {
		generate.Allowed, generate.Reason = false, interfaces.BLOCKER_NO_CATALOGS
	}
	draftNext := func() {
		if noCatalogs {
			block(interfaces.BLOCKER_NO_CATALOGS, 0, interfaces.WAITING_FOR_OWNER)
			act(ownerOnly(interfaces.ProjectStatusNextAction{
				Action: interfaces.ACTION_SET_CATALOGS, Method: http.MethodPut, Path: base + "/catalogs",
			}))
		}
		act(generate)
	}
	wait := interfaces.ProjectStatusNextAction{
		Action: interfaces.ACTION_WAIT, Method: http.MethodGet, Path: base + "/status", Allowed: true,
		RetryAfterS: waitSeconds,
	}

	if draft == nil {
		s.Stage = interfaces.PROJECT_STAGE_NO_DRAFT
		draftNext()
		return s
	}
	s.Draft = &interfaces.ProjectStatusDraft{
		Status: draft.Status, UpdateTime: draft.UpdateTime, Decisions: len(draft.Items.Decisions),
	}
	switch {
	case draft.Status == interfaces.DRAFT_STATUS_DRAFTING && draft.UpdateTime >= draftStaleBefore:
		s.Stage = interfaces.PROJECT_STAGE_DRAFTING
		act(wait)
		return s
	case draft.Status != interfaces.DRAFT_STATUS_READY:
		s.Stage = interfaces.PROJECT_STAGE_DRAFT_FAILED
		draftNext()
		return s
	}

	confirm := ownerOnly(interfaces.ProjectStatusNextAction{
		Action: interfaces.ACTION_CONFIRM_DRAFT, Method: http.MethodPost, Path: base + "/ontology/draft:confirm",
		Body: map[string]any{"update_time": draft.UpdateTime},
	})
	confirmation := draft.Items.Confirmation
	if confirmation != nil && confirmation.Commit != "" {
		s.Confirmation = &interfaces.ProjectStatusConfirmed{
			Commit: confirmation.Commit, Time: confirmation.Time, Current: confirmation.UpdateTime == draft.UpdateTime,
		}
		if !s.Confirmation.Current {
			s.Stale = append(s.Stale, interfaces.STALE_CONFIRMATION_OUTDATED)
		}
	}

	// What waits for a person, in the one order confirming checks it.
	pending := pendingItems(&draft.Items)
	deciding := []interfaces.ProjectStatusBlocker{}
	for _, code := range []string{interfaces.BLOCKER_NEEDS_IDENTITY, interfaces.BLOCKER_UNADOPTED_ENDS} {
		if n := len(pending[code]); n > 0 {
			deciding = append(deciding, interfaces.ProjectStatusBlocker{
				Code: code, Count: n, WaitingFor: interfaces.WAITING_FOR_EDITOR,
			})
		}
	}
	if !anyObjectTypeAdopted(&draft.Items) {
		deciding = append(deciding, interfaces.ProjectStatusBlocker{
			Code: interfaces.BLOCKER_NOTHING_ADOPTED, WaitingFor: interfaces.WAITING_FOR_EDITOR,
		})
	}
	if len(deciding) > 0 {
		s.Stage = interfaces.PROJECT_STAGE_DECIDING
		s.Blockers = append(s.Blockers, deciding...)
		if deciding[0].Code == interfaces.BLOCKER_NOTHING_ADOPTED {
			act(interfaces.ProjectStatusNextAction{
				Action: interfaces.ACTION_ADOPT_ITEMS, Method: http.MethodGet, Path: base + "/ontology/draft",
				Allowed: true,
			})
		}
		confirm.Allowed, confirm.Reason = false, deciding[0].Code
		act(confirm)
		act(generate)
		return s
	}

	if s.Confirmation == nil || !s.Confirmation.Current {
		s.Stage = interfaces.PROJECT_STAGE_READY_TO_CONFIRM
		act(confirm)
		act(generate)
		return s
	}

	// Confirmed: where its check stands.
	run := ownerOnly(interfaces.ProjectStatusNextAction{
		Action: interfaces.ACTION_RUN_CHECK, Method: http.MethodPost, Path: base + "/ontology/check:run",
	})
	if check != nil {
		s.Check = &interfaces.ProjectStatusCheck{
			Commit: check.Commit, Status: check.Status, Current: check.Commit == confirmation.Commit,
		}
		if !s.Check.Current {
			s.Stale = append(s.Stale, interfaces.STALE_CHECK_OUTDATED)
		}
	}
	switch {
	case s.Check == nil || !s.Check.Current:
		s.Stage = interfaces.PROJECT_STAGE_CHECK_NOT_STARTED
		act(run)
	case check.Status == interfaces.CHECK_STATUS_RUNNING && check.StartTime >= checkStaleBefore:
		s.Stage = interfaces.PROJECT_STAGE_CHECKING
		run.Allowed, run.Reason = false, interfaces.ACTION_REASON_RUNNING
		act(wait)
		act(run)
	case check.Status == interfaces.CHECK_STATUS_PASSED:
		s.Stage = interfaces.PROJECT_STAGE_CONFIRMED
		act(run)
	default:
		s.Stage = interfaces.PROJECT_STAGE_CHECK_NEEDS_ATTENTION
		code := interfaces.BLOCKER_CHECK_FAILED
		switch check.Status {
		case interfaces.CHECK_STATUS_ISSUES:
			code = interfaces.BLOCKER_CHECK_ISSUES
		case interfaces.CHECK_STATUS_PARTIAL:
			code = interfaces.BLOCKER_CHECK_PARTIAL
		}
		block(code, 0, interfaces.WAITING_FOR_OWNER)
		act(run)
	}
	act(generate)
	return s
}

func anyObjectTypeAdopted(items *interfaces.DraftItems) bool {
	for _, ot := range items.ObjectTypes {
		if ot.Adopted {
			return true
		}
	}
	return false
}
