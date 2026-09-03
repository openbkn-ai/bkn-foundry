// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_schedule

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	sq "github.com/Masterminds/squirrel"
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
	DISCOVER_SCHEDULE_TABLE_NAME = "t_discover_schedule"
)

var (
	dsAccessOnce sync.Once
	dsAccess     interfaces.DiscoverScheduleAccess
)

type discoverScheduleAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

type discoverScheduleScanner interface {
	Scan(dest ...any) error
}

func discoverScheduleColumns() []string {
	return []string{
		"f_id",
		"f_name",
		"f_catalog_id",
		"f_cron_expr",
		"f_start_time",
		"f_end_time",
		"f_enabled",
		"f_strategy",
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

func scanDiscoverSchedule(scanner discoverScheduleScanner) (*interfaces.DiscoverSchedule, error) {
	schedule := &interfaces.DiscoverSchedule{}

	err := scanner.Scan(
		&schedule.ID,
		&schedule.Name,
		&schedule.CatalogID,
		&schedule.CronExpr,
		&schedule.StartTime,
		&schedule.EndTime,
		&schedule.Enabled,
		&schedule.Strategy,
		&schedule.LastRun,
		&schedule.NextRun,
		&schedule.Creator.ID,
		&schedule.Creator.Type,
		&schedule.CreateTime,
		&schedule.Updater.ID,
		&schedule.Updater.Type,
		&schedule.UpdateTime,
	)
	if err != nil {
		return nil, err
	}

	return schedule, nil
}

// NewDiscoverScheduleAccess creates a new DiscoverScheduleAccess.
func NewDiscoverScheduleAccess(appSetting *common.AppSetting) interfaces.DiscoverScheduleAccess {
	dsAccessOnce.Do(func() {
		dsAccess = &discoverScheduleAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return dsAccess
}

func (dsa *discoverScheduleAccess) UpdateEnabled(ctx context.Context, id string, enabled bool, nextRun *int64,
	expectedUpdateTime, updateTime int64, updater interfaces.AccountInfo) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "Update enabled discover_schedule")
	defer span.End()

	span.SetAttributes(
		attr.Key("schedule_id").String(id),
		attr.Key("enabled").Bool(enabled),
	)

	updateBuilder := sq.Update(DISCOVER_SCHEDULE_TABLE_NAME).
		Set("f_enabled", enabled)
	if enabled && nextRun != nil {
		span.SetAttributes(attr.Key("next_run").Int64(*nextRun))
		updateBuilder = updateBuilder.Set("f_next_run", *nextRun)
	}
	updateBuilder = updateBuilder.
		Set("f_updater", updater.ID).
		Set("f_updater_type", updater.Type).
		Set("f_update_time", updateTime).
		Where(sq.Eq{"f_id": id}).
		Where(sq.Eq{"f_update_time": expectedUpdateTime})

	sqlStr, vals, err := updateBuilder.ToSql()
	if err != nil {
		otellog.LogError(ctx, "Failed to build update enabled discover_schedule sql", err)
		return 0, err
	}

	otellog.LogInfo(ctx, fmt.Sprintf("Update enabled discover_schedule SQL: %s", sqlStr))

	result, err := dsa.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Update enabled discover_schedule failed", err)
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "Get rows affected failed")
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	logger.Infof("Updated discover schedule enabled state: id=%s, enabled=%t", id, enabled)
	return rowsAffected, nil
}

/**
 * Create a scheduled discovery task
 * @param ctx context used for tracing and carrying request-scoped data
 * @param schedule regularly discovers the pointer of the task structure, which contains all the information of the task
 * @return error execution result: nil for success and error message for failure
 */
func (dsa *discoverScheduleAccess) Create(ctx context.Context, schedule *interfaces.DiscoverSchedule) error {
	// Trace the function with OpenTelemetry by creating a client span.
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Insert into t_discover_schedule")
	defer span.End() // End the span when the function returns.
	// Attach the database URL and type information to the span.
	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	sqlStr, vals, err := sq.Insert(DISCOVER_SCHEDULE_TABLE_NAME).
		Columns(
			"f_id",
			"f_name",
			"f_catalog_id",
			"f_cron_expr",
			"f_start_time",
			"f_end_time",
			"f_enabled",
			"f_strategy",
			"f_last_run",
			"f_next_run",
			"f_creator",
			"f_creator_type",
			"f_create_time",
			"f_updater",
			"f_updater_type",
			"f_update_time",
		).
		Values(
			schedule.ID,
			schedule.Name,
			schedule.CatalogID,
			schedule.CronExpr,
			schedule.StartTime,
			schedule.EndTime,
			schedule.Enabled,
			schedule.Strategy,
			schedule.LastRun,
			schedule.NextRun,
			schedule.Creator.ID,
			schedule.Creator.Type,
			schedule.CreateTime,
			schedule.Updater.ID,
			schedule.Updater.Type,
			schedule.UpdateTime,
		).ToSql()
	if err != nil {
		otellog.LogError(ctx, "Failed to build insert discover_schedule sql", err)
		return err
	}

	otellog.LogInfo(ctx, fmt.Sprintf("Insert discover_schedule SQL: %s", sqlStr))

	// Execute insert
	_, err = dsa.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Insert discover_schedule failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	logger.Infof("Created discover schedule: id=%s, catalog_id=%s, cron=%s", schedule.ID, schedule.CatalogID, schedule.CronExpr)
	return nil
}

// GetByID retrieves a discover schedule by ID.
func (dsa *discoverScheduleAccess) GetByID(ctx context.Context, id string) (*interfaces.DiscoverSchedule, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query discover_schedule by ID")
	defer span.End()

	span.SetAttributes(attr.Key("schedule_id").String(id))

	// Build select SQL
	sqlStr, vals, err := sq.Select(discoverScheduleColumns()...).
		From(DISCOVER_SCHEDULE_TABLE_NAME).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		logger.Errorf("Failed to build select discover_schedule sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	// Execute query
	row := dsa.db.QueryRowContext(ctx, sqlStr, vals...)
	schedule, err := scanDiscoverSchedule(row)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "")
		return nil, nil
	}
	if err != nil {
		logger.Errorf("Scan discover_schedule failed: %v", err)
		span.SetStatus(codes.Error, "Scan failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return schedule, nil
}

// List lists discover schedules with filters.
func (dsa *discoverScheduleAccess) List(ctx context.Context, params interfaces.DiscoverScheduleQueryParams) ([]*interfaces.DiscoverSchedule, int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List discover_schedules")
	defer span.End()

	// Build select query
	builder := sq.Select(discoverScheduleColumns()...).
		From(DISCOVER_SCHEDULE_TABLE_NAME)

	// Apply filters
	if params.Name != "" {
		name := "%" + common.EscapeLikePattern(params.Name) + "%"
		builder = builder.Where(sq.Like{"f_name": name})
	}
	if params.CatalogID != "" {
		builder = builder.Where(sq.Eq{"f_catalog_id": params.CatalogID})
	}
	if params.Enabled != nil {
		builder = builder.Where(sq.Eq{"f_enabled": *params.Enabled})
	}

	// Get total count
	countBuilder := sq.Select("COUNT(*)").From(DISCOVER_SCHEDULE_TABLE_NAME)
	if params.Name != "" {
		name := "%" + common.EscapeLikePattern(params.Name) + "%"
		countBuilder = countBuilder.Where(sq.Like{"f_name": name})
	}
	if params.CatalogID != "" {
		countBuilder = countBuilder.Where(sq.Eq{"f_catalog_id": params.CatalogID})
	}
	if params.Enabled != nil {
		countBuilder = countBuilder.Where(sq.Eq{"f_enabled": *params.Enabled})
	}

	countSql, countVals, err := countBuilder.ToSql()
	if err != nil {
		logger.Errorf("Failed to build count discover_schedule sql: %v", err)
		span.SetStatus(codes.Error, "Build count sql failed")
		return nil, 0, err
	}

	var total int64
	err = dsa.db.QueryRowContext(ctx, countSql, countVals...).Scan(&total)
	if err != nil {
		logger.Errorf("Count discover_schedule failed: %v", err)
		span.SetStatus(codes.Error, "Count failed")
		return nil, 0, err
	}

	// Apply ordering and pagination
	if params.Sort != "" {
		builder = builder.OrderBy(fmt.Sprintf("%s %s", params.Sort, params.Direction))
	} else {
		builder = builder.OrderBy("f_update_time DESC")
	}
	// Pagination
	if params.Limit > 0 {
		builder = builder.Limit(uint64(params.Limit)).Offset(uint64(params.Offset))
	}
	// Build query
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		logger.Errorf("Failed to build select discover_schedule sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, 0, err
	}

	// Execute query
	rows, err := dsa.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		logger.Errorf("Query discover_schedule failed: %v", err)
		span.SetStatus(codes.Error, "Query failed")
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	schedules := []*interfaces.DiscoverSchedule{}
	for rows.Next() {
		schedule, err := scanDiscoverSchedule(rows)
		if err != nil {
			logger.Errorf("Scan discover_schedule failed: %v", err)
			span.SetStatus(codes.Error, "Scan failed")
			return nil, 0, err
		}
		schedules = append(schedules, schedule)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate discover_schedule rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, 0, err
	}

	span.SetStatus(codes.Ok, "")
	return schedules, total, nil
}

// Update updates a discover schedule.
func (dsa *discoverScheduleAccess) Update(ctx context.Context, schedule *interfaces.DiscoverSchedule, expectedUpdateTime int64) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update discover_schedule")
	defer span.End()

	span.SetAttributes(attr.Key("schedule_id").String(schedule.ID))

	// Build update SQL - only update non-zero value fields
	updateBuilder := sq.Update(DISCOVER_SCHEDULE_TABLE_NAME).
		Set("f_name", schedule.Name).
		Set("f_cron_expr", schedule.CronExpr).
		Set("f_start_time", schedule.StartTime).
		Set("f_end_time", schedule.EndTime).
		Set("f_strategy", schedule.Strategy).
		Set("f_next_run", schedule.NextRun).
		Set("f_updater", schedule.Updater.ID).
		Set("f_updater_type", schedule.Updater.Type).
		Set("f_update_time", schedule.UpdateTime).
		Where(sq.Eq{"f_id": schedule.ID}).
		Where(sq.Eq{"f_update_time": expectedUpdateTime})

	sqlStr, vals, err := updateBuilder.ToSql()
	if err != nil {
		otellog.LogError(ctx, "Failed to build update discover_schedule sql", err)
		return 0, err
	}

	otellog.LogInfo(ctx, fmt.Sprintf("Update discover_schedule SQL: %s", sqlStr))

	// Execute update
	result, err := dsa.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Update discover_schedule failed", err)
		return 0, err
	}

	// Check if any rows were affected
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		logger.Errorf("Failed to get rows affected: %v", err)
		span.SetStatus(codes.Error, "Get rows affected failed")
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	logger.Infof("Updated discover_schedule: id=%s", schedule.ID)
	return rowsAffected, nil
}

// Delete deletes a discover schedule by ID.
func (dsa *discoverScheduleAccess) Delete(ctx context.Context, id string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete discover_schedule")
	defer span.End()

	span.SetAttributes(attr.Key("schedule_id").String(id))

	// Build delete SQL
	sqlStr, vals, err := sq.Delete(DISCOVER_SCHEDULE_TABLE_NAME).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		otellog.LogError(ctx, "Failed to build delete discover_schedule sql", err)
		return err
	}

	otellog.LogInfo(ctx, fmt.Sprintf("Delete discover_schedule SQL: %s", sqlStr))

	// Execute delete
	result, err := dsa.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Delete discover_schedule failed", err)
		return err
	}

	// Check if any rows were affected
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		logger.Errorf("Failed to get rows affected: %v", err)
		span.SetStatus(codes.Error, "Get rows affected failed")
		return err
	}
	if rowsAffected == 0 {
		logger.Warnf("No rows affected when deleting discover_schedule: id=%s", id)
	}

	span.SetStatus(codes.Ok, "")
	logger.Infof("Deleted discover_schedule: id=%s", id)
	return nil
}

// DeleteByCatalogID deletes discover schedules belonging to a Catalog.
func (dsa *discoverScheduleAccess) DeleteByCatalogID(ctx context.Context, tx *sql.Tx, catalogID string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete discover schedules by catalog ID")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(catalogID))
	sqlStr, vals, err := sq.Delete(DISCOVER_SCHEDULE_TABLE_NAME).
		Where(sq.Eq{"f_catalog_id": catalogID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if tx != nil {
		_, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		_, err = dsa.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Delete failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// ListDue retrieves enabled schedules whose next run is at or before now.
func (dsa *discoverScheduleAccess) ListDue(ctx context.Context, now int64) ([]*interfaces.DiscoverSchedule, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List due discover_schedules")
	defer span.End()

	span.SetAttributes(attr.Key("due_before").Int64(now))

	sqlStr, vals, err := sq.Select(discoverScheduleColumns()...).
		From(DISCOVER_SCHEDULE_TABLE_NAME).
		Where(sq.Eq{"f_enabled": true}).
		Where(sq.LtOrEq{"f_next_run": now}).
		OrderBy("f_next_run ASC").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := dsa.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	schedules := make([]*interfaces.DiscoverSchedule, 0)
	for rows.Next() {
		schedule, scanErr := scanDiscoverSchedule(rows)
		if scanErr != nil {
			span.SetStatus(codes.Error, "Scan failed")
			return nil, scanErr
		}
		schedules = append(schedules, schedule)
	}
	if err := rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return schedules, nil
}

// UpdateRunMetadata atomically advances run metadata when the schedule has not changed.
func (dsa *discoverScheduleAccess) UpdateRunMetadata(ctx context.Context, id string,
	expectedUpdateTime, expectedNextRun, lastRun, nextRun int64) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update run metadata for discover_schedule")
	defer span.End()

	span.SetAttributes(
		attr.Key("schedule_id").String(id),
		attr.Key("expected_update_time").Int64(expectedUpdateTime),
		attr.Key("expected_next_run").Int64(expectedNextRun),
		attr.Key("last_run").Int64(lastRun),
		attr.Key("next_run").Int64(nextRun),
	)

	sqlStr, vals, err := sq.Update(DISCOVER_SCHEDULE_TABLE_NAME).
		Set("f_last_run", lastRun).
		Set("f_next_run", nextRun).
		Where(sq.Eq{"f_id": id}).
		Where(sq.Eq{"f_update_time": expectedUpdateTime}).
		Where(sq.Eq{"f_next_run": expectedNextRun}).
		Where(sq.Eq{"f_enabled": true}).
		ToSql()
	if err != nil {
		otellog.LogError(ctx, "Failed to build update run metadata discover_schedule sql", err)
		return 0, err
	}

	result, err := dsa.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		otellog.LogError(ctx, "Update run metadata discover_schedule failed", err)
		return 0, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		logger.Errorf("Get affected rows after updating discover schedule run metadata failed: %v", err)
		span.SetStatus(codes.Error, "Get rows affected failed")
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return rowsAffected, nil
}
