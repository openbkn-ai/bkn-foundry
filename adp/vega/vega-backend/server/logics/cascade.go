// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// CascadeDeleteBuildTasks deletes all build tasks hit by the filter.
// Physical OpenSearch indexes are reconciled asynchronously by IndexCleanupWorker.
//
// The filter must set either ResourceID for one resource or CatalogID for every resource in a catalog.
// If a matching task is running or stopping, HasRunningExecution is returned and nothing is deleted.
//
// Errors are returned only when task-row deletion fails. This helper lives in logics because both the
// resource and catalog services use it, while logics/build_task already depends on logics/catalog.
func CascadeDeleteBuildTasks(ctx context.Context, bta interfaces.BuildTaskAccess, filter interfaces.BuildTasksQueryParams) error {
	// Limit=0 disables pagination so historical tasks and their orphan indexes are included.
	filter.Limit = 0
	filter.Offset = 0
	tasks, err := bta.InternalList(ctx, filter)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	// First, conduct an overall verification of the running state: Reject any tasks that are running and never delete half of them
	running := make([]string, 0)
	for _, t := range tasks {
		if t.Status == interfaces.BuildTaskStatusRunning || t.Status == interfaces.BuildTaskStatusStopping {
			running = append(running, t.ID)
		}
	}
	if len(running) > 0 {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_HasRunningExecution).
			WithErrorDetails(map[string]any{"running_ids": running})
	}

	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	deleted, err := bta.DeleteByIDs(ctx, ids)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}
	if deleted != int64(len(ids)) {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_DeleteFailed).
			WithErrorDetails(map[string]any{"expected": len(ids), "deleted": deleted})
	}
	return nil
}
