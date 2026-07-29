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
	libdb "github.com/openbkn-ai/bkn-comm-go/db"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
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

type scheduleAccess struct {
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

func NewCatalogHealthCheckScheduleAccess(appSetting *common.AppSetting) interfaces.CatalogHealthCheckScheduleAccess {
	chcsAccessOnce.Do(func() {
		chcsAccess = &scheduleAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return chcsAccess
}

func (a *scheduleAccess) Create(ctx context.Context, s *interfaces.CatalogHealthCheckSchedule) error {
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

	_, err = a.db.ExecContext(ctx, query, args...)
	if err != nil {
		span.SetStatus(codes.Error, "Insert failed")
		otellog.LogError(ctx, "Insert catalog health check schedule failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return err
}

func (a *scheduleAccess) GetByCatalogID(ctx context.Context, catalogID string) (*interfaces.CatalogHealthCheckSchedule, error) {
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

	row := a.db.QueryRowContext(ctx, query, args...)
	schedule, err := scanSchedule(row)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		otellog.LogError(ctx, "Query catalog health check schedule failed", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return schedule, nil
}

func (a *scheduleAccess) Update(ctx context.Context, s *interfaces.CatalogHealthCheckSchedule) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update catalog health check schedule")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(s.CatalogID))

	query, args, err := sq.Update(tableName).
		Set("f_mode", s.Mode).
		Set("f_cron_expr", s.CronExpr).
		Set("f_next_run", s.NextRun).
		Set("f_updater", s.Updater.ID).
		Set("f_updater_type", s.Updater.Type).
		Set("f_update_time", s.UpdateTime).
		Where(sq.Eq{"f_catalog_id": s.CatalogID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build update SQL failed")
		otellog.LogError(ctx, "Build catalog health check schedule update SQL failed", err)
		return err
	}

	_, err = a.db.ExecContext(ctx, query, args...)
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		otellog.LogError(ctx, "Update catalog health check schedule failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return err
}

func (a *scheduleAccess) DeleteByCatalogIDs(ctx context.Context, catalogIDs []string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete catalog health check schedules")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_ids").StringSlice(catalogIDs))

	if len(catalogIDs) == 0 {
		return nil
	}

	query, args, err := sq.Delete(tableName).
		Where(sq.Eq{"f_catalog_id": catalogIDs}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build delete SQL failed")
		otellog.LogError(ctx, "Build catalog health check schedule delete SQL failed", err)
		return err
	}

	_, err = a.db.ExecContext(ctx, query, args...)
	if err != nil {
		span.SetStatus(codes.Error, "Delete failed")
		otellog.LogError(ctx, "Delete catalog health check schedules failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return err
}
