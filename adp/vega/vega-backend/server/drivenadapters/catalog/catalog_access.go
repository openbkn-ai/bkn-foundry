// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package catalog provides Catalog data access operations.
package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	sq "github.com/Masterminds/squirrel"
	"github.com/bytedance/sonic"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
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
	CATALOG_TABLE_NAME = "t_catalog"
)

var (
	cAccessOnce sync.Once
	cAccess     interfaces.CatalogAccess
)

type catalogAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

var catalogColumns = []string{
	"f_id",
	"f_name",
	"f_tags",
	"f_description",
	"f_type",
	"f_enabled",
	"f_internal",
	"f_connector_type",
	"f_connector_config",
	"f_metadata",
	"f_health_check_status",
	"f_last_check_time",
	"f_health_check_result",
	"f_creator",
	"f_creator_type",
	"f_create_time",
	"f_updater",
	"f_updater_type",
	"f_update_time",
}

var catalogSummaryColumns = []string{
	"f_id",
	"f_name",
	"f_tags",
	"f_description",
	"f_type",
	"f_enabled",
	"f_internal",
	"f_connector_type",
	"f_health_check_status",
	"f_last_check_time",
	"f_health_check_result",
	"f_creator",
	"f_creator_type",
	"f_create_time",
	"f_updater",
	"f_updater_type",
	"f_update_time",
}

type catalogRowScanner interface {
	Scan(dest ...any) error
}

func scanCatalog(scanner catalogRowScanner) (*interfaces.Catalog, error) {
	catalog := &interfaces.Catalog{}
	var tagsStr, connectorConfigStr, metadataStr string
	if err := scanner.Scan(
		&catalog.ID,
		&catalog.Name,
		&tagsStr,
		&catalog.Description,
		&catalog.Type,
		&catalog.Enabled,
		&catalog.Internal,
		&catalog.ConnectorType,
		&connectorConfigStr,
		&metadataStr,
		&catalog.HealthCheckStatus,
		&catalog.LastCheckTime,
		&catalog.HealthCheckResult,
		&catalog.Creator.ID,
		&catalog.Creator.Type,
		&catalog.CreateTime,
		&catalog.Updater.ID,
		&catalog.Updater.Type,
		&catalog.UpdateTime,
	); err != nil {
		return nil, err
	}
	catalog.Tags = libCommon.TagString2TagSlice(tagsStr)
	if connectorConfigStr != "" {
		if err := sonic.UnmarshalString(connectorConfigStr, &catalog.ConnectorCfg); err != nil {
			return nil, err
		}
	}
	if metadataStr != "" {
		if err := sonic.UnmarshalString(metadataStr, &catalog.Metadata); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func scanCatalogSummary(scanner catalogRowScanner) (*interfaces.CatalogSummary, error) {
	summary := &interfaces.CatalogSummary{}
	var tagsStr string
	if err := scanner.Scan(
		&summary.ID,
		&summary.Name,
		&tagsStr,
		&summary.Description,
		&summary.Type,
		&summary.Enabled,
		&summary.Internal,
		&summary.ConnectorType,
		&summary.HealthCheckStatus,
		&summary.LastCheckTime,
		&summary.HealthCheckResult,
		&summary.Creator.ID,
		&summary.Creator.Type,
		&summary.CreateTime,
		&summary.Updater.ID,
		&summary.Updater.Type,
		&summary.UpdateTime,
	); err != nil {
		return nil, err
	}
	summary.Tags = libCommon.TagString2TagSlice(tagsStr)
	return summary, nil
}

func applyCatalogFilters(builder sq.SelectBuilder, params interfaces.CatalogsQueryParams) sq.SelectBuilder {
	if params.Name != "" {
		builder = builder.Where(sq.Like{"f_name": "%" + common.EscapeLikePattern(params.Name) + "%"})
	}
	if params.Tag != "" {
		builder = builder.Where(sq.Like{"f_tags": "%" + common.EscapeLikePattern(params.Tag) + "%"})
	}
	if params.Type != "" {
		builder = builder.Where(sq.Eq{"f_type": params.Type})
	}
	if params.ConnectorType != "" {
		builder = builder.Where(sq.Eq{"f_connector_type": params.ConnectorType})
	}
	if params.Enabled != nil {
		builder = builder.Where(sq.Eq{"f_enabled": *params.Enabled})
	}
	if params.HealthCheckStatus != "" {
		builder = builder.Where(sq.Eq{"f_health_check_status": params.HealthCheckStatus})
	}
	return builder
}

// NewCatalogAccess creates ca new CatalogAccess.
func NewCatalogAccess(appSetting *common.AppSetting) interfaces.CatalogAccess {
	cAccessOnce.Do(func() {
		cAccess = &catalogAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return cAccess
}

// Create creates ca new Catalog.
func (ca *catalogAccess) Create(ctx context.Context, tx *sql.Tx, catalog *interfaces.Catalog) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Insert into catalog")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	// Convert tags to string format
	tagsStr := libCommon.TagSlice2TagString(catalog.Tags)

	// Serialize connector config
	connectorConfigStr, err := sonic.MarshalString(catalog.ConnectorCfg)
	if err != nil {
		otellog.LogError(ctx, "Failed to marshal connector config", err)
		return err
	}

	sqlStr, vals, err := sq.Insert(CATALOG_TABLE_NAME).
		Columns(
			"f_id",
			"f_name",
			"f_tags",
			"f_description",
			"f_type",
			"f_enabled",
			"f_internal",
			"f_connector_type",
			"f_connector_config",
			"f_metadata",
			"f_health_check_status",
			"f_last_check_time",
			"f_health_check_result",
			"f_creator",
			"f_creator_type",
			"f_create_time",
			"f_updater",
			"f_updater_type",
			"f_update_time",
		).
		Values(
			catalog.ID,
			catalog.Name,
			tagsStr,
			catalog.Description,
			catalog.Type,
			catalog.Enabled,
			catalog.Internal,
			catalog.ConnectorType,
			connectorConfigStr,
			"{}",
			catalog.HealthCheckStatus,
			catalog.LastCheckTime,
			catalog.HealthCheckResult,
			catalog.Creator.ID,
			catalog.Creator.Type,
			catalog.CreateTime,
			catalog.Updater.ID,
			catalog.Updater.Type,
			catalog.UpdateTime,
		).ToSql()
	if err != nil {
		otellog.LogError(ctx, "Failed to build insert catalog sql", err)
		return err
	}

	otellog.LogInfo(ctx, fmt.Sprintf("Insert catalog SQL: %s", sqlStr))

	if tx != nil {
		_, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		_, err = ca.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		otellog.LogError(ctx, "Insert catalog failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// GetByID retrieves ca Catalog by ID.
func (ca *catalogAccess) GetByID(ctx context.Context, id string) (*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query catalog by ID")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(id))

	sqlStr, vals, err := sq.Select(catalogColumns...).
		From(CATALOG_TABLE_NAME).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		logger.Errorf("Failed to build select catalog sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	row := ca.db.QueryRowContext(ctx, sqlStr, vals...)
	catalog, err := scanCatalog(row)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "")
		return nil, nil
	}
	if err != nil {
		logger.Errorf("Scan catalog failed: %v", err)
		span.SetStatus(codes.Error, "Scan failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return catalog, nil
}

// GetByIDs retrieves ca Catalog by IDs.
func (ca *catalogAccess) GetByIDs(ctx context.Context, ids []string) ([]*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query catalog by IDs")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_ids").StringSlice(ids))

	sqlStr, vals, err := sq.Select(catalogColumns...).
		From(CATALOG_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()
	if err != nil {
		logger.Errorf("Failed to build select catalog sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return []*interfaces.Catalog{}, err
	}

	rows, err := ca.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		logger.Errorf("Query catalog failed: %v", err)
		span.SetStatus(codes.Error, "Query failed")
		return []*interfaces.Catalog{}, err
	}
	defer func() { _ = rows.Close() }()

	catalogs := make([]*interfaces.Catalog, 0)
	for rows.Next() {
		catalog, err := scanCatalog(rows)
		if err != nil {
			logger.Errorf("Scan catalog row failed: %v", err)
			span.SetStatus(codes.Error, "Scan row failed")
			return []*interfaces.Catalog{}, err
		}

		catalogs = append(catalogs, catalog)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate catalog rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return []*interfaces.Catalog{}, err
	}

	span.SetStatus(codes.Ok, "")
	return catalogs, nil
}

// GetSummariesByIDs retrieves catalog list summaries by IDs.
func (ca *catalogAccess) GetSummariesByIDs(ctx context.Context, ids []string) ([]*interfaces.CatalogSummary, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query catalog summaries by IDs")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_ids").StringSlice(ids))
	sqlStr, vals, err := sq.Select(catalogSummaryColumns...).
		From(CATALOG_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	rows, err := ca.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	summaries := make([]*interfaces.CatalogSummary, 0)
	for rows.Next() {
		summary, err := scanCatalogSummary(rows)
		if err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return summaries, nil
}

// GetByName retrieves ca Catalog by name.
func (ca *catalogAccess) GetByName(ctx context.Context, name string) (*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query catalog by Name")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_name").String(name))

	sqlStr, vals, err := sq.Select(catalogColumns...).
		From(CATALOG_TABLE_NAME).
		Where(sq.Eq{"f_name": name}).
		ToSql()
	if err != nil {
		logger.Errorf("Failed to build select catalog sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	row := ca.db.QueryRowContext(ctx, sqlStr, vals...)
	catalog, err := scanCatalog(row)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "")
		return nil, nil
	}
	if err != nil {
		logger.Errorf("Scan catalog failed: %v", err)
		span.SetStatus(codes.Error, "Scan failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return catalog, nil
}

// ListPermissionRefs lists the minimal relations needed before list authorization.
func (ca *catalogAccess) ListPermissionRefs(ctx context.Context, params interfaces.CatalogsQueryParams) ([]interfaces.CatalogPermissionRef, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List catalog permission refs")
	defer span.End()

	builder := sq.Select("f_id").From(CATALOG_TABLE_NAME)
	builder = applyCatalogFilters(builder, params)

	builder = builder.OrderBy(catalogListOrderByClause(params.Sort, params.Direction))

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := ca.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	refs := make([]interfaces.CatalogPermissionRef, 0)
	for rows.Next() {
		var ref interfaces.CatalogPermissionRef
		if err := rows.Scan(&ref.CatalogID); err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate catalog rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return refs, nil
}

// ListInternalIDs lists the ids of all internal system directories (grouped by internal_catalog type when used for permission verification).
func (ca *catalogAccess) ListInternalIDs(ctx context.Context) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List internal catalog IDs")
	defer span.End()

	sqlStr, vals, err := sq.Select("f_id").From(CATALOG_TABLE_NAME).
		Where(sq.Eq{"f_internal": true}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := ca.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate internal catalog rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return ids, nil
}

// List lists Catalog summaries with filters.
func (ca *catalogAccess) List(ctx context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.CatalogSummary, int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List catalogs")
	defer span.End()

	builder := sq.Select(catalogSummaryColumns...).From(CATALOG_TABLE_NAME)
	builder = applyCatalogFilters(builder, params)

	countBuilder := sq.Select("COUNT(*)").From(CATALOG_TABLE_NAME)
	countBuilder = applyCatalogFilters(countBuilder, params)

	countSql, countVals, _ := countBuilder.ToSql()
	var total int64
	err := ca.db.QueryRowContext(ctx, countSql, countVals...).Scan(&total)
	if err != nil {
		logger.Errorf("Failed to count catalogs: %v", err)
		span.SetStatus(codes.Error, "Count failed")
		return nil, 0, err
	}

	// Pagination is applied in service after permission filtering.
	builder = builder.OrderBy(catalogListOrderByClause(params.Sort, params.Direction))

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, 0, err
	}

	rows, err := ca.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	catalogs := make([]*interfaces.CatalogSummary, 0)
	for rows.Next() {
		catalog, err := scanCatalogSummary(rows)
		if err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, 0, err
		}

		catalogs = append(catalogs, catalog)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate catalog rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, 0, err
	}

	span.SetStatus(codes.Ok, "")
	return catalogs, total, nil
}

// ListAuthResources lists catalog auth resources with filters.
func (ca *catalogAccess) ListAuthResources(ctx context.Context, params interfaces.AuthResourceQueryParams) ([]*interfaces.AuthResourceEntry, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListAuthResources")
	defer span.End()

	builder := sq.Select(
		"f_id",
		"f_name",
	).From(CATALOG_TABLE_NAME).
		// The internal system directory is authorized by the internal_catalog type and does not enter the list of authorized resources of the catalog type
		Where(sq.Eq{"f_internal": false})

	if params.ID != "" {
		builder = builder.Where(sq.Eq{"f_id": params.ID})
	}

	if params.Keyword != "" {
		keyword := "%" + params.Keyword + "%"
		builder = builder.Where(sq.Like{"f_name": keyword})
	}

	// Sorting
	if params.Sort != "" {
		builder = builder.OrderBy(fmt.Sprintf("%s %s", params.Sort, params.Direction))
	} else {
		builder = builder.OrderBy("f_update_time DESC")
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := ca.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	entries := make([]*interfaces.AuthResourceEntry, 0)
	for rows.Next() {
		entry := &interfaces.AuthResourceEntry{}

		err := rows.Scan(
			&entry.ID,
			&entry.Name,
		)
		if err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}

		entry.Type = interfaces.AUTH_RESOURCE_TYPE_CATALOG
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate catalog authorization resource rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return entries, nil
}

// Update updates ca Catalog.
func (ca *catalogAccess) Update(ctx context.Context, tx *sql.Tx, catalog *interfaces.Catalog, expectedUpdateTime int64) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update catalog")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(catalog.ID))

	// Convert tags to string format
	tagsStr := libCommon.TagSlice2TagString(catalog.Tags)

	connectorConfigStr, err := sonic.MarshalString(catalog.ConnectorCfg)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal connector config failed")
		return 0, err
	}
	builder := sq.Update(CATALOG_TABLE_NAME).
		Set("f_name", catalog.Name).
		Set("f_tags", tagsStr).
		Set("f_description", catalog.Description).
		Set("f_connector_config", connectorConfigStr).
		Set("f_health_check_status", catalog.HealthCheckStatus).
		Set("f_last_check_time", catalog.LastCheckTime).
		Set("f_health_check_result", catalog.HealthCheckResult).
		Set("f_updater", catalog.Updater.ID).
		Set("f_updater_type", catalog.Updater.Type).
		Set("f_update_time", catalog.UpdateTime).
		Where(sq.Eq{"f_id": catalog.ID}).
		Where(sq.Eq{"f_update_time": expectedUpdateTime})
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}

	var result sql.Result
	if tx != nil {
		result, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		result, err = ca.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		return 0, err
	}
	rowsAffected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		span.SetStatus(codes.Error, "Get affected rows failed")
		return 0, rowsErr
	}

	span.SetStatus(codes.Ok, "")
	return rowsAffected, nil
}

// DeleteByID deletes a Catalog by ID.
func (ca *catalogAccess) DeleteByID(ctx context.Context, tx *sql.Tx, id string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete catalog")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(id))

	sqlStr, vals, _ := sq.Delete(CATALOG_TABLE_NAME).
		Where(sq.Eq{"f_id": id}).
		ToSql()

	var err error
	if tx != nil {
		_, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		_, err = ca.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Delete failed")
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// catalogListOrderByClause translates API sort fields into a safe ORDER BY clause.
// Empty or unknown sort values fall back to update time descending.
func catalogListOrderByClause(sort, direction string) string {
	dir := "DESC"
	if strings.EqualFold(direction, interfaces.ASC_DIRECTION) {
		dir = "ASC"
	}

	switch sort {
	case interfaces.CatalogSortName:
		return "f_name " + dir
	case interfaces.CatalogSortCreateTime:
		return "f_create_time " + dir
	case interfaces.CatalogSortUpdateTime:
		return "f_update_time " + dir
	default:
		return "f_update_time DESC"
	}
}

// UpdateStatus updates Catalog status.
func (ca *catalogAccess) UpdateHealthCheckStatus(ctx context.Context, id string, status interfaces.CatalogHealthCheckStatus) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update catalog status")
	defer span.End()

	sqlStr, vals, _ := sq.Update(CATALOG_TABLE_NAME).
		Set("f_health_check_status", status.HealthCheckStatus).
		Set("f_last_check_time", status.LastCheckTime).
		Set("f_health_check_result", status.HealthCheckResult).
		Where(sq.Eq{"f_id": id}).
		ToSql()

	_, err := ca.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Update status failed")
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ca *catalogAccess) UpdateEnabled(ctx context.Context, id string, enabled bool,
	status interfaces.CatalogHealthCheckStatus, updateTime int64, updater interfaces.AccountInfo) error {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update catalog enabled")
	defer span.End()

	sqlStr, vals, err := sq.Update(CATALOG_TABLE_NAME).
		Set("f_enabled", enabled).
		Set("f_health_check_status", status.HealthCheckStatus).
		Set("f_last_check_time", status.LastCheckTime).
		Set("f_health_check_result", status.HealthCheckResult).
		Set("f_updater", updater.ID).
		Set("f_updater_type", updater.Type).
		Set("f_update_time", updateTime).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	_, err = ca.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Update enabled failed")
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ca *catalogAccess) UpdateMetadata(ctx context.Context, id string, metadata map[string]any) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update catalog metadata")
	defer span.End()

	metadataStr, _ := sonic.MarshalString(metadata)

	sqlStr, vals, err := sq.Update(CATALOG_TABLE_NAME).
		Set("f_metadata", metadataStr).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	_, err = ca.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
