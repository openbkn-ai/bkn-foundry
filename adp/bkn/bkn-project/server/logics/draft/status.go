// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// firstVersion is the version a lineage's first release is suggested.
const firstVersion = "1.0.0"

// waitSeconds is how soon to read the status again while drafting or a check runs.
const waitSeconds = 5

// GetStatus computes where a project stands, as the caller sees it. Only a draft ready to confirm
// is read against its project branch; a preflight that fails leaves out what it would tell, and
// confirming checks it anyway.
func (s *draftService) GetStatus(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.ProjectStatus, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetStatus")
	defer span.End()

	entry, networkMissing, err := s.ps.GetProjectForStatus(ctx, token, projectID, caller)
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
	if err := s.withRequirement(ctx, projectID, draft, status); err != nil {
		span.SetStatus(codes.Error, "Read requirement failed")
		return nil, internalError(ctx, "Read requirement failed", err)
	}
	// Only a current confirmation can have been published.
	if status.Confirmation != nil && status.Confirmation.Current {
		latest, err := s.ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: entry.KNID, Limit: 1})
		if err != nil {
			span.SetStatus(codes.Error, "List releases failed")
			return nil, internalError(ctx, "List releases failed", err)
		}
		sw, switched, err := s.ra.GetLatestSwitch(ctx, entry.KNID)
		if err != nil {
			span.SetStatus(codes.Error, "Get switch failed")
			return nil, internalError(ctx, "Get switch failed", err)
		}
		if !switched {
			sw = nil
		}
		var newest *interfaces.Release
		if len(latest) > 0 {
			newest = latest[0]
		}
		withRelease(status, entry.Project, newest, sw, caller)
	}
	// Polled while drafting, which reads the catalogs itself.
	if status.Stage != interfaces.PROJECT_STAGE_DRAFTING {
		status.DataSources = s.dataSources(ctx, entry.Project, draft, networkMissing, caller)
		withDataSources(status, entry.Project, caller)
	}
	span.SetStatus(codes.Ok, "")
	return status, nil
}

// boundTable is a table a draft item reads its records from.
type boundTable struct {
	resourceID string
	item       interfaces.ProjectStatusAffected
}

// boundTables are the tables of a ready draft's adopted object types and the link tables of its
// adopted relation types, in the draft's order. A value type's values are in the definitions.
func boundTables(draft *interfaces.Draft) []boundTable {
	if draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY {
		return nil
	}
	bound := []boundTable{}
	for _, ot := range draft.Items.ObjectTypes {
		if ot.Adopted && ot.Table.ResourceID != "" {
			bound = append(bound, boundTable{resourceID: ot.Table.ResourceID, item: interfaces.ProjectStatusAffected{
				Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ID: ot.ID, Name: ot.Name, Table: ot.Table.Name,
			}})
		}
	}
	for _, rt := range draft.Items.RelationTypes {
		if rt.Adopted && rt.LinkTable != nil && rt.LinkTable.ResourceID != "" {
			bound = append(bound, boundTable{resourceID: rt.LinkTable.ResourceID, item: interfaces.ProjectStatusAffected{
				Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ID: rt.ID, Name: rt.Name, Table: rt.LinkTable.Name,
			}})
		}
	}
	return bound
}

// dataSources reads, as the caller, whether the project's catalogs and the tables its draft binds
// are still in vega; networkMissing is what reading the project told of its network. vega not
// answering leaves the dependency unreachable.
func (s *draftService) dataSources(ctx context.Context, project *interfaces.Project, draft *interfaces.Draft,
	networkMissing bool, caller interfaces.AccountInfo) *interfaces.ProjectStatusDataSources {
	ds := &interfaces.ProjectStatusDataSources{
		Network: interfaces.ProjectStatusDependency{State: interfaces.DATA_SOURCE_OK, Total: 1},
	}
	if networkMissing {
		ds.Network.State, ds.Network.Missing = interfaces.DATA_SOURCE_MISSING, 1
	}

	ds.Catalogs = interfaces.ProjectStatusDependency{State: interfaces.DATA_SOURCE_NOT_BOUND, Total: len(project.CatalogIDs)}
	if len(project.CatalogIDs) > 0 {
		missing, err := s.va.MissingCatalogs(ctx, caller, project.CatalogIDs)
		switch {
		case err != nil:
			otellog.LogWarn(ctx, fmt.Sprintf("Read catalogs of project %s: %v", project.ID, err))
			ds.Catalogs.State = interfaces.DATA_SOURCE_UNREACHABLE
		case len(missing) > 0:
			ds.Catalogs.State, ds.Catalogs.Missing = interfaces.DATA_SOURCE_MISSING, len(missing)
		default:
			ds.Catalogs.State = interfaces.DATA_SOURCE_OK
		}
	}

	bound := boundTables(draft)
	ds.Tables = interfaces.ProjectStatusDependency{State: interfaces.DATA_SOURCE_NOT_BOUND, Total: len(bound)}
	if len(bound) == 0 {
		return ds
	}
	ids, seen := []string{}, map[string]bool{}
	for _, b := range bound {
		if !seen[b.resourceID] {
			seen[b.resourceID] = true
			ids = append(ids, b.resourceID)
		}
	}
	missing, err := s.va.MissingTables(ctx, caller, ids)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read tables of project %s: %v", project.ID, err))
		ds.Tables.State = interfaces.DATA_SOURCE_UNREACHABLE
		return ds
	}
	gone := map[string]bool{}
	for _, id := range missing {
		gone[id] = true
	}
	ds.Tables.State = interfaces.DATA_SOURCE_OK
	for _, b := range bound {
		if gone[b.resourceID] {
			ds.Tables.Affected = append(ds.Tables.Affected, b.item)
		}
	}
	if ds.Tables.Missing = len(ds.Tables.Affected); ds.Tables.Missing > 0 {
		ds.Tables.State = interfaces.DATA_SOURCE_MISSING
	}
	return ds
}

// withDataSources has what is gone block the project, ahead of what else does, and refuses the
// actions it stops. A deleted network leaves the owner only archiving; a deleted catalog, choosing
// the catalogs again; a deleted table, drafting again. An archived project only shows it, and
// what cannot be told blocks nothing.
func withDataSources(s *interfaces.ProjectStatus, project *interfaces.Project, caller interfaces.AccountInfo) {
	ds := s.DataSources
	blockers := []interfaces.ProjectStatusBlocker{}
	for _, d := range []struct {
		dep  interfaces.ProjectStatusDependency
		code string
	}{
		{ds.Network, interfaces.BLOCKER_NETWORK_MISSING},
		{ds.Catalogs, interfaces.BLOCKER_CATALOG_MISSING},
		{ds.Tables, interfaces.BLOCKER_TABLE_MISSING},
	} {
		if d.dep.State == interfaces.DATA_SOURCE_MISSING {
			blockers = append(blockers, interfaces.ProjectStatusBlocker{
				Code: d.code, Count: d.dep.Missing, WaitingFor: interfaces.WAITING_FOR_OWNER,
			})
		}
	}
	if len(blockers) == 0 {
		return
	}
	s.Blockers = append(blockers, s.Blockers...)
	if s.Stage == interfaces.PROJECT_STAGE_ARCHIVED {
		return
	}
	base := "/projects/" + project.ID
	isOwner := caller.ID == project.Owner.ID
	ownerOnly := func(a interfaces.ProjectStatusNextAction) interfaces.ProjectStatusNextAction {
		a.Allowed = isOwner
		if !isOwner {
			a.Reason = interfaces.ACTION_REASON_NOT_OWNER
		}
		return a
	}
	refuse := func(code string, actions ...string) {
		for i := range s.NextActions {
			if a := &s.NextActions[i]; a.Allowed && (len(actions) == 0 || slices.Contains(actions, a.Action)) {
				a.Allowed, a.Reason = false, code
			}
		}
	}

	if ds.Network.State == interfaces.DATA_SOURCE_MISSING {
		refuse(interfaces.BLOCKER_NETWORK_MISSING)
		s.NextActions = append([]interfaces.ProjectStatusNextAction{ownerOnly(interfaces.ProjectStatusNextAction{
			Action: interfaces.ACTION_ARCHIVE, Method: http.MethodPost, Path: base + ":archive",
		})}, s.NextActions...)
		return
	}
	if ds.Tables.State == interfaces.DATA_SOURCE_MISSING {
		refuse(interfaces.BLOCKER_TABLE_MISSING,
			interfaces.ACTION_CONFIRM_DRAFT, interfaces.ACTION_RUN_CHECK, interfaces.ACTION_REVIEW_RELEASE)
	}
	if ds.Catalogs.State == interfaces.DATA_SOURCE_MISSING {
		refuse(interfaces.BLOCKER_CATALOG_MISSING, interfaces.ACTION_GENERATE_DRAFT)
		s.NextActions = append([]interfaces.ProjectStatusNextAction{ownerOnly(interfaces.ProjectStatusNextAction{
			Action: interfaces.ACTION_SET_CATALOGS, Method: http.MethodPut, Path: base + "/catalogs",
		})}, s.NextActions...)
	}
}

// withRelease reads a status against latest, the lineage's newest release or nil before any. Until
// the project is published, reviewing a release names the next version, which the client may
// change. The status is published when latest is the project's and came after its current
// confirmation; consumers not yet on it wait for the owner to switch them. What blocked the
// confirmation from being published no longer does. sw is the lineage's latest switch, or nil.
func withRelease(s *interfaces.ProjectStatus, project *interfaces.Project, latest *interfaces.Release,
	sw *interfaces.Switch, caller interfaces.AccountInfo) {
	version := nextVersion(latest)
	for i := range s.NextActions {
		if a := &s.NextActions[i]; a.Action == interfaces.ACTION_REVIEW_RELEASE {
			a.Path += "?version=" + version
			a.Requires, a.SuggestedVersion = []string{interfaces.ACTION_REQUIRES_VERSION}, version
		}
	}
	if latest == nil || latest.ProjectID != project.ID || s.Confirmation == nil || !s.Confirmation.Current ||
		latest.CreateTime < s.Confirmation.Time {
		return
	}
	s.Stage = interfaces.PROJECT_STAGE_PUBLISHED
	s.Release = &interfaces.ProjectStatusRelease{Version: latest.Version, NetworkID: latest.NetworkID,
		InUse: sw != nil && sw.ToNetworkID == latest.NetworkID}
	s.Blockers = []interfaces.ProjectStatusBlocker{}
	actions := []interfaces.ProjectStatusNextAction{}
	if !s.Release.InUse {
		switchAction := interfaces.ProjectStatusNextAction{
			Action: interfaces.ACTION_SWITCH_CONSUMERS, Method: http.MethodPost, Path: "/projects/" + project.ID + "/switches",
			Body: map[string]any{"to_network_id": latest.NetworkID}, Allowed: caller.ID == project.Owner.ID,
		}
		if !switchAction.Allowed {
			switchAction.Reason = interfaces.ACTION_REASON_NOT_OWNER
		}
		actions = append(actions, switchAction)
	} else {
		rollback := interfaces.ProjectStatusNextAction{
			Action: interfaces.ACTION_REVIEW_ROLLBACK, Method: http.MethodGet, Path: "/projects/" + project.ID + "/rollback-plan",
			Allowed: caller.ID == project.Owner.ID,
		}
		if !rollback.Allowed {
			rollback.Reason = interfaces.ACTION_REASON_NOT_OWNER
		}
		actions = append(actions, rollback)
	}
	for _, a := range s.NextActions {
		if a.Action != interfaces.ACTION_REVIEW_RELEASE {
			actions = append(actions, a)
		}
	}
	s.NextActions = actions
}

// nextVersion is the patch after latest's version, or 1.0.0 before the first release.
func nextVersion(latest *interfaces.Release) string {
	if latest == nil {
		return firstVersion
	}
	parts := strings.Split(latest.Version, ".")
	patch, err := strconv.Atoi(parts[len(parts)-1])
	if len(parts) != 3 || err != nil {
		return firstVersion
	}
	return fmt.Sprintf("%s.%s.%d", parts[0], parts[1], patch+1)
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
			"index": base + "/ontology/index",
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
	if inputs := draft.Items.Inputs; inputs != nil && inputs.IDs != nil {
		s.Draft.Placeholders = inputs.IDs.Placeholders
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
		confirm.Accepts = append(confirm.Accepts, interfaces.ACCEPTS_PARTIAL_DRAFT)
	}
	if s.Undecided = len(undecidedItems(&draft.Items)); s.Undecided > 0 {
		confirm.Body["accept_undecided"] = true
		confirm.Accepts = append(confirm.Accepts, interfaces.ACCEPTS_UNDECIDED_ITEMS)
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
			switch preflight.NotV3 {
			case interfaces.DEFINITIONS_LINEAGE_NOT_V3:
				block(interfaces.BLOCKER_LINEAGE_NOT_V3, 0, interfaces.WAITING_FOR_EDITOR)
				act(ownerOnly(interfaces.ProjectStatusNextAction{
					Action: interfaces.ACTION_COMMIT_DRIFT, Method: http.MethodPost, Path: base + "/drift:commit",
				}))
			case interfaces.DEFINITIONS_BRANCH_NOT_V3:
				block(interfaces.BLOCKER_BRANCH_NOT_V3, 0, interfaces.WAITING_FOR_EDITOR)
				act(ownerOnly(interfaces.ProjectStatusNextAction{
					Action: interfaces.ACTION_SYNC_BRANCH, Method: http.MethodPost, Path: base + "/branch:sync",
				}))
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
	// Publishing goes on from a review, which reads the release as the caller.
	review := interfaces.ProjectStatusNextAction{
		Action: interfaces.ACTION_REVIEW_RELEASE, Method: http.MethodGet, Path: base + "/release-review", Allowed: true,
	}
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
		act(review)
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
		// The owner may publish knowing the check could not count every item.
		if code == interfaces.BLOCKER_CHECK_PARTIAL {
			act(review)
		}
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

// withRequirement counts, when a draft can be confirmed, the unresolved conflicts of the project's
// ready requirement specification, having confirming accept them as confirming asks, and the
// undecided items its knowledge questions need.
func (s *draftService) withRequirement(ctx context.Context, projectID string, draft *interfaces.Draft,
	status *interfaces.ProjectStatus) error {
	var confirm *interfaces.ProjectStatusNextAction
	for i := range status.NextActions {
		if status.NextActions[i].Action == interfaces.ACTION_CONFIRM_DRAFT {
			confirm = &status.NextActions[i]
		}
	}
	if confirm == nil {
		return nil
	}
	// One read: the conflicts and the questions are of the same specification.
	req, found, err := s.rqa.GetRequirement(ctx, projectID)
	if err != nil {
		return err
	}
	_, unresolved := requirementStateOf(req, found)
	if status.UnresolvedConflicts = len(unresolved); status.UnresolvedConflicts > 0 {
		confirm.Body["accept_unresolved_conflicts"] = true
		confirm.Accepts = append(confirm.Accepts, interfaces.ACCEPTS_UNRESOLVED_CONFLICTS)
	}
	needed := neededOf(req, found)
	for _, u := range undecidedItems(&draft.Items) {
		if len(needed[u.Kind+"/"+u.ItemID]) > 0 {
			status.UndecidedNeeded++
		}
	}
	return nil
}
