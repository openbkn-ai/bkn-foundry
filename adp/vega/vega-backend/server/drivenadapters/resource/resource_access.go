// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package resource provides Resource data access operations.
package resource

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
	RESOURCE_TABLE_NAME = "t_resource"
)

var (
	rAccessOnce sync.Once
	rAccess     interfaces.ResourceAccess
)

type resourceAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

var resourceDetailColumns = []string{
	"f_id",
	"f_catalog_id",
	"f_name",
	"f_tags",
	"f_description",
	"f_category",
	"f_status",
	"f_status_message",
	"f_last_discover_status",
	"f_schema",
	"f_source_identifier",
	"f_source_metadata",
	"f_schema_definition",
	"f_index_config",
	"f_local_status",
	"f_local_index_name",
	"f_sync_mark",
	"f_logic_type",
	"f_logic_definition",
	"f_creator",
	"f_creator_type",
	"f_create_time",
	"f_updater",
	"f_updater_type",
	"f_update_time",
}

var resourceSummaryColumns = []string{
	"f_id",
	"f_catalog_id",
	"f_name",
	"f_tags",
	"f_description",
	"f_category",
	"f_status",
	"f_status_message",
	"f_last_discover_status",
	"f_schema",
	"f_source_identifier",
	"f_source_metadata",
	"f_schema_definition",
	"f_local_status",
	"f_local_index_name",
	"f_sync_mark",
	"f_logic_type",
	"f_creator",
	"f_creator_type",
	"f_create_time",
	"f_updater",
	"f_updater_type",
	"f_update_time",
}

type resourceRowScanner interface {
	Scan(dest ...any) error
}

func scanResourceDetail(scanner resourceRowScanner) (*interfaces.Resource, error) {
	resource := &interfaces.Resource{}
	var tagsStr string
	var sourceMetadata, schemaDefinition, indexConfig, logicDefinition sql.NullString

	if err := scanner.Scan(
		&resource.ID,
		&resource.CatalogID,
		&resource.Name,
		&tagsStr,
		&resource.Description,
		&resource.Category,
		&resource.Status,
		&resource.StatusMessage,
		&resource.LastDiscoverStatus,
		&resource.Schema,
		&resource.SourceIdentifier,
		&sourceMetadata,
		&schemaDefinition,
		&indexConfig,
		&resource.LocalIndexStatus,
		&resource.LocalIndexName,
		&resource.SyncMark,
		&resource.LogicType,
		&logicDefinition,
		&resource.Creator.ID,
		&resource.Creator.Type,
		&resource.CreateTime,
		&resource.Updater.ID,
		&resource.Updater.Type,
		&resource.UpdateTime,
	); err != nil {
		return nil, err
	}

	resource.Tags = libCommon.TagString2TagSlice(tagsStr)
	if sourceMetadata.Valid && sourceMetadata.String != "" {
		_ = sonic.Unmarshal([]byte(sourceMetadata.String), &resource.SourceMetadata)
	}
	if schemaDefinition.Valid && schemaDefinition.String != "" {
		_ = sonic.Unmarshal([]byte(schemaDefinition.String), &resource.SchemaDefinition)
	}
	if indexConfig.Valid && indexConfig.String != "" {
		_ = sonic.Unmarshal([]byte(indexConfig.String), &resource.IndexConfig)
	}
	if logicDefinition.Valid && logicDefinition.String != "" {
		_ = sonic.Unmarshal([]byte(logicDefinition.String), &resource.LogicDefinition)
	}
	return resource, nil
}

func scanResourceSummary(scanner resourceRowScanner) (*interfaces.ResourceSummary, error) {
	summary := &interfaces.ResourceSummary{}
	var tagsStr string
	var sourceMetadata, schemaDefinition sql.NullString
	if err := scanner.Scan(
		&summary.ID,
		&summary.CatalogID,
		&summary.Name,
		&tagsStr,
		&summary.Description,
		&summary.Category,
		&summary.Status,
		&summary.StatusMessage,
		&summary.LastDiscoverStatus,
		&summary.Schema,
		&summary.SourceIdentifier,
		&sourceMetadata,
		&schemaDefinition,
		&summary.LocalIndexStatus,
		&summary.LocalIndexName,
		&summary.SyncMark,
		&summary.LogicType,
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
	if schemaDefinition.Valid && schemaDefinition.String != "" {
		if node, err := sonic.GetFromString(schemaDefinition.String); err == nil && node.Load() == nil {
			if n, err := node.Len(); err == nil {
				summary.ColumnCount = &n
			}
		}
	}
	if sourceMetadata.Valid && sourceMetadata.String != "" {
		if node, err := sonic.GetFromString(sourceMetadata.String, "properties", "row_count"); err == nil {
			if v, err := node.Int64(); err == nil {
				summary.RowCount = &v
			}
		}
	}
	return summary, nil
}

func applyResourceFilters(builder sq.SelectBuilder, params interfaces.ResourcesQueryParams) sq.SelectBuilder {
	if params.Name != "" {
		builder = builder.Where(sq.Like{"f_name": "%" + common.EscapeLikePattern(params.Name) + "%"})
	}
	if params.CatalogID != "" {
		builder = builder.Where(sq.Eq{"f_catalog_id": params.CatalogID})
	}
	if params.Category != "" {
		builder = builder.Where(sq.Eq{"f_category": params.Category})
	}
	if params.Status != "" {
		builder = builder.Where(sq.Eq{"f_status": params.Status})
	}
	if params.Schema != "" {
		builder = builder.Where(sq.Eq{"f_schema": params.Schema})
	}
	return builder
}

// NewResourceAccess creates ra new ResourceAccess.
func NewResourceAccess(appSetting *common.AppSetting) interfaces.ResourceAccess {
	rAccessOnce.Do(func() {
		rAccess = &resourceAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return rAccess
}

// Create creates ra new Resource.
func (ra *resourceAccess) Create(ctx context.Context, tx *sql.Tx, resource *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Insert into resource")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	// Convert tags to string format
	tagsStr := libCommon.TagSlice2TagString(resource.Tags)

	// Serialize Source Data, Scheme Definition, and LogicDefinition
	sourceMetadataBytes, _ := sonic.Marshal(resource.SourceMetadata)
	if resource.SourceMetadata == nil {
		sourceMetadataBytes = []byte("{}")
	}
	schemaDefinitionBytes, _ := sonic.Marshal(resource.SchemaDefinition)
	if resource.SchemaDefinition == nil {
		schemaDefinitionBytes = []byte("[]")
	}
	indexConfigBytes, _ := sonic.Marshal(resource.IndexConfig)
	if resource.IndexConfig == nil {
		indexConfigBytes = []byte("{}")
	}
	logicDefinitionBytes, _ := sonic.Marshal(resource.LogicDefinition)
	if resource.LogicDefinition == nil {
		logicDefinitionBytes = []byte("[]")
	}
	if resource.LocalIndexStatus == "" {
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusUnavailable
	}

	sqlStr, vals, err := sq.Insert(RESOURCE_TABLE_NAME).
		Columns(
			"f_id",
			"f_catalog_id",
			"f_name",
			"f_tags",
			"f_description",
			"f_category",
			"f_status",
			"f_status_message",
			"f_last_discover_status",
			"f_schema",
			"f_source_identifier",
			"f_source_metadata",
			"f_schema_definition",
			"f_index_config",

			"f_logic_type",
			"f_logic_definition",

			"f_local_status",
			"f_local_index_name",
			"f_sync_mark",

			"f_creator",
			"f_creator_type",
			"f_create_time",
			"f_updater",
			"f_updater_type",
			"f_update_time",
		).
		Values(
			resource.ID,
			resource.CatalogID,
			resource.Name,
			tagsStr,
			resource.Description,
			resource.Category,
			resource.Status,
			resource.StatusMessage,
			resource.LastDiscoverStatus,
			resource.Schema,
			resource.SourceIdentifier,
			string(sourceMetadataBytes),
			string(schemaDefinitionBytes),
			string(indexConfigBytes),

			resource.LogicType,
			string(logicDefinitionBytes),

			resource.LocalIndexStatus,
			resource.LocalIndexName,
			resource.SyncMark,

			resource.Creator.ID,
			resource.Creator.Type,
			resource.CreateTime,
			resource.Updater.ID,
			resource.Updater.Type,
			resource.UpdateTime,
		).ToSql()
	if err != nil {
		otellog.LogError(ctx, "Failed to build insert resource sql", err)
		return err
	}

	otellog.LogInfo(ctx, fmt.Sprintf("Insert resource SQL: %s", sqlStr))

	if tx != nil {
		_, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		_, err = ra.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		otellog.LogError(ctx, "Insert resource failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// GetByID retrieves ra Resource by ID.
func (ra *resourceAccess) GetByID(ctx context.Context, tx *sql.Tx, id string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query resource by ID")
	defer span.End()

	span.SetAttributes(attr.Key("resource_id").String(id))

	builder := sq.Select(resourceDetailColumns...).
		From(RESOURCE_TABLE_NAME).
		Where(sq.Eq{"f_id": id})
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		logger.Errorf("Failed to build query resource sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, sqlStr, vals...)
	} else {
		row = ra.db.QueryRowContext(ctx, sqlStr, vals...)
	}
	resource, err := scanResourceDetail(row)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "")
		return nil, nil
	}
	if err != nil {
		logger.Errorf("Scan resource failed: %v", err)
		span.SetStatus(codes.Error, "Scan failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return resource, nil
}

// GetByIDs retrieves ra Resource by IDs.
func (ra *resourceAccess) GetByIDs(ctx context.Context, ids []string) ([]*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query resources by IDs")
	defer span.End()

	span.SetAttributes(attr.Key("resource_ids").StringSlice(ids))

	sqlStr, vals, err := sq.Select(resourceDetailColumns...).From(RESOURCE_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()
	if err != nil {
		logger.Errorf("Failed to build query resource sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return []*interfaces.Resource{}, err
	}

	rows, err := ra.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		logger.Errorf("Query resources failed: %v", err)
		span.SetStatus(codes.Error, "Query failed")
		return []*interfaces.Resource{}, err
	}
	defer func() { _ = rows.Close() }()

	resources := make([]*interfaces.Resource, 0)
	for rows.Next() {
		resource, err := scanResourceDetail(rows)
		if err != nil {
			logger.Errorf("Scan resource row failed: %v", err)
			span.SetStatus(codes.Error, "Scan row failed")
			return []*interfaces.Resource{}, err
		}

		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate resource rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return []*interfaces.Resource{}, err
	}

	span.SetStatus(codes.Ok, "")
	return resources, nil
}

// GetSummariesByIDs retrieves resource list summaries without loading extended JSON fields.
// Only lazy extract the scale information (column_count/row_count) from the original JSON, without deserializing the complete structure;
// Counting is completed on the Go side to be compatible with multi-dialect databases (such as MariaDB/DM8/KDB9, etc.) and does not rely on MySQL JSON functions.
func (ra *resourceAccess) GetSummariesByIDs(ctx context.Context, ids []string) ([]*interfaces.ResourceSummary, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query resource summaries by IDs")
	defer span.End()

	span.SetAttributes(attr.Key("resource_ids").StringSlice(ids))

	sqlStr, vals, err := sq.Select(resourceSummaryColumns...).
		From(RESOURCE_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()
	if err != nil {
		logger.Errorf("Failed to build query resource sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return []*interfaces.ResourceSummary{}, err
	}

	rows, err := ra.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		logger.Errorf("Query resources failed: %v", err)
		span.SetStatus(codes.Error, "Query failed")
		return []*interfaces.ResourceSummary{}, err
	}
	defer func() { _ = rows.Close() }()

	summaries := make([]*interfaces.ResourceSummary, 0)
	for rows.Next() {
		summary, err := scanResourceSummary(rows)
		if err != nil {
			logger.Errorf("Scan resource row failed: %v", err)
			span.SetStatus(codes.Error, "Scan row failed")
			return []*interfaces.ResourceSummary{}, err
		}

		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate resource rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return []*interfaces.ResourceSummary{}, err
	}

	span.SetStatus(codes.Ok, "")
	return summaries, nil
}

// GetPermissionRefsByIDs retrieves the resource-to-catalog relations by IDs.
func (ra *resourceAccess) GetPermissionRefsByIDs(ctx context.Context, ids []string) ([]interfaces.ResourcePermissionRef, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query resource catalog refs by IDs")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return []interfaces.ResourcePermissionRef{}, nil
	}

	sqlStr, vals, err := sq.Select(
		"f_id",
		"f_catalog_id",
	).From(RESOURCE_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	rows, err := ra.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	refs := make([]interfaces.ResourcePermissionRef, 0)
	for rows.Next() {
		var ref interfaces.ResourcePermissionRef
		if err := rows.Scan(&ref.ResourceID, &ref.CatalogID); err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return refs, nil
}

// GetByName retrieves ra Resource by catalog and name.
func (ra *resourceAccess) GetByName(ctx context.Context, catalogID string, name string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query resource by name")
	defer span.End()

	span.SetAttributes(attr.Key("resource_name").String(name))

	sqlStr, vals, err := sq.Select(resourceDetailColumns...).
		From(RESOURCE_TABLE_NAME).
		Where(sq.Eq{"f_catalog_id": catalogID}).
		Where(sq.Eq{"f_name": name}).
		ToSql()
	if err != nil {
		logger.Errorf("Failed to build select resource sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	row := ra.db.QueryRowContext(ctx, sqlStr, vals...)
	resource, err := scanResourceDetail(row)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "")
		return nil, nil
	}
	if err != nil {
		logger.Errorf("Scan resource failed: %v", err)
		span.SetStatus(codes.Error, "Scan failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return resource, nil
}

// ListPermissionRefs lists the minimal relations needed before list authorization.
func (ra *resourceAccess) ListPermissionRefs(ctx context.Context, params interfaces.ResourcesQueryParams) ([]interfaces.ResourcePermissionRef, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List resource permission refs")
	defer span.End()

	builder := sq.Select("f_id", "f_catalog_id").From(RESOURCE_TABLE_NAME)
	builder = applyResourceFilters(builder, params)

	builder = builder.OrderBy(resourceListOrderByClause(params.Sort, params.Direction))

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := ra.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	refs := make([]interfaces.ResourcePermissionRef, 0)
	for rows.Next() {
		var ref interfaces.ResourcePermissionRef
		if err := rows.Scan(&ref.ResourceID, &ref.CatalogID); err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate resource permission refs failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return refs, nil
}

// List lists resource summaries with filters.
func (ra *resourceAccess) List(ctx context.Context, params interfaces.ResourcesQueryParams) ([]*interfaces.ResourceSummary, int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "List resource summaries")
	defer span.End()

	builder := sq.Select(resourceSummaryColumns...).From(RESOURCE_TABLE_NAME)
	builder = applyResourceFilters(builder, params)

	countBuilder := sq.Select("COUNT(*)").From(RESOURCE_TABLE_NAME)
	countBuilder = applyResourceFilters(countBuilder, params)

	countSql, countVals, _ := countBuilder.ToSql()
	var total int64
	err := ra.db.QueryRowContext(ctx, countSql, countVals...).Scan(&total)
	if err != nil {
		logger.Errorf("Failed to count resources: %v", err)
		span.SetStatus(codes.Error, "Count failed")
		return nil, 0, err
	}

	// Pagination is applied in service after permission filtering.
	builder = builder.OrderBy(resourceListOrderByClause(params.Sort, params.Direction))

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, 0, err
	}

	rows, err := ra.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query failed")
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	summaries := make([]*interfaces.ResourceSummary, 0)
	for rows.Next() {
		summary, err := scanResourceSummary(rows)
		if err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, 0, err
		}

		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate resource rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, 0, err
	}

	span.SetStatus(codes.Ok, "")
	return summaries, total, nil
}

// Update updates ra Resource.
func (ra *resourceAccess) Update(ctx context.Context, tx *sql.Tx,
	resource *interfaces.Resource, expectedUpdateTime int64) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update resource")
	defer span.End()

	span.SetAttributes(attr.Key("resource_id").String(resource.ID))

	// Convert tags to string format
	tagsStr := libCommon.TagSlice2TagString(resource.Tags)

	// Serialize schema definition, index config, and logic definition.
	schemaDefinitionBytes, _ := sonic.Marshal(resource.SchemaDefinition)
	if resource.SchemaDefinition == nil {
		schemaDefinitionBytes = []byte("[]")
	}
	indexConfigBytes, _ := sonic.Marshal(resource.IndexConfig)
	if resource.IndexConfig == nil {
		indexConfigBytes = []byte("{}")
	}
	logicDefinitionBytes, _ := sonic.Marshal(resource.LogicDefinition)
	if resource.LogicDefinition == nil {
		logicDefinitionBytes = []byte("[]")
	}

	builder := sq.Update(RESOURCE_TABLE_NAME).
		Set("f_name", resource.Name).
		Set("f_tags", tagsStr).
		Set("f_description", resource.Description).
		Set("f_schema_definition", string(schemaDefinitionBytes)).
		Set("f_index_config", string(indexConfigBytes)).
		Set("f_logic_type", resource.LogicType).
		Set("f_logic_definition", string(logicDefinitionBytes)).
		Set("f_updater", resource.Updater.ID).
		Set("f_updater_type", resource.Updater.Type).
		Set("f_update_time", resource.UpdateTime).
		Where(sq.Eq{"f_id": resource.ID}).
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
		result, err = ra.db.ExecContext(ctx, sqlStr, vals...)
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

// UpdateLocalIndexName updates only the local index name so asynchronous build
// completion cannot overwrite metadata changed after the build task started.
func (ra *resourceAccess) UpdateLocalIndexName(ctx context.Context, tx *sql.Tx, id, localIndexName string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update resource local index name")
	defer span.End()

	span.SetAttributes(attr.Key("resource_id").String(id))
	sqlStr, vals, err := sq.Update(RESOURCE_TABLE_NAME).
		Set("f_local_index_name", localIndexName).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	if tx != nil {
		_, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		_, err = ra.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// UpdateLocalIndexState atomically updates the Resource-owned index state.
func (ra *resourceAccess) UpdateLocalIndexState(ctx context.Context, tx *sql.Tx, id string,
	localIndexStatus string, localIndexName string, syncMark string) (bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update resource local index state")
	defer span.End()

	span.SetAttributes(attr.Key("resource_id").String(id))
	builder := sq.Update(RESOURCE_TABLE_NAME).
		Set("f_local_status", localIndexStatus).
		Set("f_local_index_name", localIndexName).
		Set("f_sync_mark", syncMark).
		Where(sq.Eq{"f_id": id})

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}

	var result sql.Result
	if tx != nil {
		result, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		result, err = ra.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "Get affected rows failed")
		return false, err
	}

	span.SetStatus(codes.Ok, "")
	return rowsAffected > 0, nil
}

// UpdateSemanticMetadata updates only fields owned by semantic understanding
// and guards the write with the resource version read by the worker.
func (ra *resourceAccess) UpdateSemanticMetadata(ctx context.Context,
	tx *sql.Tx, resource *interfaces.Resource, expectedUpdateTime int64) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update resource semantic metadata")
	defer span.End()

	span.SetAttributes(attr.Key("resource_id").String(resource.ID))
	schemaDefinitionBytes, err := sonic.Marshal(resource.SchemaDefinition)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal schema definition failed")
		return 0, err
	}
	if resource.SchemaDefinition == nil {
		schemaDefinitionBytes = []byte("[]")
	}
	logicDefinitionBytes, err := sonic.Marshal(resource.LogicDefinition)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal logic definition failed")
		return 0, err
	}
	if resource.LogicDefinition == nil {
		logicDefinitionBytes = []byte("[]")
	}
	builder := sq.Update(RESOURCE_TABLE_NAME).
		Set("f_name", resource.Name).
		Set("f_description", resource.Description).
		Set("f_schema_definition", string(schemaDefinitionBytes)).
		Set("f_logic_definition", string(logicDefinitionBytes)).
		Set("f_updater", resource.Updater.ID).
		Set("f_updater_type", resource.Updater.Type).
		Set("f_update_time", resource.UpdateTime).
		Where(sq.Eq{"f_id": resource.ID}).
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
		result, err = ra.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "Get affected rows failed")
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return rowsAffected, nil
}

// UpdateDiscoveryMetadata updates only fields owned by discovery and guards the
// write with the resource version read by the worker.
func (ra *resourceAccess) UpdateDiscoveryMetadata(ctx context.Context,
	tx *sql.Tx, resource *interfaces.Resource, expectedUpdateTime int64) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update resource discovery metadata")
	defer span.End()

	span.SetAttributes(attr.Key("resource_id").String(resource.ID))
	sourceMetadataBytes, err := sonic.Marshal(resource.SourceMetadata)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal source metadata failed")
		return 0, err
	}
	if resource.SourceMetadata == nil {
		sourceMetadataBytes = []byte("{}")
	}
	schemaDefinitionBytes, err := sonic.Marshal(resource.SchemaDefinition)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal schema definition failed")
		return 0, err
	}
	if resource.SchemaDefinition == nil {
		schemaDefinitionBytes = []byte("[]")
	}

	builder := sq.Update(RESOURCE_TABLE_NAME).
		Set("f_description", resource.Description).
		Set("f_status_message", resource.StatusMessage).
		Set("f_source_metadata", string(sourceMetadataBytes)).
		Set("f_schema_definition", string(schemaDefinitionBytes)).
		Set("f_last_discover_status", resource.LastDiscoverStatus).
		Set("f_updater", resource.Updater.ID).
		Set("f_updater_type", resource.Updater.Type).
		Set("f_update_time", resource.UpdateTime).
		Where(sq.Eq{"f_id": resource.ID}).
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
		result, err = ra.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "Get affected rows failed")
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return rowsAffected, nil
}

// GetByCatalogID retrieves all Resources under a Catalog.
func (ra *resourceAccess) GetByCatalogID(ctx context.Context, catalogID string) ([]*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query resources by catalog ID")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	sqlStr, vals, err := sq.Select(resourceDetailColumns...).
		From(RESOURCE_TABLE_NAME).
		Where(sq.Eq{"f_catalog_id": catalogID}).
		ToSql()
	if err != nil {
		logger.Errorf("Failed to build query resources sql: %v", err)
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := ra.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		logger.Errorf("Query resources failed: %v", err)
		span.SetStatus(codes.Error, "Query failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	resources := make([]*interfaces.Resource, 0)
	for rows.Next() {
		resource, err := scanResourceDetail(rows)
		if err != nil {
			logger.Errorf("Scan resource row failed: %v", err)
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}

		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate resource rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return resources, nil
}

// UpdateStatus updates a Resource's status, using tx when provided.
func (ra *resourceAccess) UpdateStatus(ctx context.Context, tx *sql.Tx, id string, status string, statusMessage string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update resource status")
	defer span.End()

	span.SetAttributes(
		attr.Key("resource_id").String(id),
		attr.Key("status").String(status),
	)

	sqlStr, vals, err := sq.Update(RESOURCE_TABLE_NAME).
		Set("f_status", status).
		Set("f_status_message", statusMessage).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	if tx != nil {
		_, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		_, err = ra.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// UpdateDiscoverStatus updates a Resource's last discover status.
func (ra *resourceAccess) UpdateDiscoverStatus(ctx context.Context, id string, status string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Update resource discover status")
	defer span.End()

	span.SetAttributes(
		attr.Key("resource_id").String(id),
		attr.Key("last_discover_status").String(status),
	)

	sqlStr, vals, err := sq.Update(RESOURCE_TABLE_NAME).
		Set("f_last_discover_status", status).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	_, err = ra.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Update failed")
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ra *resourceAccess) DeleteByIDs(ctx context.Context, ids []string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete resources")
	defer span.End()

	span.SetAttributes(attr.Key("resource_ids").StringSlice(ids))

	if len(ids) == 0 {
		return nil
	}

	sqlStr, vals, _ := sq.Delete(RESOURCE_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		ToSql()

	_, err := ra.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Delete failed")
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// ListAuthResources lists resource auth resources with filters.
func (ra *resourceAccess) ListAuthResources(ctx context.Context, params interfaces.AuthResourceQueryParams) ([]*interfaces.AuthResourceEntry, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListAuthResources")
	defer span.End()

	builder := sq.Select(
		"f_id",
		"f_name",
	).From(RESOURCE_TABLE_NAME)

	if params.ID != "" {
		builder = builder.Where(sq.Eq{"f_id": params.ID})
	}

	if params.Keyword != "" {
		keyword := "%" + common.EscapeLikePattern(params.Keyword) + "%"
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

	rows, err := ra.db.QueryContext(ctx, sqlStr, vals...)
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
		entry.Type = interfaces.AUTH_RESOURCE_TYPE_RESOURCE
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Iterate resource authorization resource rows failed: %v", err)
		span.SetStatus(codes.Error, "Rows iteration failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return entries, nil
}

func (ra *resourceAccess) CheckExistByCategories(ctx context.Context, catalogID string, categories []string) (bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Check resources exist")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	countBuilder := sq.Select("COUNT(*)").
		From(RESOURCE_TABLE_NAME)

	if catalogID != "" {
		countBuilder = countBuilder.Where(sq.Eq{"f_catalog_id": catalogID})
	}
	if len(categories) > 0 {
		countBuilder = countBuilder.Where(sq.Eq{"f_category": categories})
	}

	countSql, countVals, _ := countBuilder.ToSql()
	var total int64
	err := ra.db.QueryRowContext(ctx, countSql, countVals...).Scan(&total)
	if err != nil {
		logger.Errorf("Failed to count resources: %v", err)
		span.SetStatus(codes.Error, "Count failed")
		return false, err
	}

	span.SetStatus(codes.Ok, "")
	return total > 0, nil
}

func (ra *resourceAccess) DeleteByCatalogID(ctx context.Context, tx *sql.Tx, catalogID string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete resources by catalog ID")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	sqlStr, vals, err := sq.Delete(RESOURCE_TABLE_NAME).
		Where(sq.Eq{"f_catalog_id": catalogID}).
		ToSql()

	if tx != nil {
		_, err = tx.ExecContext(ctx, sqlStr, vals...)
	} else {
		_, err = ra.db.ExecContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Delete failed")
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// resourceListOrderByClause translates API sort fields into a safe ORDER BY clause.
// Empty or unknown sort values fall back to update time descending.
func resourceListOrderByClause(sort, direction string) string {
	dir := "DESC"
	if strings.EqualFold(direction, interfaces.ASC_DIRECTION) {
		dir = "ASC"
	}

	switch sort {
	case interfaces.ResourceSortName:
		return "f_name " + dir
	case interfaces.ResourceSortCreateTime:
		return "f_create_time " + dir
	case interfaces.ResourceSortUpdateTime:
		return "f_update_time " + dir
	default:
		return "f_update_time DESC"
	}
}
