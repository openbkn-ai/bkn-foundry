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
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
)

// waitSeconds is how soon to read the status again while drafting or a check runs.
const waitSeconds = 5

// GetStatus computes where a project stands, as the caller sees it. Only a draft ready to confirm
// is read against its project branch; a preflight that fails leaves out what it would tell, and
// confirming checks it anyway.
func (s *draftService) GetStatus(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.ProjectStatus, error) {
	return s.getStatus(ctx, token, projectID, caller, true)
}

// getStatus computes the status; with rebind, an owner reading that tables the draft binds are
// gone has them bound again by catalog name and table name first, then the status read again.
func (s *draftService) getStatus(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo, rebind bool) (*interfaces.ProjectStatus, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetStatus")
	defer span.End()

	entry, networkMissing, err := s.ps.GetProjectForStatus(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	if !networkMissing {
		s.pc.Resolve(ctx, token, caller, entry)
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
	if draft != nil && draft.Status == interfaces.DRAFT_STATUS_READY {
		// Whether the knowledge base's objects are prepared for the confirmed model as it stands.
		documentHooks().DatasetProgress(ctx, draftHost{s}, draft)
	}
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
		latest, err := s.ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: entry.KNID,
			State: interfaces.RELEASE_STATE_PUBLISHED, Limit: 1})
		if err != nil {
			span.SetStatus(codes.Error, "List releases failed")
			return nil, internalError(ctx, "List releases failed", err)
		}
		var newest *interfaces.Release
		var running *interfaces.KNVersion
		first := logics.FIRST_RELEASE_VERSION
		if len(latest) > 0 {
			newest = latest[0]
			running = s.runningVersion(ctx, token, entry.KNID)
		} else {
			first = s.firstReleaseVersion(ctx, token, entry.KNID)
		}
		withRelease(status, entry.Project, newest, running, first, caller)
	}
	if draft != nil && draft.Status == interfaces.DRAFT_STATUS_READY {
		status.Unreviewed = unreviewed(&draft.Items)
	}
	// Polled while drafting, which reads the catalogs itself.
	// Before what is gone refuses the actions it stops, understanding among them.
	if namingStages[status.Stage] {
		s.withCatalogsHint(ctx, entry, draft, caller, status)
	}
	if status.Stage != interfaces.PROJECT_STAGE_DRAFTING {
		var existingGone bool
		status.DataSources, existingGone = s.dataSources(ctx, entry.Project, draft, networkMissing, caller)
		// Tables deleted and created again under their names are bound again, by the owner, who
		// alone writes the draft; what vega or the draft refuses leaves them gone, as before. The
		// network's existing items the draft holds follow too, though they block nothing.
		if rebind && (status.DataSources.Tables.State == interfaces.DATA_SOURCE_MISSING || existingGone) &&
			status.Stage != interfaces.PROJECT_STAGE_ARCHIVED && caller.ID == entry.Owner.ID {
			n, err := s.rebindTables(ctx, token, caller, entry, draft)
			if err != nil {
				otellog.LogWarn(ctx, fmt.Sprintf("Bind the tables of project %s again by name: %v", projectID, err))
			}
			if n > 0 {
				span.SetStatus(codes.Ok, "")
				return s.getStatus(ctx, token, projectID, caller, false)
			}
		}
		status.DataSources.Reconnected = reconnectedTables(draft)
		withDataSources(status, entry.Project, caller)
		s.withSourceChanges(ctx, status, entry.Project, draft, caller)
	}
	span.SetStatus(codes.Ok, "")
	return status, nil
}

// unnamedTablesHintRatio is the share of the catalogs' tables without a description above which
// the status hints at understanding them: more than half. Below it most items get business names
// already, and the rest are left to people.
const unnamedTablesHintRatio = 0.5

// namingStages are the stages where drafting, or drafting again, is still ahead of confirming, so
// names understanding gives would reach the draft.
var namingStages = map[string]bool{
	interfaces.PROJECT_STAGE_NO_DRAFT: true, interfaces.PROJECT_STAGE_DRAFT_FAILED: true,
	interfaces.PROJECT_STAGE_DECIDING: true, interfaces.PROJECT_STAGE_READY_TO_CONFIRM: true,
}

// withCatalogsHint reads, as the caller, the tables of the project's catalogs, and hints at
// understanding them when more than unnamedTablesHintRatio of them have no description: only
// semantic understanding or a person writes one. Only the tables drafting models count: those the
// network's existing object types read are modeled already, and named as the network names them.
// A read that fails gives no hint.
func (s *draftService) withCatalogsHint(ctx context.Context, entry *interfaces.ProjectEntry,
	draft *interfaces.Draft, caller interfaces.AccountInfo, status *interfaces.ProjectStatus) {
	project := entry.Project
	var owned []string
	if draft != nil {
		owned = ownedPaths(draft.Items.Confirmation)
	}
	existing, err := s.existingItems(ctx, entry, owned)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read what the network of project %s has: %v", project.ID, err))
		return
	}
	listed := []*interfaces.VegaTable{}
	for _, catalogID := range project.CatalogIDs {
		tables, err := s.va.ListTables(ctx, caller, catalogID)
		if err != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("List tables of project %s: %v", project.ID, err))
			return
		}
		listed = append(listed, tables...)
	}
	// The network's tables are known by name too, once a catalog was created again.
	if err := s.rebindExisting(ctx, caller, &existing, listed); err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read the tables the network of project %s reads: %v", project.ID, err))
		return
	}
	modeled := existingTableIDs(&existing)
	total, unnamed := 0, 0
	for _, t := range listed {
		if modeled[t.ID] {
			continue
		}
		total++
		if strings.TrimSpace(t.Description) == "" {
			unnamed++
		}
	}
	if float64(unnamed) <= unnamedTablesHintRatio*float64(total) {
		return
	}
	status.Hints = append(status.Hints, interfaces.ProjectStatusHint{
		Code: interfaces.HINT_CATALOGS_UNNAMED, Count: unnamed, Total: total,
	})
	understand := interfaces.ProjectStatusNextAction{
		Action: interfaces.ACTION_UNDERSTAND_CATALOGS, Method: http.MethodPost,
		Path: "/projects/" + project.ID + "/catalogs:understand", Allowed: caller.ID == project.Owner.ID,
	}
	if !understand.Allowed {
		understand.Reason = interfaces.ACTION_REASON_NOT_OWNER
	}
	status.NextActions = append(status.NextActions, understand)
}

// boundTable is a table a draft item reads its records from.
type boundTable struct {
	resourceID string
	// dataset is the dataset a knowledge-base extraction writes, which no catalog table is.
	dataset bool
	item    interfaces.ProjectStatusAffected
}

// boundTables are the tables of a ready draft's adopted object types and the link tables of its
// adopted relation types, in the draft's order. A value type's values are in the definitions.
func boundTables(draft *interfaces.Draft) []boundTable {
	if draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY {
		return nil
	}
	bound := []boundTable{}
	for _, ot := range draft.Items.ObjectTypes {
		if ot.Adopted && !ot.Existing && ot.Table.ResourceID != "" {
			bound = append(bound, boundTable{resourceID: ot.Table.ResourceID, dataset: ot.Document != nil, item: interfaces.ProjectStatusAffected{
				Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ID: ot.ID, Name: ot.Name, Table: ot.Table.Name,
				SourceTable: ot.Table.SourceName,
			}})
		}
	}
	for _, rt := range draft.Items.RelationTypes {
		if rt.Adopted && !rt.Existing && rt.LinkTable != nil && rt.LinkTable.ResourceID != "" {
			bound = append(bound, boundTable{resourceID: rt.LinkTable.ResourceID, dataset: rt.Document != nil, item: interfaces.ProjectStatusAffected{
				Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ID: rt.ID, Name: rt.Name, Table: rt.LinkTable.Name,
				SourceTable: rt.LinkTable.SourceName,
			}})
		}
	}
	return bound
}

// dataSources reads, as the caller, whether the project's catalogs and the tables its draft binds
// are still in vega; networkMissing is what reading the project told of its network. vega not
// answering leaves the dependency unreachable. It tells too whether a table the draft's copy of
// a network's existing item reads is gone: that blocks nothing, the network's item being the
// network's, but the copy is bound again.
func (s *draftService) dataSources(ctx context.Context, project *interfaces.Project, draft *interfaces.Draft,
	networkMissing bool, caller interfaces.AccountInfo) (*interfaces.ProjectStatusDataSources, bool) {
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
	// Counted as tables, each once, however many items read it: a knowledge-base dataset is no
	// table, though one that is gone still blocks.
	tables := map[string]bool{}
	for _, b := range bound {
		if !b.dataset {
			tables[b.resourceID] = true
		}
	}
	ds.Tables = interfaces.ProjectStatusDependency{State: interfaces.DATA_SOURCE_NOT_BOUND, Total: len(tables)}
	var existing []draftTableRef
	if draft != nil && draft.Status == interfaces.DRAFT_STATUS_READY {
		existing = existingTableRefs(&draft.Items)
	}
	if len(bound) == 0 && len(existing) == 0 {
		return ds, false
	}
	ids, seen := []string{}, map[string]bool{}
	for _, b := range bound {
		if !seen[b.resourceID] {
			seen[b.resourceID] = true
			ids = append(ids, b.resourceID)
		}
	}
	for _, ref := range existing {
		if !seen[ref.table.ResourceID] {
			seen[ref.table.ResourceID] = true
			ids = append(ids, ref.table.ResourceID)
		}
	}
	missing, err := s.va.MissingTables(ctx, caller, ids)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read tables of project %s: %v", project.ID, err))
		if len(bound) > 0 {
			ds.Tables.State = interfaces.DATA_SOURCE_UNREACHABLE
		}
		return ds, false
	}
	gone := map[string]bool{}
	for _, id := range missing {
		gone[id] = true
	}
	existingGone := false
	for _, ref := range existing {
		existingGone = existingGone || gone[ref.table.ResourceID]
	}
	if len(bound) == 0 {
		return ds, existingGone
	}
	ds.Tables.State = interfaces.DATA_SOURCE_OK
	for _, b := range bound {
		if gone[b.resourceID] {
			ds.Tables.Affected = append(ds.Tables.Affected, b.item)
		}
	}
	for id := range tables {
		if gone[id] {
			ds.Tables.Missing++
		}
	}
	if len(ds.Tables.Affected) > 0 {
		ds.Tables.State = interfaces.DATA_SOURCE_MISSING
	}
	return ds, existingGone
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
			// A gone table blocks each item that reads it.
			count := d.dep.Missing
			if d.code == interfaces.BLOCKER_TABLE_MISSING {
				count = len(d.dep.Affected)
			}
			blockers = append(blockers, interfaces.ProjectStatusBlocker{
				Code: d.code, Count: count, WaitingFor: interfaces.WAITING_FOR_OWNER,
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

// runningVersion is the version the network runs, or nil when it cannot be told; a status is read
// as before then.
func (s *draftService) runningVersion(ctx context.Context, token string, knID string) *interfaces.KNVersion {
	if s.kva == nil {
		return nil
	}
	versioning, err := s.kva.GetVersioning(ctx, token, knID)
	if err != nil || versioning.RunningVersion == 0 {
		return nil
	}
	versions, err := s.kva.ListVersions(ctx, token, knID)
	if err != nil {
		return &interfaces.KNVersion{Version: versioning.RunningVersion}
	}
	for _, v := range versions {
		if v.Version == versioning.RunningVersion {
			return v
		}
	}
	return &interfaces.KNVersion{Version: versioning.RunningVersion}
}

// firstReleaseVersion is the version suggested for the lineage's first release: a network that
// already has versions, as one a project started from, goes on from them.
func (s *draftService) firstReleaseVersion(ctx context.Context, token string, knID string) string {
	if s.kva == nil {
		return logics.FIRST_RELEASE_VERSION
	}
	versions, err := s.kva.ListVersions(ctx, token, knID)
	if err != nil {
		return logics.FIRST_RELEASE_VERSION
	}
	return logics.FirstReleaseVersion(versions)
}

// withRelease reads a status against latest, the lineage's newest published release or nil before
// any, and running, the version the network runs or nil when unknown. Until the project is
// published, reviewing a release names the next version, first before any release, which the
// client may change. The status
// is published when latest is the project's and came after its current confirmation; the owner may
// then roll it back. Once rolled back, the network runs another version: the status says so, and
// the owner may publish again instead. What blocked the confirmation from being published no
// longer does.
func withRelease(s *interfaces.ProjectStatus, project *interfaces.Project, latest *interfaces.Release,
	running *interfaces.KNVersion, first string, caller interfaces.AccountInfo) {
	version := nextVersion(latest, first)
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
	s.Release = &interfaces.ProjectStatusRelease{Version: latest.Version, NetworkID: latest.NetworkID, KNVersion: latest.KNVersion}
	s.Blockers = []interfaces.ProjectStatusBlocker{}
	if running != nil && running.Version != latest.KNVersion {
		// Publishing again goes through the review, which keeps its place among the actions.
		s.Release.RolledBack = true
		s.Release.Running = &interfaces.ProjectStatusRunning{KNVersion: running.Version, Label: running.Label}
		return
	}
	rollback := interfaces.ProjectStatusNextAction{
		Action: interfaces.ACTION_REVIEW_ROLLBACK, Method: http.MethodGet, Path: "/projects/" + project.ID + "/rollback-plan",
		Allowed: caller.ID == project.Owner.ID,
	}
	if !rollback.Allowed {
		rollback.Reason = interfaces.ACTION_REASON_NOT_OWNER
	}
	actions := []interfaces.ProjectStatusNextAction{rollback}
	for _, a := range s.NextActions {
		if a.Action != interfaces.ACTION_REVIEW_RELEASE {
			actions = append(actions, a)
		}
	}
	s.NextActions = actions
}

// nextVersion is the patch after latest's version, or first before the first release.
func nextVersion(latest *interfaces.Release, first string) string {
	if latest == nil {
		return first
	}
	parts := strings.Split(latest.Version, ".")
	patch, err := strconv.Atoi(parts[len(parts)-1])
	if len(parts) != 3 || err != nil {
		return logics.FIRST_RELEASE_VERSION
	}
	return fmt.Sprintf("%s.%s.%d", parts[0], parts[1], patch+1)
}

// preflightConfirm reads what the project branch would refuse confirming the draft for, with the
// paths confirming would write and those the draft owns from earlier confirmations; nil when it
// cannot tell.
func (s *draftService) preflightConfirm(ctx context.Context, project *interfaces.Project,
	draft *interfaces.Draft) *interfaces.DefinitionsPreflight {
	evidence, err := documentHooks().ConfirmEvidence(ctx, draftHost{s}, draft)
	if err != nil {
		return nil
	}
	compiled, err := compileDraft(&draft.Items, evidence)
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
		Hints:       []interfaces.ProjectStatusHint{},
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

	// Confirmed, with the knowledge base's objects prepared for another model or file selection, or
	// stopped part way: only confirming again prepares them, so it is offered again.
	kbOutdated := knowledgeBaseOutdated(draft.DatasetPreparation)
	if kbOutdated {
		s.Stale = append(s.Stale, interfaces.STALE_KNOWLEDGE_BASE_OUTDATED)
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
	if kbOutdated {
		act(confirm)
	}
	act(generate)
	return s
}

// knowledgeBaseOutdated tells whether a confirmed draft's knowledge-base objects wait for confirming
// again: prepared for another model or file selection, not started, or stopped part way. Preparing
// is under way only while a confirmation runs; a draft without knowledge-base objects has none.
func knowledgeBaseOutdated(preparation *interfaces.DatasetPreparation) bool {
	if preparation == nil {
		return false
	}
	switch preparation.State {
	case interfaces.DATASET_PREPARATION_STALE, interfaces.DATASET_PREPARATION_FAILED, interfaces.DATASET_PREPARATION_PENDING:
		return true
	}
	return false
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

// anyObjectTypeAdopted tells whether the draft adopts anything of its own to confirm; what the
// network already has is not.
func anyObjectTypeAdopted(items *interfaces.DraftItems) bool {
	for _, ot := range items.ObjectTypes {
		if ot.Adopted && !ot.Existing {
			return true
		}
	}
	return newRelationAdopted(items)
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

// withSourceChanges marks the project stale when a table its draft binds has columns added, removed
// or retyped since drafting, and offers the owner to draft them. Tables gone are data_sources'; vega
// not answering marks nothing.
func (s *draftService) withSourceChanges(ctx context.Context, status *interfaces.ProjectStatus,
	project *interfaces.Project, draft *interfaces.Draft, caller interfaces.AccountInfo) {
	if status.Stage == interfaces.PROJECT_STAGE_ARCHIVED || status.DataSources == nil ||
		status.DataSources.Tables.State != interfaces.DATA_SOURCE_OK {
		return
	}
	ids := draftSourceIDs(draft)
	if len(ids) == 0 {
		return
	}
	tables, err := s.va.GetTables(ctx, caller, ids)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read table structure of project %s: %v", project.ID, err))
		return
	}
	read := &sourceTables{tables: map[string]*interfaces.VegaTable{}, missing: map[string]bool{}}
	for _, t := range tables {
		read.tables[t.ID] = t
	}
	changes := draftSourceChanges(draft, read, s.setting.PrivacyKeywords)
	if changes.State != interfaces.SOURCE_CHANGES_CHANGED {
		return
	}
	status.Stale = append(status.Stale, interfaces.STALE_SOURCE_CHANGED)
	action := interfaces.ProjectStatusNextAction{
		Action: interfaces.ACTION_DRAFT_SOURCE_CHANGES, Method: http.MethodPost,
		Path: "/projects/" + project.ID + "/source-changes:draft", Body: map[string]any{"signature": changes.Signature},
		Allowed: caller.ID == project.Owner.ID,
	}
	if !action.Allowed {
		action.Reason = interfaces.ACTION_REASON_NOT_OWNER
	}
	status.NextActions = append(status.NextActions, action)
	status.Links["source_changes"] = "/projects/" + project.ID + "/source-changes"
}
