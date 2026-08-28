// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package build_task provides BuildTask data access layer.
package build_task

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	sq "github.com/Masterminds/squirrel"
	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	"vega-backend/interfaces"
)

var (
	btAccessOnce sync.Once
	btAccess     interfaces.BuildTaskAccess
)

const (
	BUILD_TASK_TABLE_NAME = "t_build_task"
)

func buildTaskColumns() []string {
	return []string{
		"f_id",
		"f_resource_id",
		"f_catalog_id",
		"f_mode",
		"f_execute_type",
		"f_index_config",

		"f_status",
		"f_total_count",
		"f_synced_count",
		"f_synced_mark",
		"f_error_msg",
		"f_failure_detail",

		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_start_time",
		"f_finish_time",
		"f_last_progress_time",
	}
}

func buildTaskSummaryColumns() []string {
	return []string{
		"f_id",
		"f_resource_id",
		"f_catalog_id",
		"f_mode",
		"f_execute_type",
		"f_status",
		"f_total_count",
		"f_synced_count",
		"f_synced_mark",
		"f_error_msg",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_start_time",
		"f_finish_time",
		"f_last_progress_time",
	}
}

type buildTaskScanner interface {
	Scan(dest ...any) error
}

func scanBuildTask(scanner buildTaskScanner) (*interfaces.BuildTask, error) {
	buildTask := &interfaces.BuildTask{}
	var creatorID, creatorType string
	var indexConfigJSON string

	err := scanner.Scan(
		&buildTask.ID,
		&buildTask.ResourceID,
		&buildTask.CatalogID,
		&buildTask.Mode,
		&buildTask.ExecuteType,
		&indexConfigJSON,
		&buildTask.Status,
		&buildTask.TotalCount,
		&buildTask.SyncedCount,
		&buildTask.SyncedMark,
		&buildTask.ErrorMsg,
		&buildTask.FailureDetail,
		&creatorID,
		&creatorType,
		&buildTask.CreateTime,
		&buildTask.StartTime,
		&buildTask.FinishTime,
		&buildTask.LastProgressTime,
	)
	if err != nil {
		return nil, err
	}
	if indexConfigJSON != "" {
		if err := sonic.UnmarshalString(indexConfigJSON, &buildTask.IndexConfig); err != nil {
			return nil, err
		}
	}

	buildTask.Creator = interfaces.AccountInfo{ID: creatorID, Type: creatorType}
	return buildTask, nil
}

func scanBuildTaskSummary(scanner buildTaskScanner) (*interfaces.BuildTaskSummary, error) {
	task := &interfaces.BuildTaskSummary{}
	var creatorID, creatorType string
	err := scanner.Scan(
		&task.ID,
		&task.ResourceID,
		&task.CatalogID,
		&task.Mode,
		&task.ExecuteType,
		&task.Status,
		&task.TotalCount,
		&task.SyncedCount,
		&task.SyncedMark,
		&task.ErrorMsg,
		&creatorID,
		&creatorType,
		&task.CreateTime,
		&task.StartTime,
		&task.FinishTime,
		&task.LastProgressTime,
	)
	if err != nil {
		return nil, err
	}
	task.Creator = interfaces.AccountInfo{ID: creatorID, Type: creatorType}
	return task, nil
}

type buildTaskAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

// NewBuildTaskAccess creates a new BuildTaskAccess.
func NewBuildTaskAccess(appSetting *common.AppSetting) interfaces.BuildTaskAccess {
	btAccessOnce.Do(func() {
		btAccess = &buildTaskAccess{
			appSetting: appSetting,
			db:         db.NewDB(&appSetting.DBSetting),
		}
	})
	return btAccess
}

// Create creates a new build task.
func (bta *buildTaskAccess) Create(ctx context.Context, buildTask *interfaces.BuildTask) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Create build task")
	defer span.End()

	indexConfigJSON, err := sonic.MarshalString(buildTask.IndexConfig)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal index config failed")
		return err
	}

	sqlStr, vals, err := sq.Insert(BUILD_TASK_TABLE_NAME).
		Columns(buildTaskColumns()...).
		Values(
			buildTask.ID,
			buildTask.ResourceID,
			buildTask.CatalogID,
			buildTask.Mode,
			buildTask.ExecuteType,
			indexConfigJSON,
			buildTask.Status,
			buildTask.TotalCount,
			buildTask.SyncedCount,
			buildTask.SyncedMark,
			buildTask.ErrorMsg,
			buildTask.FailureDetail,
			buildTask.Creator.ID,
			buildTask.Creator.Type,
			buildTask.CreateTime,
			buildTask.StartTime,
			buildTask.FinishTime,
			buildTask.LastProgressTime,
		).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	_, err = bta.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Create build task failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// GetByID retrieves a build task by ID.
func (bta *buildTaskAccess) GetByID(ctx context.Context, id string) (*interfaces.BuildTask, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Get build task by ID")
	defer span.End()

	sqlStr, vals, err := sq.Select(buildTaskColumns()...).
		From(BUILD_TASK_TABLE_NAME).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	row := bta.db.QueryRowContext(ctx, sqlStr, vals...)
	buildTask, err := scanBuildTask(row)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "Build task not found")
		return nil, nil
	}

	if err != nil {
		otellog.LogError(ctx, "Get build task by ID failed", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return buildTask, nil
}

// GetByIDs retrieves build tasks keyed by ID.
func (bta *buildTaskAccess) GetByIDs(ctx context.Context, ids []string) (map[string]*interfaces.BuildTask, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Get build tasks by IDs")
	defer span.End()

	buildTasks := make(map[string]*interfaces.BuildTask, len(ids))
	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return buildTasks, nil
	}

	sqlStr, vals, err := sq.Select(buildTaskColumns()...).
		From(BUILD_TASK_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := bta.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Get build tasks by IDs failed", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		buildTask, err := scanBuildTask(rows)
		if err != nil {
			otellog.LogError(ctx, "Scan build task row failed", err)
			return nil, err
		}
		buildTasks[buildTask.ID] = buildTask
	}
	if err := rows.Err(); err != nil {
		otellog.LogError(ctx, "Rows iteration failed", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return buildTasks, nil
}

// GetByCatalogID retrieves build tasks by catalog ID.
func (bta *buildTaskAccess) GetByCatalogID(ctx context.Context, catalogID string) ([]*interfaces.BuildTask, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Get build tasks by catalog ID")
	defer span.End()

	sqlStr, vals, err := sq.Select(buildTaskColumns()...).
		From(BUILD_TASK_TABLE_NAME).
		Where(sq.Eq{"f_catalog_id": catalogID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := bta.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Get build tasks by catalog ID failed", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	buildTasks := []*interfaces.BuildTask{}
	for rows.Next() {
		buildTask, err := scanBuildTask(rows)
		if err != nil {
			otellog.LogError(ctx, "Scan build task row failed", err)
			return nil, err
		}

		buildTasks = append(buildTasks, buildTask)
	}

	if err = rows.Err(); err != nil {
		otellog.LogError(ctx, "Rows iteration failed", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return buildTasks, nil
}

func (bta *buildTaskAccess) SetProgress(ctx context.Context, tx *sql.Tx, id string,
	progress interfaces.BuildTaskProgress, lastProgressTime int64) (bool, error) {
	updateColumns := map[string]any{"f_last_progress_time": lastProgressTime}
	if progress.TotalCount != nil {
		updateColumns["f_total_count"] = *progress.TotalCount
	}
	if progress.SyncedCount != nil {
		updateColumns["f_synced_count"] = *progress.SyncedCount
	}
	if progress.SyncedMark != nil {
		updateColumns["f_synced_mark"] = *progress.SyncedMark
	}
	if progress.FailureDetail != nil {
		updateColumns["f_failure_detail"] = *progress.FailureDetail
	}
	return bta.update(ctx, tx, updateColumns, map[string]any{
		"f_id": id,
		"f_status": []string{
			interfaces.BuildTaskStatusRunning,
			interfaces.BuildTaskStatusStopping,
		},
	})
}

func (bta *buildTaskAccess) MarkPending(ctx context.Context, tx *sql.Tx, id string, reset bool) (bool, error) {
	updateColumns := map[string]any{
		"f_status":             interfaces.BuildTaskStatusPending,
		"f_start_time":         int64(0),
		"f_finish_time":        int64(0),
		"f_last_progress_time": int64(0),
	}
	if reset {
		updateColumns["f_total_count"] = int64(0)
		updateColumns["f_synced_count"] = int64(0)
		updateColumns["f_synced_mark"] = ""
		updateColumns["f_error_msg"] = ""
		updateColumns["f_failure_detail"] = ""
	}
	return bta.update(ctx, tx, updateColumns, map[string]any{
		"f_id": id,
		"f_status": []string{
			interfaces.BuildTaskStatusStopped,
			interfaces.BuildTaskStatusFailed,
		},
	})
}

func (bta *buildTaskAccess) MarkRunning(ctx context.Context, tx *sql.Tx, id string, startTime int64) (bool, error) {
	return bta.update(ctx, tx, map[string]any{
		"f_status":     interfaces.BuildTaskStatusRunning,
		"f_error_msg":  "",
		"f_start_time": startTime,
	}, map[string]any{"f_id": id, "f_status": interfaces.BuildTaskStatusPending})
}

func (bta *buildTaskAccess) MarkStopping(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	return bta.update(ctx, tx, map[string]any{
		"f_status": interfaces.BuildTaskStatusStopping,
	}, map[string]any{"f_id": id, "f_status": interfaces.BuildTaskStatusRunning})
}

func (bta *buildTaskAccess) MarkStopped(ctx context.Context, tx *sql.Tx, id string, finishTime int64) (bool, error) {
	return bta.update(ctx, tx, map[string]any{
		"f_status":      interfaces.BuildTaskStatusStopped,
		"f_finish_time": finishTime,
	}, map[string]any{
		"f_id": id,
		"f_status": []string{
			interfaces.BuildTaskStatusPending,
			interfaces.BuildTaskStatusStopping,
		},
	})
}

func (bta *buildTaskAccess) MarkCompleted(ctx context.Context, tx *sql.Tx, id string, finishTime int64) (bool, error) {
	return bta.update(ctx, tx, map[string]any{
		"f_status":      interfaces.BuildTaskStatusCompleted,
		"f_finish_time": finishTime,
	}, map[string]any{"f_id": id, "f_status": interfaces.BuildTaskStatusRunning})
}

func (bta *buildTaskAccess) MarkFailed(ctx context.Context, tx *sql.Tx, id string, detail string, finishTime int64) (bool, error) {
	return bta.update(ctx, tx, map[string]any{
		"f_status":      interfaces.BuildTaskStatusFailed,
		"f_error_msg":   detail,
		"f_finish_time": finishTime,
	}, map[string]any{
		"f_id": id,
		"f_status": []string{
			interfaces.BuildTaskStatusPending,
			interfaces.BuildTaskStatusRunning,
			interfaces.BuildTaskStatusStopping,
		},
	})
}

func (bta *buildTaskAccess) MarkCancelled(ctx context.Context, tx *sql.Tx, id string, detail string, finishTime int64) (bool, error) {
	return bta.update(ctx, tx, map[string]any{
		"f_status":      interfaces.BuildTaskStatusCancelled,
		"f_error_msg":   detail,
		"f_finish_time": finishTime,
	}, map[string]any{
		"f_id": id,
		"f_status": []string{
			interfaces.BuildTaskStatusPending,
			interfaces.BuildTaskStatusRunning,
			interfaces.BuildTaskStatusStopping,
		},
	})
}

func (bta *buildTaskAccess) MarkCancelledByCatalogID(ctx context.Context,
	tx *sql.Tx, catalogID string, message string, finishTime int64) error {
	_, err := bta.update(ctx, tx, map[string]any{
		"f_status":      interfaces.BuildTaskStatusCancelled,
		"f_error_msg":   message,
		"f_finish_time": finishTime,
	}, map[string]any{
		"f_catalog_id": catalogID,
		"f_status":     interfaces.BuildTaskStatusPending,
	})
	return err
}

// GetStatusByID retrieves the status of a build task by ID.
func (bta *buildTaskAccess) GetStatusByID(ctx context.Context, id string) (string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Get build task status")
	defer span.End()

	var status string
	sqlStr, vals, err := sq.Select("f_status").
		From(BUILD_TASK_TABLE_NAME).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return "", err
	}

	err = bta.db.QueryRowContext(ctx, sqlStr, vals...).Scan(&status)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "Build task not found")
		return "", fmt.Errorf("build task not found")
	}

	if err != nil {
		otellog.LogError(ctx, "Get build task status failed", err)
		return "", err
	}

	span.SetStatus(codes.Ok, "")
	return status, nil
}

// InternalList retrieves build task summaries without an additional count query.
func (bta *buildTaskAccess) InternalList(ctx context.Context,
	params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List internal build task summaries")
	defer span.End()

	builder := sq.Select(buildTaskSummaryColumns()...).From(BUILD_TASK_TABLE_NAME)
	builder = applyBuildTaskFilters(builder, params).
		OrderBy(buildOrderByClause(params.Sort, params.Direction))
	if params.Limit > 0 {
		builder = builder.Limit(uint64(params.Limit)).Offset(uint64(params.Offset))
	}

	query, args, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	rows, err := bta.db.QueryContext(ctx, query, args...)
	if err != nil {
		otellog.LogError(ctx, "List build task summaries failed", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	tasks := make([]*interfaces.BuildTaskSummary, 0)
	for rows.Next() {
		task, err := scanBuildTaskSummary(rows)
		if err != nil {
			otellog.LogError(ctx, "Scan build task summary row failed", err)
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		otellog.LogError(ctx, "Rows iteration failed", err)
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return tasks, nil
}

// List retrieves build task summaries with optional filters and pagination.
func (bta *buildTaskAccess) List(ctx context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List build task summaries")
	defer span.End()

	countBuilder := sq.Select("COUNT(*)").From(BUILD_TASK_TABLE_NAME)
	countBuilder = applyBuildTaskFilters(countBuilder, params)
	countSQL, countVals, err := countBuilder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build count sql failed")
		return nil, 0, err
	}
	var total int64
	if err := bta.db.QueryRowContext(ctx, countSQL, countVals...).Scan(&total); err != nil {
		otellog.LogError(ctx, "Count build tasks failed", err)
		return nil, 0, err
	}
	tasks, err := bta.InternalList(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List build task summaries failed")
		return nil, 0, err
	}
	span.SetStatus(codes.Ok, "")
	return tasks, total, nil
}

func applyBuildTaskFilters(builder sq.SelectBuilder,
	params interfaces.BuildTasksQueryParams) sq.SelectBuilder {
	if len(params.Statuses) > 0 {
		builder = builder.Where(sq.Eq{"f_status": params.Statuses})
	}
	if params.Mode != "" {
		builder = builder.Where(sq.Eq{"f_mode": params.Mode})
	}
	if params.ExecuteType != "" {
		builder = builder.Where(sq.Eq{"f_execute_type": params.ExecuteType})
	}
	if params.ResourceID != "" {
		builder = builder.Where(sq.Eq{"f_resource_id": params.ResourceID})
	}
	if params.CatalogID != "" {
		builder = builder.Where(sq.Eq{"f_catalog_id": params.CatalogID})
	}
	if len(params.ExcludeCatalogIDs) > 0 {
		builder = builder.Where(sq.NotEq{"f_catalog_id": params.ExcludeCatalogIDs})
	}
	if len(params.CatalogIDs) > 0 {
		builder = builder.Where(sq.Eq{"f_catalog_id": params.CatalogIDs})
	}
	return builder
}

// buildOrderByClause translates sort/direction into an ORDER BY clause.
// Empty or unknown sort values fall back to creation time descending.
func buildOrderByClause(sort, direction string) string {
	dir := "DESC"
	if strings.EqualFold(direction, interfaces.ASC_DIRECTION) {
		dir = "ASC"
	}
	switch sort {
	case interfaces.BuildTaskSortCreateTime:
		return "f_create_time " + dir
	case interfaces.BuildTaskSortStartTime:
		return "f_start_time " + dir
	case interfaces.BuildTaskSortFinishTime:
		return "f_finish_time " + dir
	case interfaces.BuildTaskSortLastProgressTime:
		return "f_last_progress_time " + dir
	default:
		return "f_create_time DESC"
	}
}

// DeleteByIDs deletes build tasks by IDs.
func (bta *buildTaskAccess) DeleteByIDs(ctx context.Context, ids []string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete build tasks by IDs")
	defer span.End()

	if len(ids) == 0 {
		return 0, nil
	}

	sqlStr, vals, err := sq.Delete(BUILD_TASK_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}

	result, err := bta.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Delete build tasks failed", err)
		return 0, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		otellog.LogError(ctx, "Get rows affected failed", err)
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return affected, nil
}

func (bta *buildTaskAccess) update(ctx context.Context, tx *sql.Tx,
	updateColumns map[string]any, filterColumns map[string]any) (bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update build task")
	defer span.End()

	sqlStr, vals, err := sq.Update(BUILD_TASK_TABLE_NAME).
		SetMap(updateColumns).
		Where(sq.Eq(filterColumns)).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}

	var result sql.Result
	if tx != nil {
		result, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		result, err = bta.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		otellog.LogError(ctx, "Update build task failed", err)
		return false, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		otellog.LogError(ctx, "Get rows affected failed", err)
		return false, err
	}

	span.SetStatus(codes.Ok, "")
	return affected > 0, nil
}
