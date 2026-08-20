// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog_health_check_schedule

import (
	"context"
	"database/sql"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	"vega-backend/interfaces"
)

const tableName = "t_catalog_health_check_schedule"

var (
	chcsAccessOnce sync.Once
	chcsAccess     interfaces.CatalogHealthCheckScheduleAccess
)

type catalogHealthCheckScheduleAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

type scanner interface {
	Scan(...any) error
}

func scanSchedule(row scanner) (*interfaces.CatalogHealthCheckSchedule, error) {
	s := &interfaces.CatalogHealthCheckSchedule{}
	err := row.Scan(
		&s.CatalogID,
		&s.Mode,
		&s.CronExpr,
		&s.LastRun,
		&s.NextRun,
		&s.Creator.ID,
		&s.Creator.Type,
		&s.CreateTime,
		&s.Updater.ID,
		&s.Updater.Type,
		&s.UpdateTime,
	)
	return s, err
}

func scheduleColumns() []string {
	return []string{
		"f_catalog_id",
		"f_mode",
		"f_cron_expr",
		"f_last_run",
		"f_next_run",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	}
}

func qualifiedScheduleColumns(alias string) []string {
	columns := scheduleColumns()
	for i, column := range columns {
		columns[i] = alias + "." + column
	}
	return columns
}

func NewCatalogHealthCheckScheduleAccess(appSetting *common.AppSetting) interfaces.CatalogHealthCheckScheduleAccess {
	chcsAccessOnce.Do(func() {
		chcsAccess = &catalogHealthCheckScheduleAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return chcsAccess
}

func (chcsa *catalogHealthCheckScheduleAccess) Create(ctx context.Context, tx *sql.Tx, s *interfaces.CatalogHealthCheckSchedule) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Insert catalog health check schedule")
	defer span.End()

	span.SetAttributes(
		attr.Key("catalog_id").String(s.CatalogID),
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	query, args, err := sq.Insert(tableName).
		Columns(scheduleColumns()...).
		Values(
			s.CatalogID,
			s.Mode,
			s.CronExpr,
			s.LastRun,
			s.NextRun,
			s.Creator.ID,
			s.Creator.Type,
			s.CreateTime,
			s.Updater.ID,
			s.Updater.Type,
			s.UpdateTime).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build insert SQL failed")
		otellog.LogError(ctx, "Build catalog health check schedule insert SQL failed", err)
		return err
	}

	if tx != nil {
		_, err = tx.ExecContext(ctx, query, args...)
	} else {
		_, err = chcsa.db.ExecContext(ctx, query, args...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Insert failed")
		otellog.LogError(ctx, "Insert catalog health check schedule failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return err
}

func (chcsa *catalogHealthCheckScheduleAccess) GetByCatalogID(ctx context.Context, catalogID string) (*interfaces.CatalogHealthCheckSchedule, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Get catalog health check schedule")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	query, args, err := sq.Select(scheduleColumns()...).
		From(tableName).
		Where(sq.Eq{"f_catalog_id": catalogID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build select SQL failed")
		otellog.LogError(ctx, "Build catalog health check schedule select SQL failed", err)
		return nil, err
	}

	row := chcsa.db.QueryRowContext(ctx, query, args...)
	schedule, err := scanSchedule(row)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		otellog.LogError(ctx, "Query catalog health check schedule failed", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return schedule, nil
}

func (chcsa *catalogHealthCheckScheduleAccess) ListDue(ctx context.Context, now int64) ([]*interfaces.CatalogHealthCheckSchedule, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List due catalog health check schedules")
	defer span.End()

	span.SetAttributes(
		attr.Key("due_before").Int64(now),
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	query, args, err := sq.Select(qualifiedScheduleColumns("s")...).
		From(tableName + " s").
		Join("t_catalog c ON c.f_id = s.f_catalog_id").
		Where(sq.Eq{"s.f_mode": []string{
			interfaces.CatalogHealthCheckScheduleModeInherit,
			interfaces.CatalogHealthCheckScheduleModeEnabled,
		}}).
		Where(sq.LtOrEq{"s.f_next_run": now}).
		Where(sq.Eq{"c.f_type": interfaces.CatalogTypePhysical, "c.f_enabled": true}).
		OrderBy("s.f_next_run ASC").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build due schedule select SQL failed")
		otellog.LogError(ctx, "Build due catalog health check schedules select SQL failed", err)
		return nil, err
	}

	rows, err := chcsa.db.QueryContext(ctx, query, args...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		otellog.LogError(ctx, "Query due catalog health check schedules failed", err)
		return nil, err
	}
	defer rows.Close()

	schedules := make([]*interfaces.CatalogHealthCheckSchedule, 0)
	for rows.Next() {
		schedule, scanErr := scanSchedule(rows)
		if scanErr != nil {
			span.SetStatus(codes.Error, "Scan failed")
			otellog.LogError(ctx, "Scan due catalog health check schedule failed", scanErr)
			return nil, scanErr
		}
		schedules = append(schedules, schedule)
	}
	if err := rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Iterate failed")
		otellog.LogError(ctx, "Iterate due catalog health check schedules failed", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return schedules, nil
}

func (chcsa *catalogHealthCheckScheduleAccess) UpdateInheritedNextRun(ctx context.Context, now, nextRun int64) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update inherited catalog health check schedule next run")
	defer span.End()

	span.SetAttributes(
		attr.Key("now").Int64(now),
		attr.Key("next_run").Int64(nextRun),
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	query, args, err := sq.Update(tableName).
		Set("f_next_run", nextRun).
		Where(sq.Eq{"f_mode": interfaces.CatalogHealthCheckScheduleModeInherit}).
		Where(sq.Gt{"f_next_run": now}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build inherited next run update SQL failed")
		otellog.LogError(ctx, "Build inherited catalog health check schedule next run update SQL failed", err)
		return err
	}

	_, err = chcsa.db.ExecContext(ctx, query, args...)
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		otellog.LogError(ctx, "Update inherited catalog health check schedule next run failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (chcsa *catalogHealthCheckScheduleAccess) Update(ctx context.Context, s *interfaces.CatalogHealthCheckSchedule, expectedUpdateTime int64) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update catalog health check schedule")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(s.CatalogID))

	builder := sq.Update(tableName).
		Set("f_mode", s.Mode).
		Set("f_cron_expr", s.CronExpr).
		Set("f_next_run", s.NextRun).
		Set("f_updater", s.Updater.ID).
		Set("f_updater_type", s.Updater.Type).
		Set("f_update_time", s.UpdateTime).
		Where(sq.Eq{"f_catalog_id": s.CatalogID}).
		Where(sq.Eq{"f_update_time": expectedUpdateTime})
	query, args, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build update SQL failed")
		otellog.LogError(ctx, "Build catalog health check schedule update SQL failed", err)
		return 0, err
	}

	result, err := chcsa.db.ExecContext(ctx, query, args...)
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		otellog.LogError(ctx, "Update catalog health check schedule failed", err)
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "Get affected rows failed")
		otellog.LogError(ctx, "Get updated catalog health check schedule rows failed", err)
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return affected, nil
}

func (chcsa *catalogHealthCheckScheduleAccess) UpdateRunMetadata(ctx context.Context,
	catalogID string, expectedUpdateTime, expectedNextRun, lastRun, nextRun int64) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update catalog health check schedule run metadata")
	defer span.End()

	span.SetAttributes(
		attr.Key("catalog_id").String(catalogID),
		attr.Key("expected_update_time").Int64(expectedUpdateTime),
		attr.Key("expected_next_run").Int64(expectedNextRun),
		attr.Key("last_run").Int64(lastRun),
		attr.Key("next_run").Int64(nextRun),
	)

	query, args, err := sq.Update(tableName).
		Set("f_last_run", lastRun).
		Set("f_next_run", nextRun).
		Where(sq.Eq{"f_catalog_id": catalogID}).
		Where(sq.Eq{"f_update_time": expectedUpdateTime}).
		Where(sq.Eq{"f_next_run": expectedNextRun}).
		Where(sq.Eq{"f_mode": []string{
			interfaces.CatalogHealthCheckScheduleModeInherit,
			interfaces.CatalogHealthCheckScheduleModeEnabled,
		}}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build run metadata update SQL failed")
		otellog.LogError(ctx, "Build catalog health check schedule run metadata update SQL failed", err)
		return 0, err
	}

	result, err := chcsa.db.ExecContext(ctx, query, args...)
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		otellog.LogError(ctx, "Update catalog health check schedule run metadata failed", err)
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "Get affected rows failed")
		otellog.LogError(ctx, "Get affected rows after updating catalog health check schedule run metadata failed", err)
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return rowsAffected, nil
}

func (chcsa *catalogHealthCheckScheduleAccess) DeleteByCatalogID(ctx context.Context, tx *sql.Tx, catalogID string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete catalog health check schedule")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	query, args, err := sq.Delete(tableName).
		Where(sq.Eq{"f_catalog_id": catalogID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build delete SQL failed")
		otellog.LogError(ctx, "Build catalog health check schedule delete SQL failed", err)
		return err
	}

	if tx != nil {
		_, err = tx.ExecContext(ctx, query, args...)
	} else {
		_, err = chcsa.db.ExecContext(ctx, query, args...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Delete failed")
		otellog.LogError(ctx, "Delete catalog health check schedules failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return err
}
