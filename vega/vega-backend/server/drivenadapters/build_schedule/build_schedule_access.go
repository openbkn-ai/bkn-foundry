// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package build_schedule

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

const tableName = "t_build_schedule"

var accessOnce sync.Once
var access interfaces.BuildScheduleAccess

type buildScheduleAccess struct{ db *sql.DB }
type scheduleScanner interface{ Scan(...any) error }

func NewBuildScheduleAccess(settings *common.AppSetting) interfaces.BuildScheduleAccess {
	accessOnce.Do(func() { access = &buildScheduleAccess{db: libdb.NewDB(&settings.DBSetting)} })
	return access
}

func columns() []string {
	return []string{
		"f_id",
		"f_name",
		"f_resource_id",
		"f_catalog_id",
		"f_cron_expr",
		"f_execute_type",
		"f_start_time",
		"f_end_time",
		"f_enabled",
		"f_last_run",
		"f_next_run",
		"f_last_status",
		"f_last_message",
		"f_run_id",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	}
}

func scanSchedule(row scheduleScanner) (*interfaces.BuildSchedule, error) {
	s := &interfaces.BuildSchedule{}
	err := row.Scan(
		&s.ID,
		&s.Name,
		&s.ResourceID,
		&s.CatalogID,
		&s.CronExpr,
		&s.ExecuteType,
		&s.StartTime,
		&s.EndTime,
		&s.Enabled,
		&s.LastRun,
		&s.NextRun,
		&s.LastStatus,
		&s.LastMessage,
		&s.RunID,
		&s.Creator.ID,
		&s.Creator.Type,
		&s.CreateTime,
		&s.Updater.ID,
		&s.Updater.Type,
		&s.UpdateTime,
	)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (a *buildScheduleAccess) Create(ctx context.Context, s *interfaces.BuildSchedule) error {
	query := sq.Insert(tableName).Columns(columns()...).Values(
		s.ID,
		s.Name,
		s.ResourceID,
		s.CatalogID,
		s.CronExpr,
		s.ExecuteType,
		s.StartTime,
		s.EndTime,
		s.Enabled,
		s.LastRun,
		s.NextRun,
		s.LastStatus,
		s.LastMessage,
		s.RunID,
		s.Creator.ID,
		s.Creator.Type,
		s.CreateTime,
		s.Updater.ID,
		s.Updater.Type,
		s.UpdateTime,
	)
	_, err := a.execute(ctx, query)
	return err
}

func (a *buildScheduleAccess) GetByID(ctx context.Context, id string) (*interfaces.BuildSchedule, error) {
	query, args, err := sq.Select(columns()...).From(tableName).Where(sq.Eq{"f_id": id}).ToSql()
	if err != nil {
		return nil, err
	}
	s, err := scanSchedule(a.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // 不存在的记录由服务层映射为404。
	}
	return s, err
}

func applyFilters(q sq.SelectBuilder, p interfaces.BuildScheduleQueryParams) sq.SelectBuilder {
	if p.Name != "" {
		q = q.Where(sq.Like{"f_name": "%" + common.EscapeLikePattern(p.Name) + "%"})
	}
	if p.ResourceID != "" {
		q = q.Where(sq.Eq{"f_resource_id": p.ResourceID})
	}
	if p.CatalogID != "" {
		q = q.Where(sq.Eq{"f_catalog_id": p.CatalogID})
	}
	if len(p.CatalogIDs) > 0 {
		q = q.Where(sq.Eq{"f_catalog_id": p.CatalogIDs})
	}
	if len(p.ExcludeCatalogIDs) > 0 {
		q = q.Where(sq.NotEq{"f_catalog_id": p.ExcludeCatalogIDs})
	}
	if p.ExecuteType != "" {
		q = q.Where(sq.Eq{"f_execute_type": p.ExecuteType})
	}
	if p.Enabled != nil {
		q = q.Where(sq.Eq{"f_enabled": *p.Enabled})
	}
	return q
}

func (a *buildScheduleAccess) List(ctx context.Context, p interfaces.BuildScheduleQueryParams) ([]*interfaces.BuildSchedule, int64, error) {
	query, args, err := applyFilters(sq.Select("COUNT(*)").From(tableName), p).ToSql()
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err = a.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	col := "f_update_time"
	switch p.Sort {
	case interfaces.BuildScheduleSortName:
		col = "f_name"
	case interfaces.BuildScheduleSortCreateTime:
		col = "f_create_time"
	case interfaces.BuildScheduleSortNextRun:
		col = "f_next_run"
	}
	dir := "DESC"
	if p.Direction == interfaces.ASC_DIRECTION {
		dir = "ASC"
	}
	q := applyFilters(sq.Select(columns()...).From(tableName), p).OrderBy(col+" "+dir, "f_id "+dir)
	if p.Limit > 0 {
		// #nosec G115 -- HTTP 层验证分页参数非负。
		q = q.Limit(uint64(p.Limit)).Offset(uint64(p.Offset))
	}
	entries, err := a.querySchedules(ctx, q)
	return entries, total, err
}

func (a *buildScheduleAccess) querySchedules(ctx context.Context, q sq.SelectBuilder) ([]*interfaces.BuildSchedule, error) {
	query, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	entries := make([]*interfaces.BuildSchedule, 0)
	for rows.Next() {
		s, scanErr := scanSchedule(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		entries = append(entries, s)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (a *buildScheduleAccess) Update(ctx context.Context, s *interfaces.BuildSchedule, expected int64) (int64, error) {
	builder := sq.Update(tableName).
		Set("f_name", s.Name).
		Set("f_cron_expr", s.CronExpr).
		Set("f_execute_type", s.ExecuteType).
		Set("f_start_time", s.StartTime).
		Set("f_end_time", s.EndTime).
		Set("f_next_run", s.NextRun).
		Set("f_updater", s.Updater.ID).
		Set("f_updater_type", s.Updater.Type).
		Set("f_update_time", s.UpdateTime).
		Where(sq.Eq{"f_id": s.ID, "f_update_time": expected})

	return a.execute(ctx, builder)
}

func (a *buildScheduleAccess) UpdateEnabled(ctx context.Context, id string, enabled bool, next *int64, expected, now int64, updater interfaces.AccountInfo) (int64, error) {
	builder := sq.Update(tableName).
		Set("f_enabled", enabled).
		Set("f_updater", updater.ID).
		Set("f_updater_type", updater.Type).
		Set("f_update_time", now).
		Where(sq.Eq{"f_id": id, "f_update_time": expected})
	if enabled && next != nil {
		builder = builder.Set("f_next_run", *next)
	}
	return a.execute(ctx, builder)
}

func (a *buildScheduleAccess) Delete(ctx context.Context, id string) error {
	_, err := a.execute(ctx, sq.Delete(tableName).Where(sq.Eq{"f_id": id}))
	return err
}

func (a *buildScheduleAccess) DeleteByCatalogID(ctx context.Context, tx *sql.Tx, catalogID string) error {
	query, args, err := sq.Delete(tableName).Where(sq.Eq{"f_catalog_id": catalogID}).ToSql()
	if err != nil {
		return err
	}
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, args...)
	} else {
		_, err = a.db.ExecContext(ctx, query, args...)
	}
	return err
}

// DeleteByResourceIDs 只删除指定资源绑定的计划，由调用方管理关联删除事务。
func (a *buildScheduleAccess) DeleteByResourceIDs(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	query, args, err := sq.Delete(tableName).
		Where(sq.Eq{"f_resource_id": ids}).
		ToSql()
	if err != nil {
		return err
	}
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, args...)
	} else {
		_, err = a.db.ExecContext(ctx, query, args...)
	}
	return err
}

func (a *buildScheduleAccess) ListDue(ctx context.Context, now int64) ([]*interfaces.BuildSchedule, error) {
	builder := sq.Select(columns()...).
		From(tableName).
		Where(sq.Eq{"f_enabled": true}).
		Where(sq.LtOrEq{"f_next_run": now}).
		OrderBy("f_next_run ASC", "f_id ASC")

	return a.querySchedules(ctx, builder)
}

func runCondition(id string, version, next int64) sq.Eq {
	return sq.Eq{"f_id": id, "f_update_time": version, "f_next_run": next, "f_enabled": true}
}

func (a *buildScheduleAccess) InitializeNextRun(ctx context.Context, id string, version, expectedNext, next int64) (int64, error) {
	builder := sq.Update(tableName).
		Set("f_next_run", next).
		Where(runCondition(id, version, expectedNext))

	return a.execute(ctx, builder)
}

func (a *buildScheduleAccess) ClaimRun(ctx context.Context, id string, version, expectedNext, last, next int64, runID string) (int64, error) {
	builder := sq.Update(tableName).
		Set("f_last_run", last).
		Set("f_next_run", next).
		Set("f_run_id", runID).
		Set("f_last_status", interfaces.ScheduleStatusTriggering).
		Set("f_last_message", "").
		Where(runCondition(id, version, expectedNext))

	return a.execute(ctx, builder)
}

func (a *buildScheduleAccess) CompleteRun(ctx context.Context, id, runID string, result interfaces.ScheduleRunResult) (int64, error) {
	builder := sq.Update(tableName).
		Set("f_last_status", result.Status).
		Set("f_last_message", result.Message).
		Where(sq.Eq{"f_id": id, "f_run_id": runID, "f_last_status": interfaces.ScheduleStatusTriggering})

	return a.execute(ctx, builder)
}

func (a *buildScheduleAccess) DisableInvalid(ctx context.Context, id string, version, expectedNext int64, message string) (int64, error) {
	builder := sq.Update(tableName).
		Set("f_enabled", false).
		Set("f_last_status", interfaces.ScheduleStatusFailed).
		Set("f_last_message", message).
		Where(runCondition(id, version, expectedNext))

	return a.execute(ctx, builder)
}

func (a *buildScheduleAccess) execute(ctx context.Context, q sq.Sqlizer) (int64, error) {
	query, args, err := q.ToSql()
	if err != nil {
		return 0, err
	}
	result, err := a.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
