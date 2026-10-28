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

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// waitSeconds is how soon to read the status again while drafting or a check runs.
const waitSeconds = 5

// GetStatus computes where a project stands, as the caller sees it. Only a draft ready to confirm
// is read against its project branch; a preflight that fails leaves out what it would tell, and
// confirming checks it anyway.
func (s *draftService) GetStatus(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.ProjectStatus, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetStatus")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist {
		draft = nil
	}
	check, checked, err := s.ca.GetCheck(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get check failed")
		return nil, internalError(ctx, "Get check failed", err)
	}
	if !checked {
		check = nil
	}
	now := time.Now()
	draftStaleBefore := now.Add(-(s.setting.Timeout + time.Minute)).UnixMilli()
	checkStaleBefore := now.Add(-s.checkStaleAfter()).UnixMilli()
	status := projectStatus(entry.Project, draft, check, nil, caller, draftStaleBefore, checkStaleBefore, now.UnixMilli())
	if status.Stage == interfaces.PROJECT_STAGE_READY_TO_CONFIRM {
		if preflight := s.preflightConfirm(ctx, entry.Project, draft); preflight != nil {
			status = projectStatus(entry.Project, draft, check, preflight, caller, draftStaleBefore, checkStaleBefore,
				now.UnixMilli())
		}
	}
	span.SetStatus(codes.Ok, "")
	return status, nil
}

// preflightConfirm reads what the project branch would refuse confirming the draft for, with the
// paths confirming would write and those the draft owns from earlier confirmations; nil when it
// cannot tell.
func (s *draftService) preflightConfirm(ctx context.Context, project *interfaces.Project,
	draft *interfaces.Draft) *interfaces.DefinitionsPreflight {
	compiled, err := compileDraft(&draft.Items)
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(compiled.Files))
	for p := range compiled.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var owned []string
	if c := draft.Items.Confirmation; c != nil {
		owned = unionSorted(c.Files, c.Pending)
	}
	preflight, err := s.ls.PreflightDefinitions(ctx, project.KNID, project.Branch, paths, owned)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Preflight confirming project %s: %v", project.ID, err))
		return nil
	}
	return preflight
}

// projectStatus computes where a project stands from its draft and check, as the caller sees it,
// and, for a draft ready to confirm, from what the project branch would refuse it for (preflight,
// nil when not read).
// A draft drafting, or a check running, since before its stale time has ended without being
// recorded and reads as failed. The same data gives the same status, but for computedAt and what
// the caller may do.
func projectStatus(project *interfaces.Project, draft *interfaces.Draft, check *interfaces.Check,
	preflight *interfaces.DefinitionsPreflight, caller interfaces.AccountInfo, draftStaleBefore int64, checkStaleBefore int64,
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
	if n := itemDecisions(draft); n > 0 {
		generate.DiscardsDecisions = n
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
		Status: draft.Status, UpdateTime: draft.UpdateTime, Decisions: itemDecisions(draft),
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
	if draft.Items.Inputs != nil && draft.Items.Inputs.Partial {
		confirm.Body["accept_partial"] = true
		confirm.Accepts = []string{interfaces.ACCEPTS_PARTIAL_DRAFT}
	}
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
		// What the branch would refuse confirming for, read ahead; the content is checked only when
		// confirming.
		if preflight != nil {
			lineage := "/lineages/" + project.KNID
			switch preflight.NotV3 {
			case interfaces.DEFINITIONS_LINEAGE_NOT_V3:
				block(interfaces.BLOCKER_LINEAGE_NOT_V3, 0, interfaces.WAITING_FOR_EDITOR)
				act(interfaces.ProjectStatusNextAction{
					Action: interfaces.ACTION_COMMIT_DRIFT, Method: http.MethodPost, Path: lineage + "/drift?commit=true",
					Body: map[string]any{"kn_id": project.KNID}, Allowed: true,
				})
			case interfaces.DEFINITIONS_BRANCH_NOT_V3:
				block(interfaces.BLOCKER_BRANCH_NOT_V3, 0, interfaces.WAITING_FOR_EDITOR)
				act(interfaces.ProjectStatusNextAction{
					Action: interfaces.ACTION_SYNC_BRANCH, Method: http.MethodPost,
					Path: lineage + "/branches/" + project.Branch + "/sync", Allowed: true,
				})
			}
			if n := len(preflight.Existing); n > 0 {
				block(interfaces.BLOCKER_DEFINITIONS_EXIST, n, interfaces.WAITING_FOR_EDITOR)
			}
			if len(s.Blockers) > 0 {
				confirm.Allowed, confirm.Reason = false, s.Blockers[0].Code
			}
		}
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

// itemDecisions counts the decisions on a draft's items, those drafting again would discard; a
// decision on the draft as a whole is about that draft only.
func itemDecisions(draft *interfaces.Draft) int {
	if draft == nil {
		return 0
	}
	n := 0
	for _, d := range draft.Items.Decisions {
		if d.Kind != interfaces.DRAFT_KIND_DRAFT {
			n++
		}
	}
	return n
}

func anyObjectTypeAdopted(items *interfaces.DraftItems) bool {
	for _, ot := range items.ObjectTypes {
		if ot.Adopted {
			return true
		}
	}
	return false
}
