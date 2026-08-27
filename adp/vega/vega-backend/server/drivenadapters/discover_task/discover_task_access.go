// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package discover_task provides DiscoverTask data access operations.
package discover_task

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	sq "github.com/Masterminds/squirrel"
	"github.com/bytedance/sonic"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	"vega-backend/interfaces"
)

const (
	DISCOVER_TASK_TABLE_NAME = "t_discover_task"
)

var (
	dtAccessOnce sync.Once
	dtAccess     interfaces.DiscoverTaskAccess
)

type discoverTaskAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

type discoverTaskScanner interface {
	Scan(dest ...any) error
}

func discoverTaskColumns() []string {
	return []string{
		"f_id",
		"f_catalog_id",
		"f_resource_id",
		"f_schedule_id",
		"f_strategy",
		"f_trigger_type",
		"f_queue_priority",
		"f_status",
		"f_progress",
		"f_message",
		"f_start_time",
		"f_finish_time",
		"f_last_progress_time",
		"f_result",
		"f_creator",
		"f_creator_type",
		"f_create_time",
	}
}

// discoverTaskSummaryColumns excludes execution messages. It retains result JSON
// only to extract the compact counters required by list consumers.
func discoverTaskSummaryColumns() []string {
	return []string{
		"f_id",
		"f_catalog_id",
		"f_resource_id",
		"f_schedule_id",
		"f_strategy",
		"f_trigger_type",
		"f_queue_priority",
		"f_status",
		"f_progress",
		"f_start_time",
		"f_finish_time",
		"f_last_progress_time",
		"f_result",
		"f_creator",
		"f_creator_type",
		"f_create_time",
	}
}

func scanDiscoverTask(scanner discoverTaskScanner) (*interfaces.DiscoverTask, error) {
	task := &interfaces.DiscoverTask{}
	var resultStr sql.NullString

	err := scanner.Scan(
		&task.ID,
		&task.CatalogID,
		&task.ResourceID,
		&task.ScheduleID,
		&task.Strategy,
		&task.TriggerType,
		&task.QueuePriority,
		&task.Status,
		&task.Progress,
		&task.Message,
		&task.StartTime,
		&task.FinishTime,
		&task.LastProgressTime,
		&resultStr,
		&task.Creator.ID,
		&task.Creator.Type,
		&task.CreateTime,
	)
	if err != nil {
		return nil, err
	}

	if resultStr.Valid && resultStr.String != "" {
		task.Result = &interfaces.DiscoverResult{}
		_ = sonic.UnmarshalString(resultStr.String, task.Result)
	}

	return task, nil
}

func scanDiscoverTaskSummary(scanner discoverTaskScanner) (*interfaces.DiscoverTaskSummary, error) {
	task := &interfaces.DiscoverTaskSummary{}
	var resultStr sql.NullString

	err := scanner.Scan(
		&task.ID,
		&task.CatalogID,
		&task.ResourceID,
		&task.ScheduleID,
		&task.Strategy,
		&task.TriggerType,
		&task.QueuePriority,
		&task.Status,
		&task.Progress,
		&task.StartTime,
		&task.FinishTime,
		&task.LastProgressTime,
		&resultStr,
		&task.Creator.ID,
		&task.Creator.Type,
		&task.CreateTime,
	)
	if err != nil {
		return nil, err
	}

	if resultStr.Valid && resultStr.String != "" {
		var result interfaces.DiscoverResult
		if err := sonic.UnmarshalString(resultStr.String, &result); err == nil {
			task.Result = &interfaces.DiscoverTaskResultSummary{
				CatalogID:      result.CatalogID,
				NewCount:       result.NewCount,
				StaleCount:     result.StaleCount,
				UnchangedCount: result.UnchangedCount,
				UpdatedCount:   result.UpdatedCount,
				RestoredCount:  result.RestoredCount,
				FailedCount:    result.FailedCount,
			}
		}
	}

	return task, nil
}

// NewDiscoverTaskAccess creates a new DiscoverTaskAccess.
func NewDiscoverTaskAccess(appSetting *common.AppSetting) interfaces.DiscoverTaskAccess {
	dtAccessOnce.Do(func() {
		dtAccess = &discoverTaskAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return dtAccess
}

// GetScheduledTaskStrategy retrieves strategy from t_discover_schedule table by ID.
func (dta *discoverTaskAccess) GetScheduledTaskStrategy(ctx context.Context, scheduledTaskID string) (string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query discover_schedule by ID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	sqlStr, vals, err := sq.Select("f_strategy").
		From("t_discover_schedule").
		Where(sq.Eq{"f_id": scheduledTaskID}).
		ToSql()
	if err != nil {
		otellog.LogError(ctx, "Failed to build select discover_schedule sql", err)
		return "", err
	}

	var strategy string
	err = dta.db.QueryRowContext(ctx, sqlStr, vals...).Scan(&strategy)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "")
		return "", nil
	}
	if err != nil {
		logger.Errorf("Scan discover_schedule failed: %v", err)
		span.SetStatus(codes.Error, "Scan failed")
		return "", err
	}

	span.SetStatus(codes.Ok, "")
	return strategy, nil
}

// Create creates a new DiscoverTask.
func (dta *discoverTaskAccess) Create(ctx context.Context, task *interfaces.DiscoverTask) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Insert into discover_task")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	sqlStr, vals, err := sq.Insert(DISCOVER_TASK_TABLE_NAME).
		Columns(discoverTaskColumns()...).
		Values(
			task.ID,
			task.CatalogID,
			task.ResourceID,
			task.ScheduleID,
			task.Strategy,
			task.TriggerType,
			task.QueuePriority,
			task.Status,
			task.Progress,
			task.Message,
			task.StartTime,
			task.FinishTime,
			task.LastProgressTime,
			"", // result initially empty
			task.Creator.ID,
			task.Creator.Type,
			task.CreateTime,
		).ToSql()
	if err != nil {
		otellog.LogError(ctx, "Failed to build insert discover_schedule sql", err)
		return err
	}

	otellog.LogInfo(ctx, fmt.Sprintf("Insert discover_task SQL: %s", sqlStr))

	_, err = dta.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Insert discover_schedule failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// GetByID retrieves a DiscoverTask by ID.
func (dta *discoverTaskAccess) GetByID(ctx context.Context, id string) (*interfaces.DiscoverTask, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query discover_task by ID")
	defer span.End()

	span.SetAttributes(attr.Key("task_id").String(id))

	sqlStr, vals, err := sq.Select(discoverTaskColumns()...).
		From(DISCOVER_TASK_TABLE_NAME).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		logger.Errorf("Failed to build select discover_task sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	row := dta.db.QueryRowContext(ctx, sqlStr, vals...)
	task, err := scanDiscoverTask(row)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "")
		return nil, nil
	}
	if err != nil {
		logger.Errorf("Scan discover_task failed: %v", err)
		span.SetStatus(codes.Error, "Scan failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return task, nil
}

// List lists DiscoverTasks with filters.
func (dta *discoverTaskAccess) List(ctx context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List discover_tasks")
	defer span.End()

	countBuilder := sq.Select("COUNT(*)").From(DISCOVER_TASK_TABLE_NAME)
	countBuilder = applyDiscoverTaskFilters(countBuilder, params)
	countSQL, countVals, err := countBuilder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build count sql failed")
		return nil, 0, err
	}
	var total int64
	if err := dta.db.QueryRowContext(ctx, countSQL, countVals...).Scan(&total); err != nil {
		logger.Errorf("Failed to count discover_tasks: %v", err)
		span.SetStatus(codes.Error, "Count failed")
		return nil, 0, err
	}

	tasks, err := dta.InternalList(ctx, params)
	if err != nil {
		return nil, 0, err
	}
	span.SetStatus(codes.Ok, "")
	return tasks, total, nil
}

// InternalList lists DiscoverTask summaries without an additional count query.
func (dta *discoverTaskAccess) InternalList(ctx context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List internal discover tasks")
	defer span.End()

	builder := sq.Select(discoverTaskSummaryColumns()...).
		From(DISCOVER_TASK_TABLE_NAME)
	builder = applyDiscoverTaskFilters(builder, params).
		OrderBy(buildOrderByClause(params.Sort, params.Direction))
	if params.Limit > 0 {
		builder = builder.Limit(uint64(params.Limit)).Offset(uint64(params.Offset))
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := dta.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	tasks := make([]*interfaces.DiscoverTaskSummary, 0)
	for rows.Next() {
		task, err := scanDiscoverTaskSummary(rows)
		if err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}

		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate discover_task rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return tasks, nil
}

func applyDiscoverTaskFilters(builder sq.SelectBuilder,
	params interfaces.DiscoverTaskQueryParams) sq.SelectBuilder {
	if params.CatalogID != "" {
		builder = builder.Where(sq.Eq{"f_catalog_id": params.CatalogID})
	}
	if params.ScheduleID != "" {
		builder = builder.Where(sq.Eq{"f_schedule_id": params.ScheduleID})
	}
	if len(params.ExcludeCatalogIDs) > 0 {
		builder = builder.Where(sq.NotEq{"f_catalog_id": params.ExcludeCatalogIDs})
	}
	if len(params.CatalogIDs) > 0 {
		builder = builder.Where(sq.Eq{"f_catalog_id": params.CatalogIDs})
	}
	if len(params.Statuses) > 0 {
		builder = builder.Where(sq.Eq{"f_status": params.Statuses})
	}
	if params.Strategy != "" {
		builder = builder.Where(sq.Eq{"f_strategy": params.Strategy})
	}
	if params.TriggerType != "" {
		builder = builder.Where(sq.Eq{"f_trigger_type": params.TriggerType})
	}
	return builder
}

func buildOrderByClause(sort, direction string) string {
	column := "f_create_time"
	switch sort {
	case interfaces.DiscoverTaskSortQueuePriority:
		return "f_queue_priority DESC, f_create_time ASC"
	case interfaces.DiscoverTaskSortStartTime:
		column = "f_start_time"
	case interfaces.DiscoverTaskSortFinishTime:
		column = "f_finish_time"
	case interfaces.DiscoverTaskSortLastProgressTime:
		column = "f_last_progress_time"
	case interfaces.DiscoverTaskSortCreateTime, "":
		column = "f_create_time"
	}

	dir := "DESC"
	if strings.EqualFold(direction, interfaces.ASC_DIRECTION) {
		dir = "ASC"
	}
	return fmt.Sprintf("%s %s", column, dir)
}

func (dta *discoverTaskAccess) MarkRunning(ctx context.Context, id string, startTime int64) (bool, error) {
	return dta.update(ctx, nil, map[string]any{
		"f_status":     interfaces.DiscoverTaskStatusRunning,
		"f_message":    "",
		"f_start_time": startTime,
	}, map[string]any{
		"f_id":     id,
		"f_status": interfaces.DiscoverTaskStatusPending,
	})
}

func (dta *discoverTaskAccess) UpdateProgress(
	ctx context.Context, id string, progress int, message string, lastProgressTime int64,
) (bool, error) {
	return dta.update(ctx, nil, map[string]any{
		"f_progress":           progress,
		"f_message":            message,
		"f_last_progress_time": lastProgressTime,
	}, map[string]any{
		"f_id":     id,
		"f_status": interfaces.DiscoverTaskStatusRunning,
	})
}

func (dta *discoverTaskAccess) MarkCompleted(
	ctx context.Context, id string, result *interfaces.DiscoverResult, finishTime int64,
) (bool, error) {
	resultJSON, err := sonic.MarshalString(result)
	if err != nil {
		return false, err
	}

	return dta.update(ctx, nil, map[string]any{
		"f_status":      interfaces.DiscoverTaskStatusCompleted,
		"f_result":      resultJSON,
		"f_progress":    100,
		"f_finish_time": finishTime,
	}, map[string]any{
		"f_id":     id,
		"f_status": interfaces.DiscoverTaskStatusRunning,
	})
}

func (dta *discoverTaskAccess) MarkCancelled(ctx context.Context, id, message string, finishTime int64) (bool, error) {
	return dta.update(ctx, nil, map[string]any{
		"f_status":      interfaces.DiscoverTaskStatusCancelled,
		"f_message":     message,
		"f_finish_time": finishTime,
	}, map[string]any{
		"f_id": id,
		"f_status": []string{
			interfaces.DiscoverTaskStatusPending,
			interfaces.DiscoverTaskStatusRunning,
		},
	})
}

func (dta *discoverTaskAccess) MarkFailed(ctx context.Context, id, message string, finishTime int64) (bool, error) {
	return dta.update(ctx, nil, map[string]any{
		"f_status":      interfaces.DiscoverTaskStatusFailed,
		"f_message":     message,
		"f_finish_time": finishTime,
	}, map[string]any{
		"f_id": id,
		"f_status": []string{
			interfaces.DiscoverTaskStatusPending,
			interfaces.DiscoverTaskStatusRunning,
		},
	})
}

// DeleteByIDs deletes DiscoverTasks by IDs.
func (dta *discoverTaskAccess) DeleteByIDs(ctx context.Context, ids []string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete discover tasks by IDs")
	defer span.End()

	if len(ids) == 0 {
		return 0, nil
	}

	sqlStr, vals, err := sq.Delete(DISCOVER_TASK_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}

	result, err := dta.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Delete discover tasks failed", err)
		return 0, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "RowsAffected failed")
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return affected, nil
}

// MarkCancelledByCatalogID marks pending tasks as cancelled when their Catalog is deleted.
func (dta *discoverTaskAccess) MarkCancelledByCatalogID(
	ctx context.Context, tx *sql.Tx, catalogID, message string, finishTime int64,
) error {
	_, err := dta.update(ctx, tx, map[string]any{
		"f_status":      interfaces.DiscoverTaskStatusCancelled,
		"f_message":     message,
		"f_finish_time": finishTime,
	}, map[string]any{
		"f_catalog_id": catalogID,
		"f_status":     interfaces.DiscoverTaskStatusPending,
	})
	return err
}

func (dta *discoverTaskAccess) update(
	ctx context.Context,
	tx *sql.Tx,
	updateColumns map[string]any,
	filterColumns map[string]any,
) (bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update discover task")
	defer span.End()

	sqlStr, vals, err := sq.Update(DISCOVER_TASK_TABLE_NAME).
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
		result, err = dta.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		otellog.LogError(ctx, "Update discover task failed", err)
		return false, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "RowsAffected failed")
		return false, err
	}

	span.SetStatus(codes.Ok, "")
	return affected > 0, nil
}
