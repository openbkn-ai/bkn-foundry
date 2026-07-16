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
	"time"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-comm-go/db"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
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
		"f_applied_time",
		"f_failure_detail",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_update_time",
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
		&task.AppliedTime,
		&task.FailureDetail,
		&task.Creator.ID,
		&task.Creator.Type,
		&task.CreateTime,
		&task.UpdateTime,
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

func (a *semanticUnderstandingTaskAccess) Create(ctx context.Context, task *interfaces.SemanticUnderstandingTask) error {
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
			task.AppliedTime,
			task.FailureDetail,
			task.Creator.ID,
			task.Creator.Type,
			task.CreateTime,
			task.UpdateTime,
		).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	if _, err := a.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		otellog.LogError(ctx, "Create semantic understanding task failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (a *semanticUnderstandingTaskAccess) GetByID(ctx context.Context, id string) (*interfaces.SemanticUnderstandingTask, error) {
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

	task, err := scanSemanticUnderstandingTask(a.db.QueryRowContext(ctx, sqlStr, vals...))
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

func (a *semanticUnderstandingTaskAccess) GetByIDs(ctx context.Context, ids []string) ([]*interfaces.SemanticUnderstandingTask, error) {
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

	rows, err := a.db.QueryContext(ctx, sqlStr, vals...)
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

func (a *semanticUnderstandingTaskAccess) FindActiveByInputHash(ctx context.Context, scope string, inputHash string) (*interfaces.SemanticUnderstandingTask, error) {
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

	task, err := scanSemanticUnderstandingTask(a.db.QueryRowContext(ctx, sqlStr, vals...))
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

func (a *semanticUnderstandingTaskAccess) List(ctx context.Context, params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTask, int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List semantic understanding tasks")
	defer span.End()

	builder := sq.Select(semanticUnderstandingTaskColumns()...).From(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME)
	countBuilder := sq.Select("COUNT(*)").From(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME)

	applyFilters := func(b sq.SelectBuilder) sq.SelectBuilder {
		if params.Scope != "" {
			b = b.Where(sq.Eq{"f_scope": params.Scope})
		}
		if params.CatalogID != "" {
			b = b.Where(sq.Eq{"f_catalog_id": params.CatalogID})
		}
		if params.ResourceID != "" {
			b = b.Where(sq.Eq{"f_resource_id": params.ResourceID})
		}
		if len(params.Statuses) > 0 {
			b = b.Where(sq.Eq{"f_status": params.Statuses})
		}
		return b
	}
	builder = applyFilters(builder)
	countBuilder = applyFilters(countBuilder)

	countSQL, countVals, err := countBuilder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build count sql failed")
		return nil, 0, err
	}

	var total int64
	if err := a.db.QueryRowContext(ctx, countSQL, countVals...).Scan(&total); err != nil {
		otellog.LogError(ctx, "Count semantic understanding tasks failed", err)
		return nil, 0, err
	}

	builder = builder.OrderBy(semanticUnderstandingTaskOrderBy(params.Sort, params.Direction))
	if params.Limit > 0 {
		builder = builder.Limit(uint64(params.Limit)).Offset(uint64(params.Offset))
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, 0, err
	}

	rows, err := a.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "List semantic understanding tasks failed", err)
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	tasks := []*interfaces.SemanticUnderstandingTask{}
	for rows.Next() {
		task, err := scanSemanticUnderstandingTask(rows)
		if err != nil {
			otellog.LogError(ctx, "Scan semantic understanding task row failed", err)
			return nil, 0, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		otellog.LogError(ctx, "Rows iteration failed", err)
		return nil, 0, err
	}

	span.SetStatus(codes.Ok, "")
	return tasks, total, nil
}

func (a *semanticUnderstandingTaskAccess) Delete(ctx context.Context, id string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete semantic understanding task")
	defer span.End()

	sqlStr, vals, err := sq.Delete(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	result, err := a.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Delete semantic understanding task failed", err)
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "RowsAffected failed")
		return err
	}
	if affected == 0 {
		span.SetStatus(codes.Ok, "Semantic understanding task not found")
		return sql.ErrNoRows
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (a *semanticUnderstandingTaskAccess) DeleteByIDs(ctx context.Context, ids []string) (int64, error) {
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

	result, err := a.db.ExecContext(ctx, sqlStr, vals...)
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

func (a *semanticUnderstandingTaskAccess) MarkRunning(ctx context.Context, id string, agentTaskID string) (bool, error) {
	return a.update(ctx, id, map[string]any{
		"f_status":         interfaces.SemanticUnderstandingTaskStatusRunning,
		"f_agent_task_id":  agentTaskID,
		"f_failure_detail": "",
	}, interfaces.SemanticUnderstandingTaskStatusPending)
}

func (a *semanticUnderstandingTaskAccess) MarkSucceeded(ctx context.Context, id string, resultJSON string, confidence float64, confidenceDetailJSON string) (bool, error) {
	return a.update(ctx, id, map[string]any{
		"f_status":                 interfaces.SemanticUnderstandingTaskStatusSucceeded,
		"f_result_json":            resultJSON,
		"f_confidence":             confidence,
		"f_confidence_detail_json": confidenceDetailJSON,
		"f_failure_detail":         "",
	}, interfaces.SemanticUnderstandingTaskStatusRunning)
}

func (a *semanticUnderstandingTaskAccess) MarkFailed(ctx context.Context, id string, failureDetail string) (bool, error) {
	return a.update(ctx, id, map[string]any{
		"f_status":         interfaces.SemanticUnderstandingTaskStatusFailed,
		"f_failure_detail": failureDetail,
	}, interfaces.SemanticUnderstandingTaskStatusPending, interfaces.SemanticUnderstandingTaskStatusRunning)
}

func (a *semanticUnderstandingTaskAccess) MarkApplied(ctx context.Context, id string, applied bool, appliedTime int64, applyDetailJSON string) (bool, error) {
	updateColumns := map[string]any{
		"f_applied":      applied,
		"f_applied_time": appliedTime,
	}
	if applyDetailJSON != "" {
		updateColumns["f_apply_detail_json"] = applyDetailJSON
	}
	return a.update(ctx, id, updateColumns, interfaces.SemanticUnderstandingTaskStatusSucceeded)
}

func (a *semanticUnderstandingTaskAccess) update(ctx context.Context, id string, updateColumns map[string]any, allowedStatuses ...string) (bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Mark semantic understanding task")
	defer span.End()

	updateColumns["f_update_time"] = time.Now().UnixMilli()

	builder := sq.Update(SEMANTIC_UNDERSTANDING_TASK_TABLE_NAME).
		SetMap(updateColumns).
		Where(sq.Eq{"f_id": id})
	if len(allowedStatuses) > 0 {
		builder = builder.Where(sq.Eq{"f_status": allowedStatuses})
	}
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}

	result, err := a.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Mark semantic understanding task failed", err)
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	span.SetStatus(codes.Ok, "")
	return affected > 0, nil
}

func semanticUnderstandingTaskOrderBy(sort, direction string) string {
	column := "f_create_time"
	switch sort {
	case "update_time":
		column = "f_update_time"
	case "status":
		column = "f_status"
	case "scope":
		column = "f_scope"
	case "create_time", "":
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
