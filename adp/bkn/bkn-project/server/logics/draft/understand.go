// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Semantic understanding of the tables in a project's data catalogs: vega-backend's tasks fill in
// the names, descriptions and English short names drafting then uses. vega-backend keeps the
// tasks; the progress is read from it each time.

// Statuses of a semantic understanding task under way, or done well.
const (
	semanticPending   = "pending"
	semanticRunning   = "running"
	semanticCompleted = "completed"
)

// newestTasks gives each table its newest task, from tasks listed newest first.
func newestTasks(tasks []*interfaces.VegaSemanticTask) map[string]*interfaces.VegaSemanticTask {
	newest := map[string]*interfaces.VegaSemanticTask{}
	for _, task := range tasks {
		if _, ok := newest[task.ResourceID]; !ok {
			newest[task.ResourceID] = task
		}
	}
	return newest
}

// understood tells whether a table needs no new task: one is queued or running, or one completed
// that was meant to write. A completed preview (dry_run) wrote nothing, so it does not count; a
// result too unsure to write does, as asking again would give the same.
func understood(task *interfaces.VegaSemanticTask) bool {
	if task == nil {
		return false
	}
	switch task.Status {
	case semanticPending, semanticRunning:
		return true
	case semanticCompleted:
		return task.ApplyMode != interfaces.VEGA_SEMANTIC_APPLY_DRY_RUN
	}
	return false
}

func (s *draftService) Understand(ctx context.Context, token string, projectID string,
	request interfaces.ProjectUnderstand, caller interfaces.AccountInfo) (*interfaces.ProjectUnderstandSubmitted, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Understand")
	defer span.End()

	applyMode := request.ApplyMode
	switch applyMode {
	case "":
		applyMode = interfaces.VEGA_SEMANTIC_APPLY_FILL_EMPTY
	case interfaces.VEGA_SEMANTIC_APPLY_FILL_EMPTY, interfaces.VEGA_SEMANTIC_APPLY_FORCE:
	default:
		span.SetStatus(codes.Error, "Invalid apply mode")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Understand_InvalidApplyMode)
	}
	entry, err := s.unarchivedProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	if entry.Owner.ID != caller.ID {
		span.SetStatus(codes.Error, "Not the owner")
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Understand_NotOwner)
	}
	if len(entry.CatalogIDs) == 0 {
		span.SetStatus(codes.Error, "No catalogs")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Understand_NoCatalogs)
	}
	language := entry.NamingLanguage
	if language == "" {
		language = interfaces.NAMING_LANGUAGE_ZH_CN
	}

	submitted := &interfaces.ProjectUnderstandSubmitted{Catalogs: []interfaces.UnderstandSubmittedCatalog{}}
	var resourceIDs []string
	for _, catalogID := range entry.CatalogIDs {
		// Creating tasks needs the catalog's task_manage permission, which listing them tells.
		tasks, permitted, err := s.va.ListSemanticTasks(ctx, caller, catalogID)
		if err != nil {
			span.SetStatus(codes.Error, "List semantic tasks failed")
			return nil, internalError(ctx, "List semantic tasks failed", err)
		}
		catalog := interfaces.UnderstandSubmittedCatalog{CatalogID: catalogID, Permitted: permitted}
		if permitted {
			tables, err := s.va.ListTables(ctx, caller, catalogID)
			if err != nil {
				span.SetStatus(codes.Error, "List tables failed")
				return nil, internalError(ctx, "List tables failed", err)
			}
			newest := newestTasks(tasks)
			for _, table := range tables {
				// A table understood, or being understood, is left alone unless all are asked for.
				if !request.All && understood(newest[table.ID]) {
					continue
				}
				resourceIDs = append(resourceIDs, table.ID)
				catalog.Tables++
			}
		}
		submitted.Catalogs = append(submitted.Catalogs, catalog)
	}

	background := context.WithoutCancel(ctx)
	s.run(func() {
		for _, id := range resourceIDs {
			if _, err := s.va.CreateSemanticTask(background, caller, id, language, applyMode); err != nil {
				// The progress shows the table not started; submitting again picks it up.
				otellog.LogWarn(background, fmt.Sprintf("Understand table %s of project %s: %v", id, projectID, err))
			}
		}
	})
	span.SetStatus(codes.Ok, "")
	return submitted, nil
}

func (s *draftService) GetUnderstanding(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.ProjectUnderstanding, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetUnderstanding")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	understanding := &interfaces.ProjectUnderstanding{Catalogs: []interfaces.UnderstandingCatalog{}}
	for _, catalogID := range entry.CatalogIDs {
		tasks, visible, err := s.va.ListSemanticTasks(ctx, caller, catalogID)
		if err != nil {
			span.SetStatus(codes.Error, "List semantic tasks failed")
			return nil, internalError(ctx, "List semantic tasks failed", err)
		}
		catalog := interfaces.UnderstandingCatalog{CatalogID: catalogID, Visible: visible, Counts: map[string]int{}}
		if visible {
			tables, err := s.va.ListTables(ctx, caller, catalogID)
			if err != nil {
				span.SetStatus(codes.Error, "List tables failed")
				return nil, internalError(ctx, "List tables failed", err)
			}
			newest := newestTasks(tasks)
			for _, table := range tables {
				item := interfaces.UnderstandingTable{ResourceID: table.ID, Name: table.Name,
					Status: interfaces.UNDERSTAND_NOT_STARTED}
				if task := newest[table.ID]; task != nil {
					item.Status, item.Applied, item.FinishTime = task.Status, task.Applied, task.FinishTime
				}
				catalog.Counts[item.Status]++
				catalog.Tables = append(catalog.Tables, item)
			}
		}
		understanding.Catalogs = append(understanding.Catalogs, catalog)
	}
	span.SetStatus(codes.Ok, "")
	return understanding, nil
}
