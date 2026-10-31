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
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// vega-backend's refusal of a build while one of the table is under way.
const vegaBuildTaskExist = "VegaBackend.BuildTask.Exist"

// BuildIndex builds the local indexes of the last confirmation again, as the project owner, and
// returns the readiness with what each build did. Only a draft still as it was confirmed can be
// built again: what a confirmation adopted is otherwise on the branch only.
func (s *draftService) BuildIndex(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.ProjectIndex, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildIndex")
	defer span.End()

	entry, err := s.unarchivedProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	if entry.Owner.ID != caller.ID {
		span.SetStatus(codes.Error, "Not the owner")
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Index_NotOwner)
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist || draft.Items.Confirmation == nil || draft.Items.Confirmation.Commit == "" {
		span.SetStatus(codes.Error, "Nothing confirmed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Index_NothingConfirmed)
	}
	confirmation := draft.Items.Confirmation
	if draft.Status != interfaces.DRAFT_STATUS_READY || confirmation.UpdateTime != draft.UpdateTime {
		span.SetStatus(codes.Error, "Confirmation unknown")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Index_ConfirmAgain)
	}
	// Builds started are recorded even if the caller leaves before they all start.
	if err := s.buildIndexes(context.WithoutCancel(ctx), projectID, confirmation.Commit, &draft.Items,
		caller); err != nil {
		span.SetStatus(codes.Error, "Build indexes failed")
		return nil, internalError(ctx, "Build indexes failed", err)
	}
	index, _, err := s.readIndex(ctx, &draft.Items, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Read tables failed")
		return nil, internalError(ctx, "Read tables failed", err)
	}
	s.attachBuilds(ctx, index, projectID, confirmation.Commit)
	span.SetStatus(codes.Ok, "")
	return index, nil
}

// buildIndexes starts, as the runner, a build of each adopted object type's local index that is
// not built or went stale, and records under the confirmed commit what each start did. A table
// lacking the keys a build needs is first given them from its object type: its identity's fields
// and its update field's, or its identity's again without one, when vega-backend takes their types.
// An update field chosen after the identity's fields stood in for it replaces them, and the index
// is built again. A table already being built is
// recorded with its task; ready and unreadable tables start nothing.
func (s *draftService) buildIndexes(ctx context.Context, projectID string, commit string,
	items *interfaces.DraftItems, runner interfaces.AccountInfo) error {
	index, tables, err := s.readIndex(ctx, items, runner)
	if err != nil {
		return fmt.Errorf("read index readiness: %w", err)
	}
	var builds []*interfaces.IndexBuild
	for _, item := range index.ObjectTypes {
		build := &interfaces.IndexBuild{ProjectID: projectID, Commit: commit, ResourceID: item.Table.ResourceID,
			ObjectTypeID: item.ID, Runner: runner}
		objectType, table := findObjectType(items, item.ID), tables[item.Table.ResourceID]
		switch {
		case item.State == interfaces.INDEX_STATE_BUILDING && item.Task != nil:
			build.Outcome, build.TaskID = interfaces.INDEX_BUILD_EXISTING, item.Task.ID
		case item.FullRebuildOnly && objectType.IncrementalKey != "" && item.State != interfaces.INDEX_STATE_UNREADABLE &&
			item.State != interfaces.INDEX_STATE_BUILDING:
			// An update field chosen since takes the place of the identity's fields standing in for it.
			keys, unknown := indexKeys(objectType, table, []string{interfaces.INDEX_FIELD_INCREMENTAL})
			if len(unknown) > 0 {
				build.Outcome, build.Reason = interfaces.INDEX_BUILD_NOT_CONFIGURABLE, strings.Join(unknown, ",")
				break
			}
			keys.ReplaceIncremental = table.Index.IncrementalFields
			changed, err := s.va.UpdateIndexConfig(ctx, runner, item.Table.ResourceID, keys)
			if err != nil {
				build.Outcome, build.Reason = configOutcome(err)
				break
			}
			// Someone changed the fields in vega since: the index is built only as its state asks.
			if !changed && item.State != interfaces.INDEX_STATE_NOT_BUILT && item.State != interfaces.INDEX_STATE_STALE {
				continue
			}
			build.TaskID, err = s.va.CreateBuildTask(ctx, runner, item.Table.ResourceID)
			build.Outcome, build.Reason = buildOutcome(err)
		case item.State == interfaces.INDEX_STATE_NOT_BUILT || item.State == interfaces.INDEX_STATE_STALE:
			build.TaskID, err = s.va.CreateBuildTask(ctx, runner, item.Table.ResourceID)
			build.Outcome, build.Reason = buildOutcome(err)
		case item.State == interfaces.INDEX_STATE_NOT_CONFIGURED:
			keys, unknown := indexKeys(objectType, table, item.Missing)
			if len(unknown) > 0 {
				build.Outcome, build.Reason = interfaces.INDEX_BUILD_NOT_CONFIGURABLE, strings.Join(unknown, ",")
				break
			}
			if _, err = s.va.UpdateIndexConfig(ctx, runner, item.Table.ResourceID, keys); err != nil {
				build.Outcome, build.Reason = configOutcome(err)
				break
			}
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

// primaryKeyTypes are the types vega-backend keys a table's local index by (PRIMARY_KEY_TYPES).
var primaryKeyTypes = map[string]bool{"integer": true, "unsigned integer": true, "string": true}

// indexKeys gives, from the object type, the keys of missing its table lacks: its identity's
// fields and its update field's, or its identity's fields again when it has none, each of a vega
// type the index takes. It returns the keys it
// cannot give, as INDEX_FIELD_*.
func indexKeys(objectType *interfaces.DraftObjectType, table *interfaces.VegaTable,
	missing []string) (interfaces.VegaIndexKeys, []string) {
	fields := func(properties []string, types map[string]bool) []string {
		var names []string
		for _, name := range properties {
			property := findProperty(objectType, name)
			if property == nil {
				return nil
			}
			c, ok := column(table, property.MappedField)
			if !ok || !types[c.Type] {
				return nil
			}
			names = append(names, c.Name)
		}
		return names
	}
	var keys interfaces.VegaIndexKeys
	var unknown []string
	for _, key := range missing {
		switch key {
		case interfaces.INDEX_FIELD_PRIMARY_KEY:
			if keys.PrimaryKeyFields = fields(objectType.PrimaryKeys, primaryKeyTypes); keys.PrimaryKeyFields == nil {
				unknown = append(unknown, key)
			}
		case interfaces.INDEX_FIELD_INCREMENTAL:
			// Without an update field the identity's fields stand in: a full build reads the table in
			// their order, and only full builds can refresh the index.
			switch {
			case objectType.IncrementalKey != "":
				keys.IncrementalFields = fields([]string{objectType.IncrementalKey}, incrementalTypes)
			case len(table.Index.PrimaryKeyFields) > 0:
				// The table's own primary key fields stand in, so that they read as standing in.
				keys.IncrementalFields = table.Index.PrimaryKeyFields
			default:
				keys.IncrementalFields = fields(objectType.PrimaryKeys, incrementalTypes)
			}
			if keys.IncrementalFields == nil {
				unknown = append(unknown, key)
			}
		}
	}
	return keys, unknown
}

// configOutcome is the outcome of a refused index configuration, and why.
func configOutcome(err error) (string, string) {
	var vegaErr *interfaces.VegaError
	if errors.As(err, &vegaErr) && vegaErr.HTTPCode == http.StatusForbidden &&
		vegaErr.ErrorCode == httperrors.PublicError_Forbidden {
		return interfaces.INDEX_BUILD_NO_CONFIG_PERMISSION, ""
	}
	return interfaces.INDEX_BUILD_FAILED, err.Error()
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
