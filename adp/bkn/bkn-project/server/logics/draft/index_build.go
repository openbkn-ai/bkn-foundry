// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// vega-backend's refusal of a build while one of the table is under way.
const vegaBuildTaskExist = "VegaBackend.BuildTask.Exist"

// buildIndexes starts, as the runner, a build of each adopted object type's local index that is
// not built or went stale, and records under the confirmed commit what each start did. A table
// already being built is recorded with its task; ready, unconfigured and unreadable tables start
// nothing.
func (s *draftService) buildIndexes(ctx context.Context, projectID string, commit string,
	items *interfaces.DraftItems, runner interfaces.AccountInfo) error {
	index, err := s.readIndex(ctx, items, runner)
	if err != nil {
		return fmt.Errorf("read index readiness: %w", err)
	}
	var builds []*interfaces.IndexBuild
	for _, item := range index.ObjectTypes {
		build := &interfaces.IndexBuild{ProjectID: projectID, Commit: commit, ResourceID: item.Table.ResourceID,
			ObjectTypeID: item.ID, Runner: runner}
		switch {
		case item.State == interfaces.INDEX_STATE_BUILDING && item.Task != nil:
			build.Outcome, build.TaskID = interfaces.INDEX_BUILD_EXISTING, item.Task.ID
		case item.State == interfaces.INDEX_STATE_NOT_BUILT || item.State == interfaces.INDEX_STATE_STALE:
			build.TaskID, err = s.va.CreateBuildTask(ctx, runner, item.Table.ResourceID)
			build.Outcome, build.Reason = buildOutcome(err)
		default:
			continue
		}
		build.CreateTime = time.Now().UnixMilli()
		builds = append(builds, build)
	}
	if len(builds) == 0 {
		return nil
	}
	if err := s.iba.RecordBuilds(ctx, builds); err != nil {
		return fmt.Errorf("record index builds: %w", err)
	}
	return nil
}

// buildOutcome is the outcome of creating a build task, and why it did not start.
func buildOutcome(err error) (string, string) {
	if err == nil {
		return interfaces.INDEX_BUILD_CREATED, ""
	}
	var vegaErr *interfaces.VegaError
	if errors.As(err, &vegaErr) {
		switch {
		// A 403 may also be the connector's entitlement, which is no missing permission.
		case vegaErr.HTTPCode == http.StatusForbidden && vegaErr.ErrorCode == httperrors.PublicError_Forbidden:
			return interfaces.INDEX_BUILD_NO_PERMISSION, ""
		case vegaErr.HTTPCode == http.StatusBadRequest && vegaErr.ErrorCode == vegaBuildTaskExist:
			return interfaces.INDEX_BUILD_EXISTING, ""
		}
	}
	return interfaces.INDEX_BUILD_FAILED, err.Error()
}
