// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/lineage"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/project"
)

// Reasons a draft failed, stored as codes for the client to word.
const (
	FailureInterrupted = "interrupted"
	FailureTimeout     = "timeout"
)

// Reasons a table could not be read.
const (
	unreadableNotVisible  = "not_visible"
	unreadableNoStructure = "no_structure"
)

var (
	dsOnce sync.Once
	ds     interfaces.DraftService
)

type draftService struct {
	setting common.DraftSetting
	da      interfaces.DraftAccess
	// ca records the check run after a draft is confirmed.
	ca interfaces.CheckAccess
	// ira keeps the ids of items drafted from names that are not ASCII, so drafts do not change them.
	ira interfaces.IDRegistryAccess
	va  interfaces.VegaAccess
	// iba records the local index builds a confirmation starts.
	iba interfaces.IndexBuildAccess
	ps  interfaces.ProjectService
	// ls commits a confirmed draft to its project branch.
	ls interfaces.LineageService
	// run drafts in the background; tests replace it to run in line.
	run func(fn func())
	// checkRuns holds each project's running check, a *checkRun, so a later run can stop it.
	checkRuns sync.Map
}

func NewDraftService(appSetting *common.AppSetting) interfaces.DraftService {
	dsOnce.Do(func() {
		ds = &draftService{
			setting: appSetting.DraftSetting,
			da:      logics.DA,
			ca:      logics.CA,
			ira:     logics.IRA,
			va:      logics.VA,
			iba:     logics.IBA,
			ps:      project.NewProjectService(),
			ls:      lineage.NewLineageService(),
			run:     func(fn func()) { go fn() },
		}
	})
	return ds
}

func internalError(ctx context.Context, operation string, err error) error {
	otellog.LogError(ctx, operation, err)
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError).
		WithErrorDetails(err.Error())
}

// unarchivedProject returns the project the caller can see, refusing an archived one: its
// draft no longer changes.
func (s *draftService) unarchivedProject(ctx context.Context, token string,
	projectID string) (*interfaces.ProjectEntry, error) {
	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		return nil, err
	}
	if entry.Archived {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_ProjectArchived)
	}
	return entry, nil
}

func (s *draftService) GenerateDraft(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo, discardDecisions bool) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GenerateDraft")
	defer span.End()

	entry, err := s.unarchivedProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	if len(entry.CatalogIDs) == 0 {
		span.SetStatus(codes.Error, "No catalogs")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_NoCatalogs)
	}
	// vega-backend lists no tables for a catalog that is gone, which would draft an empty ontology.
	missing, err := s.va.MissingCatalogs(ctx, caller, entry.CatalogIDs)
	if err != nil {
		span.SetStatus(codes.Error, "Check catalogs failed")
		return nil, internalError(ctx, "Check catalogs failed", err)
	}
	if len(missing) > 0 {
		span.SetStatus(codes.Error, "Catalog missing")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_CatalogMissing).
			WithErrorDetails(strings.Join(missing, ", "))
	}

	now := time.Now().UnixMilli()
	draft := &interfaces.Draft{
		ProjectID:  projectID,
		Status:     interfaces.DRAFT_STATUS_DRAFTING,
		Items:      interfaces.DraftItems{},
		Drafter:    caller,
		CreateTime: now,
		UpdateTime: now,
	}
	// A draft still drafting after the drafting timeout, with a minute to record it, has ended
	// without being recorded, e.g. on a database error; it no longer holds the project.
	staleBefore := now - (s.setting.Timeout + time.Minute).Milliseconds()
	started, err := s.da.StartDraft(ctx, draft, staleBefore, discardDecisions)
	if errors.Is(err, interfaces.ErrDraftHasDecisions) {
		span.SetStatus(codes.Error, "Draft has decisions")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_HasDecisions)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Start draft failed")
		return nil, internalError(ctx, "Start draft failed", err)
	}
	if !started {
		span.SetStatus(codes.Error, "Drafting")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_Drafting)
	}

	catalogIDs, language := entry.CatalogIDs, entry.NamingLanguage
	// Drafting outlives the request; it keeps the trace but not the request's cancellation.
	background := context.WithoutCancel(ctx)
	// StartDraft kept what the last confirmation wrote; the new draft keeps it too.
	carried := draft.Items.Confirmation
	s.run(func() { s.draft(background, projectID, catalogIDs, language, caller, carried) })

	span.SetStatus(codes.Ok, "")
	return draft, nil
}

// draft reads the catalogs' tables as the caller, drafts and records the result.
func (s *draftService) draft(ctx context.Context, projectID string, catalogIDs []string, language string,
	caller interfaces.AccountInfo, confirmation *interfaces.DraftConfirmation) {
	ctx, cancel := context.WithTimeout(ctx, s.setting.Timeout)
	defer cancel()
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DraftFromTables")
	defer span.End()

	result := &interfaces.Draft{ProjectID: projectID, Status: interfaces.DRAFT_STATUS_READY}
	items, err := s.draftFromTables(ctx, projectID, catalogIDs, language, caller)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		result.Status, result.Error = interfaces.DRAFT_STATUS_FAILED, FailureTimeout
	case err != nil:
		result.Status, result.Error = interfaces.DRAFT_STATUS_FAILED, err.Error()
	default:
		result.Items = items
	}
	result.Items.Confirmation = confirmation
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Draft of project %s failed: %v", projectID, err))
		span.SetStatus(codes.Error, "Draft failed")
	}
	result.UpdateTime = time.Now().UnixMilli()
	// The outcome is recorded even when drafting ran out of time.
	if err := s.da.FinishDraft(context.WithoutCancel(ctx), result); err != nil {
		otellog.LogError(ctx, fmt.Sprintf("Record draft of project %s failed", projectID), err)
	}
}

func (s *draftService) draftFromTables(ctx context.Context, projectID string, catalogIDs []string, language string,
	caller interfaces.AccountInfo) (interfaces.DraftItems, error) {
	listed := []*interfaces.VegaTable{}
	for _, catalogID := range catalogIDs {
		tables, err := s.va.ListTables(ctx, caller, catalogID)
		if err != nil {
			return interfaces.DraftItems{}, fmt.Errorf("list tables of catalog %s: %w", catalogID, err)
		}
		listed = append(listed, tables...)
	}
	ids := make([]string, 0, len(listed))
	for _, table := range listed {
		ids = append(ids, table.ID)
	}
	read, err := s.va.GetTables(ctx, caller, ids)
	if err != nil {
		return interfaces.DraftItems{}, fmt.Errorf("read tables: %w", err)
	}
	byID := map[string]*interfaces.VegaTable{}
	for _, table := range read {
		byID[table.ID] = table
	}

	tables := []*interfaces.VegaTable{}
	unreadable := []*interfaces.DraftTable{}
	for _, table := range listed {
		full, ok := byID[table.ID]
		switch {
		case !ok:
			unreadable = append(unreadable, &interfaces.DraftTable{
				ResourceID: table.ID, Name: table.Name, CatalogID: table.CatalogID, Reason: unreadableNotVisible,
			})
		case len(full.Columns) == 0:
			unreadable = append(unreadable, &interfaces.DraftTable{
				ResourceID: table.ID, Name: table.Name, CatalogID: table.CatalogID, Reason: unreadableNoStructure,
			})
		default:
			tables = append(tables, full)
		}
	}
	registered, err := s.ira.ListIDs(ctx, projectID)
	if err != nil {
		return interfaces.DraftItems{}, fmt.Errorf("read registered ids: %w", err)
	}
	c := &vegaCounter{va: s.va, account: caller, timeoutSeconds: s.setting.QueryTimeout}
	d := newDrafter(s.setting, c).withRegisteredIDs(projectID, registered).withNamingLanguage(language)
	items, err := d.draft(ctx, tables, unreadable)
	if err != nil {
		return items, err
	}
	// Ids are registered before the draft is recorded: a draft whose ids later drafts could not
	// give again must not stand.
	now := time.Now().UnixMilli()
	for _, id := range d.recorded {
		id.CreateTime, id.UpdateTime = now, now
	}
	if err := s.ira.RecordIDs(ctx, d.recorded); err != nil {
		return interfaces.DraftItems{}, fmt.Errorf("record ids: %w", err)
	}
	// Tables drafting itself could not read, such as a code table whose values it could not
	// query, are in items.Unreadable too.
	items.Inputs = draftInputsOf(s.setting, catalogIDs, tables, items.Unreadable)
	items.Inputs.NamingLanguage = d.language()
	items.Inputs.IDs = registryInputsOf(registered, d.recorded, d.named)
	return items, nil
}

func (s *draftService) GetDraft(ctx context.Context, token string, projectID string) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetDraft")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist {
		span.SetStatus(codes.Error, "Draft not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}

func (s *draftService) FailInterrupted(ctx context.Context) error {
	n, err := s.da.FailDrafting(ctx, FailureInterrupted, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if n > 0 {
		logger.Warnf("Failed %d drafts left drafting by a previous run", n)
	}
	// A check is not retried by itself after a restart: a person runs it again.
	if n, err = s.ca.FailRunning(ctx, FailureInterrupted, time.Now().UnixMilli()); err != nil {
		return err
	}
	if n > 0 {
		logger.Warnf("Failed %d checks left running by a previous run", n)
	}
	return nil
}
