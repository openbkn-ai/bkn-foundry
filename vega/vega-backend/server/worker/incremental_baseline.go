// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/sync_checkpoint"
)

var (
	baselineLocalDatetime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(\.\d{1,9})?$`)
	baselineZonedDatetime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]([01]\d|2[0-3]):[0-5]\d)$`)
)

var errIncrementalBaselineTaskInactive = errors.New("task is no longer running during incremental baseline recovery")

// These public details are persisted in BuildTask.error_msg. The wrapped cause
// remains available to the worker for diagnostics.
type incrementalBaselineReadError struct {
	detail string
}

var (
	errBaselineUnsupportedCursorField = &incrementalBaselineReadError{detail: "cannot recover incremental checkpoint: unsupported cursor field"}
	errBaselineIndexQuery             = &incrementalBaselineReadError{detail: "cannot recover incremental checkpoint: published index query failed"}
	errBaselineInvalidCursor          = &incrementalBaselineReadError{detail: "cannot recover incremental checkpoint: invalid indexed cursor"}
)

func (e *incrementalBaselineReadError) Error() string { return e.detail }

func (btw *BuildTaskWorker) prepareIncrementalBaseline(ctx context.Context, task *interfaces.BuildTask,
	resource *interfaces.Resource, catalog *interfaces.Catalog) (bool, error) {
	failureDetail := "cannot recover a compatible incremental checkpoint from the published index"
	checkpoint, err := btw.recoverIncrementalBaseline(ctx, task.ID, resource)
	if btw.bbw.isStopping() || errors.Is(err, errIncrementalBaselineTaskInactive) {
		return false, nil
	}
	var readErr *incrementalBaselineReadError
	if errors.As(err, &readErr) {
		failureDetail = readErr.Error()
	}
	if err == nil {
		var saved bool
		saved, err = btw.persistRecoveredBaseline(ctx, task, resource, catalog.UpdateTime, checkpoint)
		if saved {
			resource.SyncMark = checkpoint
			task.SyncedMark = checkpoint
			return true, nil
		}
		if err != nil {
			failureDetail = "cannot save the recovered incremental checkpoint"
		} else {
			failureDetail = "resource, catalog, or task changed during incremental baseline recovery"
			err = errors.New(failureDetail)
		}
	}

	if btw.bbw.isStopping() {
		return false, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	failed, markErr := markBatchBuildFailed(ctx, btw.bts, task.ID, failureDetail)
	if markErr != nil {
		return false, errors.Join(err, markErr)
	}
	if failed {
		return false, err
	}
	return false, nil
}

func (btw *BuildTaskWorker) incrementalBaselineRecoveryActive(ctx context.Context, taskID string) (bool, error) {
	if btw.bbw.isStopping() {
		return false, nil
	}
	status, err := btw.bts.InternalGetStatusByID(ctx, taskID)
	if err != nil {
		return false, fmt.Errorf("get baseline recovery task status: %w", err)
	}
	if status == interfaces.BuildTaskStatusStopping {
		if _, err := btw.bts.InternalMarkStopped(ctx, nil, taskID, []string{interfaces.BuildTaskStatusStopping}); err != nil {
			return false, fmt.Errorf("stop baseline recovery task: %w", err)
		}
	}
	return status == interfaces.BuildTaskStatusRunning, nil
}

func (btw *BuildTaskWorker) persistRecoveredBaseline(ctx context.Context, task *interfaces.BuildTask,
	expected *interfaces.Resource, catalogUpdateTime int64, checkpoint string) (bool, error) {
	catalog, err := btw.bbw.cs.InternalGetByID(ctx, expected.CatalogID, false)
	if err != nil {
		return false, fmt.Errorf("reload recovery catalog: %w", err)
	}
	if catalog == nil || !catalog.Enabled || catalog.UpdateTime != catalogUpdateTime {
		return false, nil
	}
	if logics.DB == nil || logics.RA == nil {
		return false, errors.New("baseline recovery access is not initialized")
	}
	if btw.bbw.isStopping() {
		return false, nil
	}

	tx, err := logics.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, fmt.Errorf("begin baseline recovery transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 先以 CAS 取得资源写锁，再读取当前发布配置，保证期间的配置更新可见。
	updated, err := logics.RA.CompareAndSetSyncMark(ctx, tx,
		expected.ID, expected.LocalIndexName, interfaces.ResourceLocalIndexStatusAvailable, "", checkpoint)
	if err != nil {
		return false, fmt.Errorf("update recovered resource checkpoint: %w", err)
	}
	if !updated {
		return false, nil
	}
	current, err := btw.bbw.rs.InternalGetByID(ctx, tx, expected.ID)
	if err != nil {
		return false, fmt.Errorf("reload recovery resource: %w", err)
	}
	if !matchesRecoveryResource(current, expected, checkpoint) {
		return false, nil
	}

	progress := interfaces.BuildTaskProgress{SyncedMark: &checkpoint}
	updated, err = btw.bts.InternalSetProgress(ctx, tx, task.ID, progress, []string{interfaces.BuildTaskStatusRunning})
	if err != nil {
		return false, fmt.Errorf("update recovered task checkpoint: %w", err)
	}
	if !updated || btw.bbw.isStopping() {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit recovered checkpoints: %w", err)
	}
	return true, nil
}

func matchesRecoveryResource(current, expected *interfaces.Resource, checkpoint string) bool {
	if current == nil || current.ID != expected.ID || current.CatalogID != expected.CatalogID ||
		current.Category != expected.Category || current.Schema != expected.Schema ||
		current.SourceIdentifier != expected.SourceIdentifier || current.UpdateTime != expected.UpdateTime ||
		current.SyncMark != checkpoint || current.LocalIndexName != expected.LocalIndexName ||
		!resourcelogic.HasWritableLocalIndex(current) ||
		!reflect.DeepEqual(current.IndexConfig, expected.IndexConfig) ||
		!reflect.DeepEqual(current.SourceMetadata, expected.SourceMetadata) {
		return false
	}
	return reflect.DeepEqual(current.SchemaDefinition, expected.SchemaDefinition)
}

// recoverIncrementalBaseline 按已发布索引的排序读取游标，检查点由独立的短事务写入。
func (btw *BuildTaskWorker) recoverIncrementalBaseline(ctx context.Context, taskID string, resource *interfaces.Resource) (string, error) {
	if !resourcelogic.HasWritableLocalIndex(resource) || resource.IndexConfig == nil ||
		len(resource.IndexConfig.PrimaryKeyFields) == 0 || len(resource.IndexConfig.IncrementalFields) == 0 {
		return "", errors.New("incremental baseline requires a published index and configured build keys")
	}
	cursorFields := sync_checkpoint.EffectiveCursorFields(resource.IndexConfig.IncrementalFields, resource.IndexConfig.PrimaryKeyFields)
	fields, err := incrementalBaselineFields(cursorFields, resource.SchemaDefinition)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errBaselineUnsupportedCursorField, err)
	}
	sortFields := make([]*interfaces.SortField, 0, len(cursorFields))
	for _, name := range cursorFields {
		sortFields = append(sortFields, &interfaces.SortField{Field: name, Direction: "desc"})
	}
	params := &interfaces.ResourceDataQueryParams{
		Paging:       interfaces.PagingRequest{Limit: 1},
		Sort:         sortFields,
		OutputFields: cursorFields,
		NeedTotal:    true,
	}

	active, err := btw.incrementalBaselineRecoveryActive(ctx, taskID)
	if err != nil {
		return "", err
	}
	if !active {
		return "", errIncrementalBaselineTaskInactive
	}
	result, err := btw.bbw.lim.ListDocuments(ctx, resource.LocalIndexName, resource, params)
	if err != nil {
		return "", fmt.Errorf("%w: query published index: %w", errBaselineIndexQuery, err)
	}
	if result == nil || result.TotalCount < 0 {
		return "", fmt.Errorf("%w: published index search has an invalid result", errBaselineIndexQuery)
	}
	if result.TotalCount == 0 && len(result.Entries) == 0 {
		return sync_checkpoint.EncodeBatch(nil)
	}
	if result.TotalCount == 0 || len(result.Entries) != 1 {
		return "", fmt.Errorf("%w: published index search row count differs from its exact total", errBaselineIndexQuery)
	}

	row := result.Entries[0]
	cursor := make([]interfaces.KeyValue, len(fields))
	for i, field := range fields {
		value := row[field.Name]
		switch field.Type {
		case interfaces.DataType_Date, interfaces.DataType_Datetime, interfaces.DataType_Timestamp:
			if err := validateIncrementalBaselineTime(value, field.Type); err != nil {
				return "", fmt.Errorf("%w: read indexed cursor field %q: %w", errBaselineInvalidCursor, field.Name, err)
			}
		}
		cursor[i] = interfaces.KeyValue{Key: field.Name, Value: value}
	}
	// ValidateCursor normalizes values for SQL binding. Persist the original index values.
	checkpoint := &sync_checkpoint.SyncCheckpoint{Cursor: slices.Clone(cursor)}
	if err := sync_checkpoint.ValidateCursor(checkpoint, cursorFields, resource.SchemaDefinition); err != nil {
		return "", fmt.Errorf("%w: %w", errBaselineInvalidCursor, err)
	}
	return sync_checkpoint.EncodeBatch(cursor)
}

func incrementalBaselineFields(names []string, schema []*interfaces.Property) ([]*interfaces.Property, error) {
	sourceFields := make(map[string]*interfaces.Property, len(schema))
	for _, field := range schema {
		if field == nil || field.Name == "" || sourceFields[field.Name] != nil {
			return nil, errors.New("resource schema has invalid or duplicate fields")
		}
		sourceFields[field.Name] = field
	}
	fields := make([]*interfaces.Property, 0, len(names))
	for _, name := range names {
		field := sourceFields[name]
		if field == nil || field.OriginalName == "" || field.OriginalType == "" {
			return nil, fmt.Errorf("cursor field %q has incomplete source metadata", name)
		}
		switch field.Type {
		case interfaces.DataType_Integer, interfaces.DataType_UnsignedInteger,
			interfaces.DataType_Date, interfaces.DataType_Datetime, interfaces.DataType_Timestamp:
			fields = append(fields, field)
		default:
			return nil, fmt.Errorf("cursor field %q cannot be recovered losslessly", name)
		}
	}
	return fields, nil
}

func validateIncrementalBaselineTime(raw any, fieldType string) error {
	value, ok := raw.(string)
	if !ok {
		return errors.New("expected a complete and lossless indexed temporal value")
	}
	var layout string
	switch {
	case fieldType == interfaces.DataType_Date:
		layout = time.DateOnly
	case fieldType == interfaces.DataType_Datetime && baselineLocalDatetime.MatchString(value):
		layout = time.DateTime
		value = strings.Replace(value, "T", " ", 1)
	case baselineZonedDatetime.MatchString(value):
		layout = time.RFC3339Nano
	default:
		return errors.New("unsupported indexed temporal representation")
	}
	_, err := time.Parse(layout, value)
	return err
}
