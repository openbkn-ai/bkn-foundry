// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package semantic_understanding_task provides semantic-understanding task data access.
package semantic_understanding_task

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	"vega-backend/interfaces"
)

const (
	SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME = "t_semantic_understanding_task"
)

var (
	sutAccessOnce sync.Once
	sutAccess     interfaces.SemanticUnderstandingTaskAccess
)

type semanticUnderstandingTaskAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

type semanticUnderstandingTaskScanner interface {
	Scan(dest ...any) error
}

func semanticUnderstandingTaskColumns() []string {
	return []string{
		"f_id",
		"f_scope",
		"f_catalog_id",
		"f_resource_id",
		"f_agent_task_id",
		"f_agent_id",
		"f_input",
		"f_input_hash",
		"f_status",
		"f_apply_mode",
		"f_result_json",
		"f_confidence_threshold",
		"f_confidence",
		"f_confidence_detail_json",
		"f_apply_detail_json",
		"f_applied",
		"f_failure_detail",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_start_time",
		"f_finish_time",
	}
}

// semanticUnderstandingTaskListColumns excludes task payloads and execution
// details, which are only needed by the single-task detail API.
func semanticUnderstandingTaskListColumns() []string {
	return []string{
		"f_id",
		"f_scope",
		"f_catalog_id",
		"f_resource_id",
		"f_agent_task_id",
		"f_agent_id",
		"f_status",
		"f_apply_mode",
		"f_confidence_threshold",
		"f_confidence",
		"f_applied",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_start_time",
		"f_finish_time",
	}
}

func scanSemanticUnderstandingTask(scanner semanticUnderstandingTaskScanner) (*interfaces.SemanticUnderstandingTask, error) {
	task := &interfaces.SemanticUnderstandingTask{}
	err := scanner.Scan(
		&task.ID,
		&task.Scope,
		&task.CatalogID,
		&task.ResourceID,
		&task.AgentTaskID,
		&task.AgentID,
		&task.Input,
		&task.InputHash,
		&task.Status,
		&task.ApplyMode,
		&task.ResultJSON,
		&task.ConfidenceThreshold,
		&task.Confidence,
		&task.ConfidenceDetailJSON,
		&task.ApplyDetailJSON,
		&task.Applied,
		&task.FailureDetail,
		&task.Creator.ID,
		&task.Creator.Type,
		&task.CreateTime,
		&task.StartTime,
		&task.FinishTime,
	)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func scanSemanticUnderstandingTaskListItem(scanner semanticUnderstandingTaskScanner) (*interfaces.SemanticUnderstandingTaskSummary, error) {
	task := &interfaces.SemanticUnderstandingTaskSummary{}
	err := scanner.Scan(
		&task.ID,
		&task.Scope,
		&task.CatalogID,
		&task.ResourceID,
		&task.AgentTaskID,
		&task.AgentID,
		&task.Status,
		&task.ApplyMode,
		&task.ConfidenceThreshold,
		&task.Confidence,
		&task.Applied,
		&task.Creator.ID,
		&task.Creator.Type,
		&task.CreateTime,
		&task.StartTime,
		&task.FinishTime,
	)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func NewSemanticUnderstandingTaskAccess(appSetting *common.AppSetting) interfaces.SemanticUnderstandingTaskAccess {
	sutAccessOnce.Do(func() {
		sutAccess = &semanticUnderstandingTaskAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return sutAccess
}

func (suta *semanticUnderstandingTaskAccess) Create(ctx context.Context, task *interfaces.SemanticUnderstandingTask) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Create semantic understanding task")
	defer span.End()

	sqlStr, vals, err := sq.Insert(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME).
		Columns(semanticUnderstandingTaskColumns()...).
		Values(
			task.ID,
			task.Scope,
			task.CatalogID,
			task.ResourceID,
			task.AgentTaskID,
			task.AgentID,
			task.Input,
			task.InputHash,
			task.Status,
			task.ApplyMode,
			task.ResultJSON,
			task.ConfidenceThreshold,
			task.Confidence,
			task.ConfidenceDetailJSON,
			task.ApplyDetailJSON,
			task.Applied,
			task.FailureDetail,
			task.Creator.ID,
			task.Creator.Type,
			task.CreateTime,
			task.StartTime,
			task.FinishTime,
		).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	if _, err := suta.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		otellog.LogError(ctx, "Create semantic understanding task failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (suta *semanticUnderstandingTaskAccess) GetByID(ctx context.Context, id string) (*interfaces.SemanticUnderstandingTask, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Get semantic understanding task by ID")
	defer span.End()

	sqlStr, vals, err := sq.Select(semanticUnderstandingTaskColumns()...).
		From(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	task, err := scanSemanticUnderstandingTask(suta.db.QueryRowContext(ctx, sqlStr, vals...))
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "Semantic understanding task not found")
		return nil, nil
	}
	if err != nil {
		otellog.LogError(ctx, "Get semantic understanding task failed", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return task, nil
}

func (suta *semanticUnderstandingTaskAccess) GetByIDs(ctx context.Context, ids []string) ([]*interfaces.SemanticUnderstandingTask, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Get semantic understanding tasks by IDs")
	defer span.End()

	if len(ids) == 0 {
		return []*interfaces.SemanticUnderstandingTask{}, nil
	}

	sqlStr, vals, err := sq.Select(semanticUnderstandingTaskColumns()...).
		From(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := suta.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Get semantic understanding tasks failed", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	tasks := []*interfaces.SemanticUnderstandingTask{}
	for rows.Next() {
		task, err := scanSemanticUnderstandingTask(rows)
		if err != nil {
			otellog.LogError(ctx, "Scan semantic understanding task row failed", err)
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

func (suta *semanticUnderstandingTaskAccess) FindActiveByInputHash(ctx context.Context, scope string, inputHash string) (*interfaces.SemanticUnderstandingTask, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Find active semantic understanding task by input hash")
	defer span.End()

	sqlStr, vals, err := sq.Select(semanticUnderstandingTaskColumns()...).
		From(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME).
		Where(sq.Eq{"f_scope": scope}).
		Where(sq.Eq{"f_input_hash": inputHash}).
		Where(sq.Eq{"f_status": interfaces.SemanticUnderstandingTaskActiveStatuses}).
		OrderBy("f_create_time DESC").
		Limit(1).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	task, err := scanSemanticUnderstandingTask(suta.db.QueryRowContext(ctx, sqlStr, vals...))
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "Active semantic understanding task not found")
		return nil, nil
	}
	if err != nil {
		otellog.LogError(ctx, "Find active semantic understanding task failed", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return task, nil
}

func (suta *semanticUnderstandingTaskAccess) List(ctx context.Context, params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTaskSummary, int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List semantic understanding tasks")
	defer span.End()

	countBuilder := sq.Select("COUNT(*)").From(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME)
	countBuilder = applySemanticUnderstandingTaskFilters(countBuilder, params)
	countSQL, countVals, err := countBuilder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build count sql failed")
		return nil, 0, err
	}

	var total int64
	if err := suta.db.QueryRowContext(ctx, countSQL, countVals...).Scan(&total); err != nil {
		otellog.LogError(ctx, "Count semantic understanding tasks failed", err)
		return nil, 0, err
	}

	tasks, err := suta.InternalList(ctx, params)
	if err != nil {
		return nil, 0, err
	}
	span.SetStatus(codes.Ok, "")
	return tasks, total, nil
}

func (suta *semanticUnderstandingTaskAccess) InternalList(ctx context.Context,
	params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTaskSummary, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List internal semantic understanding tasks")
	defer span.End()

	builder := sq.Select(semanticUnderstandingTaskListColumns()...).From(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME)
	builder = applySemanticUnderstandingTaskFilters(builder, params).
		OrderBy(buildOrderByClause(params.Sort, params.Direction))
	if params.Limit > 0 {
		builder = builder.Limit(uint64(params.Limit)).Offset(uint64(params.Offset))
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := suta.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "List semantic understanding tasks failed", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	tasks := []*interfaces.SemanticUnderstandingTaskSummary{}
	for rows.Next() {
		task, err := scanSemanticUnderstandingTaskListItem(rows)
		if err != nil {
			otellog.LogError(ctx, "Scan semantic understanding task row failed", err)
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

func applySemanticUnderstandingTaskFilters(builder sq.SelectBuilder,
	params interfaces.SemanticUnderstandingTaskQueryParams) sq.SelectBuilder {
	if params.Scope != "" {
		builder = builder.Where(sq.Eq{"f_scope": params.Scope})
	}
	if params.CatalogID != "" {
		builder = builder.Where(sq.Eq{"f_catalog_id": params.CatalogID})
	}
	if params.ResourceID != "" {
		builder = builder.Where(sq.Eq{"f_resource_id": params.ResourceID})
	}
	if len(params.Statuses) > 0 {
		builder = builder.Where(sq.Eq{"f_status": params.Statuses})
	}
	if params.ApplyMode != "" {
		builder = builder.Where(sq.Eq{"f_apply_mode": params.ApplyMode})
	}
	if params.Applied != nil {
		builder = builder.Where(sq.Eq{"f_applied": *params.Applied})
	}
	return builder
}

func (suta *semanticUnderstandingTaskAccess) DeleteByIDs(ctx context.Context, ids []string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete semantic understanding tasks by IDs")
	defer span.End()

	if len(ids) == 0 {
		return 0, nil
	}

	sqlStr, vals, err := sq.Delete(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}

	result, err := suta.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Delete semantic understanding tasks failed", err)
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

func (suta *semanticUnderstandingTaskAccess) MarkCancelledByCatalogID(
	ctx context.Context, tx *sql.Tx, catalogID, failureDetail string, finishTime int64,
) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Mark semantic understanding tasks cancelled by catalog ID")
	defer span.End()

	sqlStr, vals, err := sq.Update(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME).
		Set("f_status", interfaces.SemanticUnderstandingTaskStatusCancelled).
		Set("f_failure_detail", failureDetail).
		Set("f_finish_time", finishTime).
		Where(sq.Eq{"f_catalog_id": catalogID}).
		Where(sq.Eq{"f_status": interfaces.SemanticUnderstandingTaskStatusPending}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if tx != nil {
		_, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		_, err = suta.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (suta *semanticUnderstandingTaskAccess) MarkRunning(ctx context.Context, id string, startTime int64) (bool, error) {
	return suta.update(ctx, nil, map[string]any{
		"f_status":     interfaces.SemanticUnderstandingTaskStatusRunning,
		"f_start_time": startTime,
	}, map[string]any{
		"f_id":     id,
		"f_status": interfaces.SemanticUnderstandingTaskStatusPending,
	})
}

func (suta *semanticUnderstandingTaskAccess) MarkCompleted(ctx context.Context, tx *sql.Tx, id string, resultJSON string, confidence float64, confidenceDetailJSON string, finishTime int64) (bool, error) {
	return suta.update(ctx, tx, map[string]any{
		"f_status":                 interfaces.SemanticUnderstandingTaskStatusCompleted,
		"f_result_json":            resultJSON,
		"f_confidence":             confidence,
		"f_confidence_detail_json": confidenceDetailJSON,
		"f_failure_detail":         "",
		"f_finish_time":            finishTime,
	}, map[string]any{
		"f_id":     id,
		"f_status": interfaces.SemanticUnderstandingTaskStatusRunning,
	})
}

func (suta *semanticUnderstandingTaskAccess) MarkFailed(ctx context.Context, id string, failureDetail string, finishTime int64) (bool, error) {
	return suta.update(ctx, nil, map[string]any{
		"f_status":         interfaces.SemanticUnderstandingTaskStatusFailed,
		"f_failure_detail": failureDetail,
		"f_finish_time":    finishTime,
	}, map[string]any{
		"f_id": id,
		"f_status": []string{
			interfaces.SemanticUnderstandingTaskStatusPending,
			interfaces.SemanticUnderstandingTaskStatusRunning,
		},
	})
}

func (suta *semanticUnderstandingTaskAccess) MarkCancelled(ctx context.Context, id string, failureDetail string, finishTime int64) (bool, error) {
	return suta.update(ctx, nil, map[string]any{
		"f_status":         interfaces.SemanticUnderstandingTaskStatusCancelled,
		"f_failure_detail": failureDetail,
		"f_finish_time":    finishTime,
	}, map[string]any{
		"f_id": id,
		"f_status": []string{
			interfaces.SemanticUnderstandingTaskStatusPending,
			interfaces.SemanticUnderstandingTaskStatusRunning,
		},
	})
}

func (suta *semanticUnderstandingTaskAccess) SetAgentTaskID(ctx context.Context, id string, agentTaskID string) (bool, error) {
	return suta.update(ctx, nil, map[string]any{
		"f_agent_task_id": agentTaskID,
	}, map[string]any{
		"f_id":     id,
		"f_status": interfaces.SemanticUnderstandingTaskStatusRunning,
	})
}

func (suta *semanticUnderstandingTaskAccess) SetApplied(ctx context.Context, tx *sql.Tx, id string, applied bool, applyDetailJSON string) (bool, error) {
	updateColumns := map[string]any{
		"f_applied": applied,
	}
	if applyDetailJSON != "" {
		updateColumns["f_apply_detail_json"] = applyDetailJSON
	}
	return suta.update(ctx, tx, updateColumns, map[string]any{
		"f_id":     id,
		"f_status": interfaces.SemanticUnderstandingTaskStatusCompleted,
	})
}

func (suta *semanticUnderstandingTaskAccess) update(ctx context.Context, tx *sql.Tx,
	updateColumns map[string]any, filterColumns map[string]any) (bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update semantic understanding task")
	defer span.End()

	builder := sq.Update(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME).
		SetMap(updateColumns).
		Where(sq.Eq(filterColumns))
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}

	var result sql.Result
	if tx != nil {
		result, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		result, err = suta.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		otellog.LogError(ctx, "Update semantic understanding task failed", err)
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	span.SetStatus(codes.Ok, "")
	return affected > 0, nil
}

func buildOrderByClause(sort, direction string) string {
	column := "f_create_time"
	switch sort {
	case interfaces.SemanticUnderstandingTaskSortStartTime:
		column = "f_start_time"
	case interfaces.SemanticUnderstandingTaskSortFinishTime:
		column = "f_finish_time"
	case interfaces.SemanticUnderstandingTaskSortCreateTime:
		column = "f_create_time"
	default:
		column = "f_create_time"
	}

	dir := "DESC"
	if strings.EqualFold(direction, interfaces.ASC_DIRECTION) {
		dir = "ASC"
	}
	return fmt.Sprintf("%s %s", column, dir)
}
