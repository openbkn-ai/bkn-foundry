// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"

	sq "github.com/Masterminds/squirrel"
	"github.com/bytedance/sonic"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	OT_TABLE_NAME                    = "t_object_type"
	OT_STATUS_TABLE_NAME             = "t_object_type_status"
	objectTypeIdentityQueryBatchSize = 500
	objectTypeInsertBatchSize        = 200
	objectTypeStatusInsertBatchSize  = 500
	objectTypeUpdateBatchSize        = 200
)

var (
	otAccessOnce sync.Once
	otAccess     interfaces.ObjectTypeAccess
)

type objectTypeAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

func NewObjectTypeAccess(appSetting *common.AppSetting) interfaces.ObjectTypeAccess {
	otAccessOnce.Do(func() {
		otAccess = &objectTypeAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return otAccess
}

// Get object type existence by ID.
func (ota *objectTypeAccess) CheckObjectTypeExistByID(ctx context.Context, knID string, branch string, otID string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CheckObjectTypeExistByID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	sqlStr, vals, err := sq.Select(
		"f_name").
		From(OT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": otID}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of get object type id by f_id, error", err)
		return "", false, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var name string
	err = ota.db.QueryRow(sqlStr, vals...).Scan(&name)
	if err == sql.ErrNoRows {
		span.SetAttributes(attr.Key("no_rows").Bool(true))
		span.SetStatus(codes.Ok, "")
		return "", false, nil
	} else if err != nil {
		common.LogSafeError(ctx, "Row scan failed, err", err)
		return "", false, err
	}

	span.SetStatus(codes.Ok, "")
	return name, true, nil
}

// Get object type existence by name.
func (ota *objectTypeAccess) CheckObjectTypeExistByName(ctx context.Context, knID string, branch string, name string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CheckObjectTypeExistByName")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	sqlStr, vals, err := sq.Select("f_id").
		From(OT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_name": name}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of get id by name, error", err)
		return "", false, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var otID string
	err = ota.db.QueryRow(sqlStr, vals...).Scan(
		&otID,
	)
	if err == sql.ErrNoRows {
		span.SetAttributes(attr.Key("no_rows").Bool(true))
		span.SetStatus(codes.Ok, "")
		return "", false, nil
	} else if err != nil {
		common.LogSafeError(ctx, "Row scan failed, err", err)
		return "", false, err
	}

	span.SetStatus(codes.Ok, "")
	return otID, true, nil
}

func (ota *objectTypeAccess) GetObjectTypeIdentitiesByIDsOrNames(ctx context.Context, knID string,
	branch string, otIDs, otNames []string) ([]*interfaces.ObjectType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetObjectTypeIdentitiesByIDsOrNames")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
		attr.Int("id_count", len(otIDs)),
		attr.Int("name_count", len(otNames)),
	)
	result := make([]*interfaces.ObjectType, 0)
	seen := make(map[string]struct{})
	requestCount := max(len(otIDs), len(otNames))
	for start := 0; start < requestCount; start += objectTypeIdentityQueryBatchSize {
		idEnd := min(start+objectTypeIdentityQueryBatchSize, len(otIDs))
		nameEnd := min(start+objectTypeIdentityQueryBatchSize, len(otNames))
		conditions := sq.Or{}
		if start < len(otIDs) {
			conditions = append(conditions, sq.Eq{"f_id": otIDs[start:idEnd]})
		}
		if start < len(otNames) {
			conditions = append(conditions, sq.Eq{"f_name": otNames[start:nameEnd]})
		}
		builder := sq.Select("f_id", "f_name").From(OT_TABLE_NAME).
			Where(sq.Eq{"f_kn_id": knID}).
			Where(sq.Eq{"f_branch": branch}).
			Where(conditions)
		sqlStr, vals, err := builder.ToSql()
		if err != nil {
			common.LogSafeError(ctx, "Failed to build object type identity query", err)
			return nil, err
		}
		otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))
		rows, err := ota.db.Query(sqlStr, vals...)
		if err != nil {
			common.LogSafeError(ctx, "Query object type identities failed", err)
			return nil, err
		}
		for rows.Next() {
			objectType := &interfaces.ObjectType{ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE}
			if err = rows.Scan(&objectType.OTID, &objectType.OTName); err != nil {
				_ = rows.Close()
				common.LogSafeError(ctx, "Scan object type identity failed", err)
				return nil, err
			}
			if _, duplicate := seen[objectType.OTID]; duplicate {
				continue
			}
			seen[objectType.OTID] = struct{}{}
			result = append(result, objectType)
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			common.LogSafeError(ctx, "Iterate object type identities failed", err)
			return nil, err
		}
		_ = rows.Close()
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// Create an object type.
func (ota *objectTypeAccess) CreateObjectType(ctx context.Context, tx *sql.Tx, objectType *interfaces.ObjectType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateObjectType")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Convert tags to a string.
	tagsStr := libCommon.TagSlice2TagString(objectType.Tags)

	// 2.0 Serialize the data source.
	dataSourceBytes, err := sonic.Marshal(objectType.DataSource)
	if err != nil {
		common.LogSafeError(ctx, "Failed to marshal DataSource, err", err)
		return err
	}
	// 2.1 Serialize data properties.
	dataPropertiesBytes, err := sonic.Marshal(objectType.DataProperties)
	if err != nil {
		common.LogSafeError(ctx, "Failed to marshal DataProperties, err", err)
		return err
	}
	// 2.2 Serialize logical properties.
	logicPropertiesBytes, err := sonic.Marshal(objectType.LogicProperties)
	if err != nil {
		common.LogSafeError(ctx, "Failed to marshal LogicProperties, err", err)
		return err
	}
	// 2.3 Serialize the primary key array.
	primaryKeysBytes, err := sonic.Marshal(objectType.PrimaryKeys)
	if err != nil {
		common.LogSafeError(ctx, "Failed to marshal PrimaryKeys, err", err)
		return err
	}

	sqlStr, vals, err := sq.Insert(OT_TABLE_NAME).
		Columns(
			"f_id",
			"f_name",
			"f_tags",
			"f_comment",
			"f_icon",
			"f_color",
			"f_bkn_raw_content",
			"f_kn_id",
			"f_branch",
			"f_data_source",
			"f_data_properties",
			"f_logic_properties",
			"f_primary_keys",
			"f_display_key",
			"f_incremental_key",
			"f_creator",
			"f_creator_type",
			"f_create_time",
			"f_updater",
			"f_updater_type",
			"f_update_time",
		).
		Values(
			objectType.OTID,
			objectType.OTName,
			tagsStr,
			objectType.Comment,
			objectType.Icon,
			objectType.Color,
			objectType.BKNRawContent,
			objectType.KNID,
			objectType.Branch,
			dataSourceBytes,
			dataPropertiesBytes,
			logicPropertiesBytes,
			primaryKeysBytes,
			objectType.DisplayKey,
			objectType.IncrementalKey,
			objectType.Creator.ID,
			objectType.Creator.Type,
			objectType.CreateTime,
			objectType.Updater.ID,
			objectType.Updater.Type,
			objectType.UpdateTime).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of insert object type, error", err)
		return err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	if tx != nil {
		_, err = tx.Exec(sqlStr, vals...)
	} else {
		_, err = ota.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		common.LogSafeError(ctx, "Insert data error", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ota *objectTypeAccess) CreateObjectTypes(ctx context.Context, tx *sql.Tx,
	objectTypes []*interfaces.ObjectType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateObjectTypes")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
		attr.Int("object_type_count", len(objectTypes)),
	)
	for start := 0; start < len(objectTypes); start += objectTypeInsertBatchSize {
		end := min(start+objectTypeInsertBatchSize, len(objectTypes))
		builder := sq.Insert(OT_TABLE_NAME).Columns(
			"f_id", "f_name", "f_tags", "f_comment", "f_icon", "f_color", "f_bkn_raw_content",
			"f_kn_id", "f_branch", "f_data_source", "f_data_properties", "f_logic_properties",
			"f_primary_keys", "f_display_key", "f_incremental_key", "f_creator", "f_creator_type",
			"f_create_time", "f_updater", "f_updater_type", "f_update_time",
		)
		for _, objectType := range objectTypes[start:end] {
			values, err := objectTypeInsertValues(objectType)
			if err != nil {
				common.LogSafeError(ctx, "Failed to marshal object type for batch insert", err)
				return err
			}
			builder = builder.Values(values...)
		}
		sqlStr, vals, err := builder.ToSql()
		if err != nil {
			common.LogSafeError(ctx, "Failed to build the sql of batch insert object types", err)
			return err
		}
		otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))
		if tx != nil {
			_, err = tx.Exec(sqlStr, vals...)
		} else {
			_, err = ota.db.Exec(sqlStr, vals...)
		}
		if err != nil {
			common.LogSafeError(ctx, "Batch insert object types failed", err)
			return err
		}
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func objectTypeInsertValues(objectType *interfaces.ObjectType) ([]any, error) {
	dataSourceBytes, err := sonic.Marshal(objectType.DataSource)
	if err != nil {
		return nil, err
	}
	dataPropertiesBytes, err := sonic.Marshal(objectType.DataProperties)
	if err != nil {
		return nil, err
	}
	logicPropertiesBytes, err := sonic.Marshal(objectType.LogicProperties)
	if err != nil {
		return nil, err
	}
	primaryKeysBytes, err := sonic.Marshal(objectType.PrimaryKeys)
	if err != nil {
		return nil, err
	}
	return []any{
		objectType.OTID, objectType.OTName, libCommon.TagSlice2TagString(objectType.Tags), objectType.Comment,
		objectType.Icon, objectType.Color, objectType.BKNRawContent, objectType.KNID, objectType.Branch,
		dataSourceBytes, dataPropertiesBytes, logicPropertiesBytes, primaryKeysBytes, objectType.DisplayKey,
		objectType.IncrementalKey, objectType.Creator.ID, objectType.Creator.Type, objectType.CreateTime,
		objectType.Updater.ID, objectType.Updater.Type, objectType.UpdateTime,
	}, nil
}

// Object type creation status.
func (ota *objectTypeAccess) CreateObjectTypeStatus(ctx context.Context, tx *sql.Tx, objectType *interfaces.ObjectType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateObjectTypeStatus")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	sqlStr, vals, err := sq.Insert(OT_STATUS_TABLE_NAME).
		Columns(
			"f_id",
			"f_kn_id",
			"f_branch",
			"f_incremental_key",
			"f_update_time",
		).
		Values(
			objectType.OTID,
			objectType.KNID,
			objectType.Branch,
			objectType.IncrementalKey,
			objectType.UpdateTime).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of insert object type status, error", err)
		return err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	if tx != nil {
		_, err = tx.Exec(sqlStr, vals...)
	} else {
		_, err = ota.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		common.LogSafeError(ctx, "Insert data error", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ota *objectTypeAccess) CreateObjectTypeStatuses(ctx context.Context, tx *sql.Tx,
	objectTypes []*interfaces.ObjectType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateObjectTypeStatuses")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
		attr.Int("object_type_count", len(objectTypes)),
	)
	for start := 0; start < len(objectTypes); start += objectTypeStatusInsertBatchSize {
		end := min(start+objectTypeStatusInsertBatchSize, len(objectTypes))
		builder := sq.Insert(OT_STATUS_TABLE_NAME).Columns(
			"f_id", "f_kn_id", "f_branch", "f_incremental_key", "f_update_time",
		)
		for _, objectType := range objectTypes[start:end] {
			builder = builder.Values(
				objectType.OTID, objectType.KNID, objectType.Branch,
				objectType.IncrementalKey, objectType.UpdateTime,
			)
		}
		sqlStr, vals, err := builder.ToSql()
		if err != nil {
			common.LogSafeError(ctx, "Failed to build the sql of batch insert object type statuses", err)
			return err
		}
		otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))
		if tx != nil {
			_, err = tx.Exec(sqlStr, vals...)
		} else {
			_, err = ota.db.Exec(sqlStr, vals...)
		}
		if err != nil {
			common.LogSafeError(ctx, "Batch insert object type statuses failed", err)
			return err
		}
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// Query current object types on the main branch.
func (ota *objectTypeAccess) ListObjectTypes(ctx context.Context, tx *sql.Tx, query interfaces.ObjectTypesQueryParams) ([]*interfaces.ObjectType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListObjectTypes")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	subBuilder := sq.Select(
		"ot.f_id",
		"ot.f_name",
		"ot.f_tags",
		"ot.f_comment",
		"ot.f_icon",
		"ot.f_color",
		"ot.f_bkn_raw_content",
		"ot.f_kn_id",
		"ot.f_branch",
		"ot.f_data_source",
		"ot.f_data_properties",
		"ot.f_logic_properties",
		"ot.f_primary_keys",
		"ot.f_display_key",
		"ot.f_incremental_key",
		"ot.f_creator",
		"ot.f_creator_type",
		"ot.f_create_time",
		"ot.f_updater",
		"ot.f_updater_type",
		"ot.f_update_time",

		"ots.f_incremental_key",
		"ots.f_incremental_value",
		"ots.f_index",
		"ots.f_index_available",
		"ots.f_doc_count",
		"ots.f_storage_size",
		"ots.f_update_time",
	).From(OT_TABLE_NAME + " AS ot").
		Join(OT_STATUS_TABLE_NAME + " AS ots ON ot.f_id = ots.f_id AND ot.f_kn_id = ots.f_kn_id AND ot.f_branch = ots.f_branch")

	builder := processQueryCondition(query, subBuilder)

	// Sort.
	if query.Sort != "" {
		orderBy, err := common.SafeOrderBy(query.Sort, query.Direction)
		if err != nil {
			return nil, err
		}
		builder = builder.OrderBy("ot."+orderBy, "ot.f_id ASC")
	}
	if query.Limit > 0 {
		builder = builder.Limit(uint64(query.Limit))
		if query.Offset > 0 {
			builder = builder.Offset(uint64(query.Offset))
		}
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select object types, error", err)
		return []*interfaces.ObjectType{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var rows *sql.Rows
	if tx != nil {
		rows, err = tx.Query(sqlStr, vals...)
	} else {
		rows, err = ota.db.Query(sqlStr, vals...)
	}
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []*interfaces.ObjectType{}, err
	}
	defer func() { _ = rows.Close() }()

	objectTypes := make([]*interfaces.ObjectType, 0)
	for rows.Next() {
		objectType := interfaces.ObjectType{
			ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE,
			Status:     &interfaces.ObjectTypeStatus{},
		}
		tagsStr := ""
		var (
			dataSourceBytes      []byte
			dataPropertiesBytes  []byte
			logicPropertiesBytes []byte
			primaryKeysBytes     []byte
		)
		err := rows.Scan(
			&objectType.OTID,
			&objectType.OTName,
			&tagsStr,
			&objectType.Comment,
			&objectType.Icon,
			&objectType.Color,
			&objectType.BKNRawContent,
			&objectType.KNID,
			&objectType.Branch,
			&dataSourceBytes,
			&dataPropertiesBytes,
			&logicPropertiesBytes,
			&primaryKeysBytes,
			&objectType.DisplayKey,
			&objectType.IncrementalKey,
			&objectType.Creator.ID,
			&objectType.Creator.Type,
			&objectType.CreateTime,
			&objectType.Updater.ID,
			&objectType.Updater.Type,
			&objectType.UpdateTime,

			&objectType.Status.IncrementalKey,
			&objectType.Status.IncrementalValue,
			&objectType.Status.Index,
			&objectType.Status.IndexAvailable,
			&objectType.Status.DocCount,
			&objectType.Status.StorageSize,
			&objectType.Status.UpdateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []*interfaces.ObjectType{}, err
		}

		// Convert a tag string to an array.
		objectType.Tags = libCommon.TagString2TagSlice(tagsStr)

		// 2.0 Deserialize the data source.
		err = common.UnmarshalStoredJSON(dataSourceBytes, &objectType.DataSource)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal dataSource after getting object type, err", err)
			return []*interfaces.ObjectType{}, err
		}

		// 2.1 Deserialize data properties.
		err = common.UnmarshalStoredJSON(dataPropertiesBytes, &objectType.DataProperties)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal dataProperties after getting object type, err", err)
			return []*interfaces.ObjectType{}, err
		}

		// 2.2 Deserialize logical properties.
		err = common.UnmarshalStoredJSON(logicPropertiesBytes, &objectType.LogicProperties)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal logicProperties after getting object type, err", err)
			return []*interfaces.ObjectType{}, err
		}

		// 2.3 Deserialize primary keys.
		err = common.UnmarshalStoredJSON(primaryKeysBytes, &objectType.PrimaryKeys)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal primaryKeys after getting object type, err", err)
			return []*interfaces.ObjectType{}, err
		}

		objectTypes = append(objectTypes, &objectType)
	}

	span.SetStatus(codes.Ok, "")
	return objectTypes, nil
}

// ListObjectTypeSummaries reads only fields required by list and overview
// surfaces. Detail-only model JSON stays on the detail endpoint.
func (ota *objectTypeAccess) ListObjectTypeSummaries(ctx context.Context, tx *sql.Tx,
	query interfaces.ObjectTypesQueryParams) ([]*interfaces.ObjectType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListObjectTypeSummaries")
	defer span.End()

	builder := processQueryCondition(query, sq.Select(
		"ot.f_id",
		"ot.f_name",
		"ot.f_tags",
		"ot.f_comment",
		"ot.f_icon",
		"ot.f_color",
		"ot.f_kn_id",
		"ot.f_branch",
		"ot.f_data_source",
		"ot.f_creator",
		"ot.f_creator_type",
		"ot.f_create_time",
		"ot.f_updater",
		"ot.f_updater_type",
		"ot.f_update_time",
		"ots.f_incremental_key",
		"ots.f_incremental_value",
		"ots.f_index",
		"ots.f_index_available",
		"ots.f_doc_count",
		"ots.f_storage_size",
		"ots.f_update_time",
	).From(OT_TABLE_NAME+" AS ot").
		Join(OT_STATUS_TABLE_NAME+" AS ots ON ot.f_id = ots.f_id AND ot.f_kn_id = ots.f_kn_id AND ot.f_branch = ots.f_branch"))

	if query.Sort != "" {
		orderBy, err := common.SafeOrderBy(query.Sort, query.Direction)
		if err != nil {
			return nil, err
		}
		builder = builder.OrderBy("ot."+orderBy, "ot.f_id ASC")
	}
	if query.Limit > 0 {
		builder = builder.Limit(uint64(query.Limit))
		if query.Offset > 0 {
			builder = builder.Offset(uint64(query.Offset))
		}
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		return nil, err
	}
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))
	var rows *sql.Rows
	if tx != nil {
		rows, err = tx.QueryContext(ctx, sqlStr, vals...)
	} else {
		rows, err = ota.db.QueryContext(ctx, sqlStr, vals...)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make([]*interfaces.ObjectType, 0)
	for rows.Next() {
		item := &interfaces.ObjectType{
			ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE,
			Status:     &interfaces.ObjectTypeStatus{},
		}
		var tags string
		var dataSource []byte
		if err := rows.Scan(
			&item.OTID,
			&item.OTName,
			&tags,
			&item.Comment,
			&item.Icon,
			&item.Color,
			&item.KNID,
			&item.Branch,
			&dataSource,
			&item.Creator.ID,
			&item.Creator.Type,
			&item.CreateTime,
			&item.Updater.ID,
			&item.Updater.Type,
			&item.UpdateTime,
			&item.Status.IncrementalKey,
			&item.Status.IncrementalValue,
			&item.Status.Index,
			&item.Status.IndexAvailable,
			&item.Status.DocCount,
			&item.Status.StorageSize,
			&item.Status.UpdateTime,
		); err != nil {
			return nil, err
		}
		item.Tags = libCommon.TagString2TagSlice(tags)
		if err := common.UnmarshalStoredJSON(dataSource, &item.DataSource); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// ListObjectTypeLogicProperties reads only the ID, name and logic properties of the matching
// object types. Capability provenance needs nothing else, and the raw import payload and data
// properties it skips are the bulk of a large network: at 10,000 object types they are hundreds
// of megabytes that the statistics path used to read and decode just to find the tools in use.
//
// The status join is kept so the result covers the same object types as ListObjectTypes. Only
// the filters of query apply: it never pages or sorts, because its caller needs every object type
// that uses a tool, and a page of them would read as "nothing else uses it".
func (ota *objectTypeAccess) ListObjectTypeLogicProperties(ctx context.Context,
	query interfaces.ObjectTypesQueryParams) ([]*interfaces.ObjectType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListObjectTypeLogicProperties")
	defer span.End()

	builder := processQueryCondition(query, sq.Select(
		"ot.f_id",
		"ot.f_name",
		"ot.f_logic_properties",
	).From(OT_TABLE_NAME+" AS ot").
		Join(OT_STATUS_TABLE_NAME+" AS ots ON ot.f_id = ots.f_id AND ot.f_kn_id = ots.f_kn_id AND ot.f_branch = ots.f_branch"))

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		return nil, err
	}
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))
	rows, err := ota.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make([]*interfaces.ObjectType, 0)
	logicBytesTotal := 0
	for rows.Next() {
		item := &interfaces.ObjectType{ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE}
		var logicProperties []byte
		if err := rows.Scan(&item.OTID, &item.OTName, &logicProperties); err != nil {
			return nil, err
		}
		logicBytesTotal += len(logicProperties)
		if err := common.UnmarshalStoredJSON(logicProperties, &item.LogicProperties); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	span.SetAttributes(
		attr.Key("row_count").Int(len(result)),
		attr.Key("logic_properties_bytes").Int(logicBytesTotal),
	)
	span.SetStatus(codes.Ok, "")
	return result, nil
}

func (ota *objectTypeAccess) GetObjectTypesTotal(ctx context.Context, query interfaces.ObjectTypesQueryParams) (int, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetObjectTypesTotal")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	subBuilder := sq.Select("COUNT(ot.f_id)").
		From(OT_TABLE_NAME + " AS ot")
	builder := processQueryCondition(query, subBuilder)

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select object types total, error", err)
		return 0, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	total := 0
	err = ota.db.QueryRow(sqlStr, vals...).Scan(&total)
	if err != nil {
		common.LogSafeError(ctx, "Get object type totals error", err)
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return total, nil
}

func (ota *objectTypeAccess) GetObjectTypeByID(ctx context.Context, tx *sql.Tx, knID string, branch string, otID string) (*interfaces.ObjectType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetObjectTypeByID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	sqlStr, vals, err := sq.Select(
		"ot.f_id",
		"ot.f_name",
		"ot.f_tags",
		"ot.f_comment",
		"ot.f_icon",
		"ot.f_color",
		"ot.f_bkn_raw_content",
		"ot.f_kn_id",
		"ot.f_branch",
		"ot.f_data_source",
		"ot.f_data_properties",
		"ot.f_logic_properties",
		"ot.f_primary_keys",
		"ot.f_display_key",
		"ot.f_incremental_key",
		"ot.f_creator",
		"ot.f_creator_type",
		"ot.f_create_time",
		"ot.f_updater",
		"ot.f_updater_type",
		"ot.f_update_time",

		"ots.f_incremental_key",
		"ots.f_incremental_value",
		"ots.f_index",
		"ots.f_index_available",
		"ots.f_doc_count",
		"ots.f_storage_size",
		"ots.f_update_time",
	).From(OT_TABLE_NAME + " AS ot").
		Join(OT_STATUS_TABLE_NAME + " AS ots ON ot.f_id = ots.f_id AND ot.f_kn_id = ots.f_kn_id AND ot.f_branch = ots.f_branch").
		Where(sq.Eq{"ot.f_kn_id": knID}).
		Where(sq.Eq{"ot.f_branch": branch}).
		Where(sq.Eq{"ot.f_id": otID}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select object type by id, error", err)
		return nil, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	objectType := interfaces.ObjectType{
		ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE,
		Status:     &interfaces.ObjectTypeStatus{},
	}
	tagsStr := ""
	var (
		dataSourceBytes      []byte
		dataPropertiesBytes  []byte
		logicPropertiesBytes []byte
		primaryKeysBytes     []byte
	)

	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, sqlStr, vals...)
	} else {
		row = ota.db.QueryRowContext(ctx, sqlStr, vals...)
	}
	err = row.Scan(
		&objectType.OTID,
		&objectType.OTName,
		&tagsStr,
		&objectType.Comment,
		&objectType.Icon,
		&objectType.Color,
		&objectType.BKNRawContent,
		&objectType.KNID,
		&objectType.Branch,
		&dataSourceBytes,
		&dataPropertiesBytes,
		&logicPropertiesBytes,
		&primaryKeysBytes,
		&objectType.DisplayKey,
		&objectType.IncrementalKey,
		&objectType.Creator.ID,
		&objectType.Creator.Type,
		&objectType.CreateTime,
		&objectType.Updater.ID,
		&objectType.Updater.Type,
		&objectType.UpdateTime,

		&objectType.Status.IncrementalKey,
		&objectType.Status.IncrementalValue,
		&objectType.Status.Index,
		&objectType.Status.IndexAvailable,
		&objectType.Status.DocCount,
		&objectType.Status.StorageSize,
		&objectType.Status.UpdateTime,
	)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Error, "Object type not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ObjectType_ObjectTypeNotFound).
			WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx),
				"BknBackend.ObjectType.InvalidParameter.Detail.ObjectTypeNotFound",
				map[string]any{"objectTypeID": otID}))
	} else if err != nil {
		common.LogSafeError(ctx, "Row scan error", err)
		return nil, err
	}

	// Convert a tag string to an array.
	objectType.Tags = libCommon.TagString2TagSlice(tagsStr)

	// 2.0 Deserialize the data source.
	err = common.UnmarshalStoredJSON(dataSourceBytes, &objectType.DataSource)
	if err != nil {
		common.LogSafeError(ctx, "Failed to unmarshal dataSource after getting object type, err", err)
		return nil, err
	}

	// 2.1 Deserialize data properties.
	err = common.UnmarshalStoredJSON(dataPropertiesBytes, &objectType.DataProperties)
	if err != nil {
		common.LogSafeError(ctx, "Failed to unmarshal dataProperties after getting object type, err", err)
		return nil, err
	}

	// 2.2 Deserialize logical properties.
	err = common.UnmarshalStoredJSON(logicPropertiesBytes, &objectType.LogicProperties)
	if err != nil {
		common.LogSafeError(ctx, "Failed to unmarshal logicProperties after getting object type, err", err)
		return nil, err
	}

	// 2.3 Deserialize primary keys.
	err = common.UnmarshalStoredJSON(primaryKeysBytes, &objectType.PrimaryKeys)
	if err != nil {
		common.LogSafeError(ctx, "Failed to unmarshal primaryKeys after getting object type, err", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return &objectType, nil
}

func (ota *objectTypeAccess) GetObjectTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, otIDs []string) ([]*interfaces.ObjectType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetObjectTypesByIDs")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	sqlStr, vals, err := sq.Select(
		"ot.f_id",
		"ot.f_name",
		"ot.f_tags",
		"ot.f_comment",
		"ot.f_icon",
		"ot.f_color",
		"ot.f_bkn_raw_content",
		"ot.f_kn_id",
		"ot.f_branch",
		"ot.f_data_source",
		"ot.f_data_properties",
		"ot.f_logic_properties",
		"ot.f_primary_keys",
		"ot.f_display_key",
		"ot.f_incremental_key",
		"ot.f_creator",
		"ot.f_creator_type",
		"ot.f_create_time",
		"ot.f_updater",
		"ot.f_updater_type",
		"ot.f_update_time",

		"ots.f_incremental_key",
		"ots.f_incremental_value",
		"ots.f_index",
		"ots.f_index_available",
		"ots.f_doc_count",
		"ots.f_storage_size",
		"ots.f_update_time",
	).From(OT_TABLE_NAME + " AS ot").
		Join(OT_STATUS_TABLE_NAME + " AS ots ON ot.f_id = ots.f_id AND ot.f_kn_id = ots.f_kn_id AND ot.f_branch = ots.f_branch").
		Where(sq.Eq{"ot.f_kn_id": knID}).
		Where(sq.Eq{"ot.f_branch": branch}).
		Where(sq.Eq{"ot.f_id": otIDs}).
		ToSql()

		// if len(cgIds) > 0 {
		// 	// Subquery: retrieve object type concept IDs in the specified concept group.
		// 	subQueryBuilder := sq.Select("cgr.f_concept_id").
		// 		From("t_concept_group_relation AS cgr").
		// 		Join(OT_TABLE_NAME + " AS ot ON cgr.f_concept_id = ot.f_id AND cgr.f_branch = ot.f_branch AND cgr.f_kn_id = ot.f_kn_id").
		// 		Join("t_concept_group AS cg ON cgr.f_group_id = cg.f_id AND cgr.f_branch = cg.f_branch AND cgr.f_kn_id = cg.f_kn_id").
		// 		Where(sq.Eq{"cgr.f_kn_id": knID}).
		// 		Where(sq.Eq{"cgr.f_branch": "main"}).
		// 		Where(sq.Eq{"cgr.f_group_id": cgIds}).
		// 		Where(sq.Eq{"cgr.f_concept_type": interfaces.MODULE_TYPE_OBJECT_TYPE})

		// 	builder = builder.Where(sq.Expr("f_id IN (?)", subQueryBuilder))
		// 	// if query.Branch != "" {
		// 	// 	subBuilder = subBuilder.Where(sq.Eq{fmt.Sprintf("%s%s", fieldPrefix, "f_branch"): query.Branch})
		// 	// } else {
		// 	// 	// Query business knowledge networks on the main branch.
		// 	// 	subBuilder = subBuilder.Where(sq.Eq{fmt.Sprintf("%s%s", fieldPrefix, "f_branch"): interfaces.MAIN_BRANCH})
		// 	// }
		// }

	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select object type by id, error", err)
		return []*interfaces.ObjectType{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var rows *sql.Rows
	if tx != nil {
		rows, err = tx.Query(sqlStr, vals...)
	} else {
		rows, err = ota.db.Query(sqlStr, vals...)
	}
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []*interfaces.ObjectType{}, err
	}
	defer func() { _ = rows.Close() }()

	objectTypes := make([]*interfaces.ObjectType, 0)
	for rows.Next() {
		objectType := interfaces.ObjectType{
			ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE,
			Status:     &interfaces.ObjectTypeStatus{},
		}
		tagsStr := ""
		var (
			dataSourceBytes      []byte
			dataPropertiesBytes  []byte
			logicPropertiesBytes []byte
			primaryKeysBytes     []byte
		)

		err := rows.Scan(
			&objectType.OTID,
			&objectType.OTName,
			&tagsStr,
			&objectType.Comment,
			&objectType.Icon,
			&objectType.Color,
			&objectType.BKNRawContent,
			&objectType.KNID,
			&objectType.Branch,
			&dataSourceBytes,
			&dataPropertiesBytes,
			&logicPropertiesBytes,
			&primaryKeysBytes,
			&objectType.DisplayKey,
			&objectType.IncrementalKey,
			&objectType.Creator.ID,
			&objectType.Creator.Type,
			&objectType.CreateTime,
			&objectType.Updater.ID,
			&objectType.Updater.Type,
			&objectType.UpdateTime,

			&objectType.Status.IncrementalKey,
			&objectType.Status.IncrementalValue,
			&objectType.Status.Index,
			&objectType.Status.IndexAvailable,
			&objectType.Status.DocCount,
			&objectType.Status.StorageSize,
			&objectType.Status.UpdateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []*interfaces.ObjectType{}, err
		}

		// Convert a tag string to an array.
		objectType.Tags = libCommon.TagString2TagSlice(tagsStr)

		// 2.0 Deserialize the data source.
		err = common.UnmarshalStoredJSON(dataSourceBytes, &objectType.DataSource)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal dataSource after getting object type, err", err)
			return []*interfaces.ObjectType{}, err
		}

		// 2.1 Deserialize data properties.
		err = common.UnmarshalStoredJSON(dataPropertiesBytes, &objectType.DataProperties)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal dataProperties after getting object type, err", err)
			return []*interfaces.ObjectType{}, err
		}

		// 2.2 Deserialize logical properties.
		err = common.UnmarshalStoredJSON(logicPropertiesBytes, &objectType.LogicProperties)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal logicProperties after getting object type, err", err)
			return []*interfaces.ObjectType{}, err
		}

		// 2.3 Deserialize primary keys.
		err = common.UnmarshalStoredJSON(primaryKeysBytes, &objectType.PrimaryKeys)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal primaryKeys after getting object type, err", err)
			return []*interfaces.ObjectType{}, err
		}

		objectTypes = append(objectTypes, &objectType)
	}

	span.SetStatus(codes.Ok, "")
	return objectTypes, nil
}

func (ota *objectTypeAccess) UpdateObjectType(ctx context.Context, tx *sql.Tx, objectType *interfaces.ObjectType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "UpdateObjectType")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Convert tags to a string.
	tagsStr := libCommon.TagSlice2TagString(objectType.Tags)
	// 2.0 Serialize the data source.
	dataSourceBytes, err := sonic.Marshal(objectType.DataSource)
	if err != nil {
		logger.Errorf("Failed to marshal DataSource, err: %v", common.SafeErrorSummary(err))
		return err
	}
	// 2.1 Serialize data properties.
	dataPropertiesBytes, err := sonic.Marshal(objectType.DataProperties)
	if err != nil {
		logger.Errorf("Failed to marshal DataProperties, err: %v", common.SafeErrorSummary(err))
		return err
	}
	// 2.2 Serialize logical properties.
	logicPropertiesBytes, err := sonic.Marshal(objectType.LogicProperties)
	if err != nil {
		logger.Errorf("Failed to marshal LogicProperties, err: %v", common.SafeErrorSummary(err))
		return err
	}
	// 2.3 Serialize the primary key array.
	primaryKeysBytes, err := sonic.Marshal(objectType.PrimaryKeys)
	if err != nil {
		logger.Errorf("Failed to marshal PrimaryKeys, err: %v", common.SafeErrorSummary(err))
		return err
	}

	data := map[string]any{
		"f_name":             objectType.OTName,
		"f_tags":             tagsStr,
		"f_comment":          objectType.Comment,
		"f_icon":             objectType.Icon,
		"f_color":            objectType.Color,
		"f_bkn_raw_content":  objectType.BKNRawContent,
		"f_data_source":      dataSourceBytes,
		"f_data_properties":  dataPropertiesBytes,
		"f_logic_properties": logicPropertiesBytes,
		"f_primary_keys":     primaryKeysBytes,
		"f_display_key":      objectType.DisplayKey,
		"f_incremental_key":  objectType.IncrementalKey,
		"f_updater":          objectType.Updater.ID,
		"f_updater_type":     objectType.Updater.Type,
		"f_update_time":      objectType.UpdateTime,
	}
	sqlStr, vals, err := sq.Update(OT_TABLE_NAME).
		SetMap(data).
		Where(sq.Eq{"f_id": objectType.OTID}).
		Where(sq.Eq{"f_kn_id": objectType.KNID}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of update object type by object type id, error", err)
		return err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var ret sql.Result
	if tx != nil {
		ret, err = tx.Exec(sqlStr, vals...)
	} else {
		ret, err = ota.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		common.LogSafeError(ctx, "update object type error", err)
		return err
	}

	// Number of rows affected by the SQL statement.
	RowsAffected, err := ret.RowsAffected()
	if err != nil {
		common.LogSafeError(ctx, "Get RowsAffected error", err)
		return err
	}

	if RowsAffected != 1 {
		// Do not return an error when affected rows are not one because the update has occurred.
		otellog.LogWarn(ctx, fmt.Sprintf("Update object type affected unexpected row count: object_type_id=%s, rows=%d",
			objectType.OTID, RowsAffected))
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ota *objectTypeAccess) UpdateObjectTypes(ctx context.Context, tx *sql.Tx,
	objectTypes []*interfaces.ObjectType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "UpdateObjectTypes")
	defer span.End()
	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
		attr.Int("object_type_count", len(objectTypes)),
	)

	if len(objectTypes) == 0 {
		return nil
	}
	knID, branch := objectTypes[0].KNID, objectTypes[0].Branch
	for _, objectType := range objectTypes {
		if objectType.KNID != knID || objectType.Branch != branch {
			return fmt.Errorf("batch update object types must have one knowledge network and branch")
		}
	}

	columns := []string{
		"f_name", "f_tags", "f_comment", "f_icon", "f_color", "f_bkn_raw_content",
		"f_data_source", "f_data_properties", "f_logic_properties", "f_primary_keys",
		"f_display_key", "f_incremental_key", "f_updater", "f_updater_type", "f_update_time",
	}
	for start := 0; start < len(objectTypes); {
		end := start
		seenIDs := make(map[string]struct{}, min(objectTypeUpdateBatchSize, len(objectTypes)-start))
		for end < len(objectTypes) && end-start < objectTypeUpdateBatchSize {
			if _, duplicate := seenIDs[objectTypes[end].OTID]; duplicate {
				break
			}
			seenIDs[objectTypes[end].OTID] = struct{}{}
			end++
		}
		batch := objectTypes[start:end]
		serialized := make([][]any, 0, len(batch))
		for _, objectType := range batch {
			values, err := objectTypeUpdateValues(objectType)
			if err != nil {
				common.LogSafeError(ctx, "Failed to marshal object type for batch update", err)
				return err
			}
			serialized = append(serialized, values)
		}

		var statement strings.Builder
		args := make([]any, 0, len(columns)*len(batch)*2+len(batch)+2)
		statement.WriteString("UPDATE ")
		statement.WriteString(OT_TABLE_NAME)
		statement.WriteString(" SET ")
		for columnIndex, column := range columns {
			if columnIndex > 0 {
				statement.WriteString(", ")
			}
			statement.WriteString(column)
			statement.WriteString(" = CASE f_id")
			for rowIndex, objectType := range batch {
				statement.WriteString(" WHEN ? THEN ?")
				args = append(args, objectType.OTID, serialized[rowIndex][columnIndex])
			}
			statement.WriteString(" ELSE ")
			statement.WriteString(column)
			statement.WriteString(" END")
		}
		statement.WriteString(" WHERE f_kn_id = ? AND f_branch = ? AND f_id IN (")
		args = append(args, knID, branch)
		for index, objectType := range batch {
			if index > 0 {
				statement.WriteByte(',')
			}
			statement.WriteByte('?')
			args = append(args, objectType.OTID)
		}
		statement.WriteByte(')')

		sqlStr := statement.String()
		otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(args)))
		var err error
		if tx != nil {
			_, err = tx.Exec(sqlStr, args...)
		} else {
			_, err = ota.db.Exec(sqlStr, args...)
		}
		if err != nil {
			common.LogSafeError(ctx, "Batch update object types failed", err)
			return err
		}
		start = end
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func objectTypeUpdateValues(objectType *interfaces.ObjectType) ([]any, error) {
	dataSourceBytes, err := sonic.Marshal(objectType.DataSource)
	if err != nil {
		return nil, err
	}
	dataPropertiesBytes, err := sonic.Marshal(objectType.DataProperties)
	if err != nil {
		return nil, err
	}
	logicPropertiesBytes, err := sonic.Marshal(objectType.LogicProperties)
	if err != nil {
		return nil, err
	}
	primaryKeysBytes, err := sonic.Marshal(objectType.PrimaryKeys)
	if err != nil {
		return nil, err
	}
	return []any{
		objectType.OTName, libCommon.TagSlice2TagString(objectType.Tags), objectType.Comment,
		objectType.Icon, objectType.Color, objectType.BKNRawContent, dataSourceBytes, dataPropertiesBytes,
		logicPropertiesBytes, primaryKeysBytes, objectType.DisplayKey, objectType.IncrementalKey,
		objectType.Updater.ID, objectType.Updater.Type, objectType.UpdateTime,
	}, nil
}

func (ota *objectTypeAccess) UpdateDataProperties(ctx context.Context, tx *sql.Tx, objectType *interfaces.ObjectType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "UpdateDataProperties")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// 2.1 Serialize data properties.
	dataPropertiesBytes, err := sonic.Marshal(objectType.DataProperties)
	if err != nil {
		logger.Errorf("Failed to marshal DataProperties, err: %v", common.SafeErrorSummary(err))
		return err
	}

	data := map[string]any{
		"f_data_properties": dataPropertiesBytes,
		"f_bkn_raw_content": objectType.BKNRawContent,
		"f_updater":         objectType.Updater.ID,
		"f_updater_type":    objectType.Updater.Type,
		"f_update_time":     objectType.UpdateTime,
	}
	sqlStr, vals, err := sq.Update(OT_TABLE_NAME).
		SetMap(data).
		Where(sq.Eq{"f_id": objectType.OTID}).
		Where(sq.Eq{"f_kn_id": objectType.KNID}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of update object type by object type id, error", err)
		return err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	ret, err := tx.Exec(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "update object type error", err)
		return err
	}

	// Number of rows affected by the SQL statement.
	RowsAffected, err := ret.RowsAffected()
	if err != nil {
		common.LogSafeError(ctx, "Get RowsAffected error", err)
		return err
	}

	if RowsAffected != 1 {
		// Do not return an error when affected rows are not one because the update has occurred.
		otellog.LogWarn(ctx, fmt.Sprintf("Update object type affected unexpected row count: object_type_id=%s, rows=%d",
			objectType.OTID, RowsAffected))
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ota *objectTypeAccess) DeleteObjectTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, otIDs []string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteObjectTypesByIDs")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	if len(otIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return 0, nil
	}

	sqlStr, vals, err := sq.Delete(OT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": otIDs}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete object type by object type id, error", err)
		return 0, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var ret sql.Result
	if tx != nil {
		ret, err = tx.Exec(sqlStr, vals...)
	} else {
		ret, err = ota.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		common.LogSafeError(ctx, "Delete data error", err)
		return 0, err
	}

	// Number of rows affected by the SQL statement.
	RowsAffected, err := ret.RowsAffected()
	if err != nil {
		common.LogSafeError(ctx, "Get RowsAffected error", err)
		return 0, err
	}

	if RowsAffected != int64(len(otIDs)) {
		// Do not return an error when affected rows differ from deleted object type count because deletion has occurred.
		otellog.LogWarn(ctx, fmt.Sprintf("Delete object types affected unexpected row count: requested_count=%d, rows=%d",
			len(otIDs), RowsAffected))
	}

	logger.Infof("RowsAffected: %d", RowsAffected)
	span.SetStatus(codes.Ok, "")
	return RowsAffected, nil
}

func (ota *objectTypeAccess) DeleteObjectTypeStatusByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, otIDs []string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteObjectTypeStatusByIDs")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	if len(otIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return 0, nil
	}

	sqlStr, vals, err := sq.Delete(OT_STATUS_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": otIDs}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete object type status by object type id, error", err)
		return 0, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var ret sql.Result
	if tx != nil {
		ret, err = tx.Exec(sqlStr, vals...)
	} else {
		ret, err = ota.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		common.LogSafeError(ctx, "Delete data error", err)
		return 0, err
	}

	// Number of rows affected by the SQL statement.
	RowsAffected, err := ret.RowsAffected()
	if err != nil {
		common.LogSafeError(ctx, "Get RowsAffected error", err)
		return 0, err
	}

	if RowsAffected != int64(len(otIDs)) {
		// Do not return an error when affected rows differ from deleted object type count because deletion has occurred.
		otellog.LogWarn(ctx, fmt.Sprintf("Delete object types affected unexpected row count: requested_count=%d, rows=%d",
			len(otIDs), RowsAffected))
	}

	logger.Infof("RowsAffected: %d", RowsAffected)
	span.SetStatus(codes.Ok, "")
	return RowsAffected, nil
}

func (ota *objectTypeAccess) DeleteObjectTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteObjectTypesByKnID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	sqlStr, vals, err := sq.Delete(OT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete object type by object type id, error", err)
		return 0, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var ret sql.Result
	if tx != nil {
		ret, err = tx.Exec(sqlStr, vals...)
	} else {
		ret, err = ota.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		common.LogSafeError(ctx, "Delete data error", err)
		return 0, err
	}

	// Number of rows affected by the SQL statement.
	RowsAffected, err := ret.RowsAffected()
	if err != nil {
		common.LogSafeError(ctx, "Get RowsAffected error", err)
		return 0, err
	}

	logger.Infof("RowsAffected: %d", RowsAffected)
	span.SetStatus(codes.Ok, "")
	return RowsAffected, nil
}

func (ota *objectTypeAccess) DeleteObjectTypeStatusByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteObjectTypeStatusByKnID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	sqlStr, vals, err := sq.Delete(OT_STATUS_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete object type status by object type id, error", err)
		return 0, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var ret sql.Result
	if tx != nil {
		ret, err = tx.Exec(sqlStr, vals...)
	} else {
		ret, err = ota.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		common.LogSafeError(ctx, "Delete data error", err)
		return 0, err
	}

	// Number of rows affected by the SQL statement.
	RowsAffected, err := ret.RowsAffected()
	if err != nil {
		common.LogSafeError(ctx, "Get RowsAffected error", err)
		return 0, err
	}

	logger.Infof("RowsAffected: %d", RowsAffected)
	span.SetStatus(codes.Ok, "")
	return RowsAffected, nil
}

func (ota *objectTypeAccess) GetObjectTypeIDsByKnID(ctx context.Context, knID string, branch string) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetObjectTypeIDsByKnID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	sqlStr, vals, err := sq.Select(
		"f_id",
	).From(OT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select object type ids by kn_id, error", err)
		return nil, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := ota.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	otIDs := []string{}
	for rows.Next() {
		var otID string
		err := rows.Scan(
			&otID,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return nil, err
		}

		otIDs = append(otIDs, otID)
	}

	span.SetStatus(codes.Ok, "")
	return otIDs, nil
}

func (ota *objectTypeAccess) UpdateObjectTypeStatus(ctx context.Context, tx *sql.Tx, knID string, branch string, otID string, otStatus interfaces.ObjectTypeStatus) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "UpdateObjectTypeStatus")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Update.
	sqlStr, vals, err := sq.Update(OT_STATUS_TABLE_NAME).
		Set("f_incremental_key", otStatus.IncrementalKey).
		Set("f_incremental_value", otStatus.IncrementalValue).
		Set("f_index", otStatus.Index).
		Set("f_index_available", otStatus.IndexAvailable).
		Set("f_doc_count", otStatus.DocCount).
		Set("f_storage_size", otStatus.StorageSize).
		Set("f_update_time", otStatus.UpdateTime).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": otID}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of update object type index, error", err)
		return err
	}

	// Execute the update.
	if tx != nil {
		_, err = tx.Exec(sqlStr, vals...)
	} else {
		_, err = ota.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		common.LogSafeError(ctx, "Failed to exec the sql of update object type index, error", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// Build SQL filter conditions.
func processQueryCondition(query interfaces.ObjectTypesQueryParams, subBuilder sq.SelectBuilder) sq.SelectBuilder {
	if query.NamePattern != "" {
		// Fuzzy-match name or ID; either match is sufficient.
		subBuilder = subBuilder.Where(sq.Expr("(instr(ot.f_name, ?) > 0 OR instr(ot.f_id, ?) > 0)", query.NamePattern, query.NamePattern))
	}

	if query.Tag != "" {
		subBuilder = subBuilder.Where(sq.Expr("instr(ot.f_tags, ?) > 0", `"`+query.Tag+`"`))
	}

	if query.KNID != "" {
		subBuilder = subBuilder.Where(sq.Eq{"ot.f_kn_id": query.KNID})
	}

	if query.Branch != "" {
		subBuilder = subBuilder.Where(sq.Eq{"ot.f_branch": query.Branch})
	} else {
		// Query business knowledge networks on the main branch.
		subBuilder = subBuilder.Where(sq.Eq{"ot.f_branch": interfaces.MAIN_BRANCH})
	}

	if query.OTIDS != nil {
		subBuilder = subBuilder.Where(sq.Eq{"ot.f_id": query.OTIDS})
	}

	return subBuilder
}

func (ota *objectTypeAccess) GetAllObjectTypesByKnID(ctx context.Context, knID string, branch string) (map[string]*interfaces.ObjectType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetAllObjectTypesByKnID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	sqlStr, vals, err := sq.Select(
		"f_id",
		"f_name",
		"f_tags",
		"f_comment",
		"f_icon",
		"f_color",
		"f_bkn_raw_content",
		"f_kn_id",
		"f_branch",
		"f_data_source",
		"f_data_properties",
		"f_logic_properties",
		"f_primary_keys",
		"f_display_key",
		"f_incremental_key",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	).From(OT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select object types by kn_id, error", err)
		return nil, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := ota.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return map[string]*interfaces.ObjectType{}, err
	}
	defer func() { _ = rows.Close() }()

	objectTypes := make(map[string]*interfaces.ObjectType)
	for rows.Next() {
		objectType := interfaces.ObjectType{
			ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE,
		}
		tagsStr := ""
		var (
			dataSourceBytes      []byte
			dataPropertiesBytes  []byte
			logicPropertiesBytes []byte
			primaryKeysBytes     []byte
		)
		err := rows.Scan(
			&objectType.OTID,
			&objectType.OTName,
			&tagsStr,
			&objectType.Comment,
			&objectType.Icon,
			&objectType.Color,
			&objectType.BKNRawContent,
			&objectType.KNID,
			&objectType.Branch,
			&dataSourceBytes,
			&dataPropertiesBytes,
			&logicPropertiesBytes,
			&primaryKeysBytes,
			&objectType.DisplayKey,
			&objectType.IncrementalKey,
			&objectType.Creator.ID,
			&objectType.Creator.Type,
			&objectType.CreateTime,
			&objectType.Updater.ID,
			&objectType.Updater.Type,
			&objectType.UpdateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return map[string]*interfaces.ObjectType{}, err
		}

		// Convert a tag string to an array.
		objectType.Tags = libCommon.TagString2TagSlice(tagsStr)

		// 2.0 Deserialize the data source.
		err = common.UnmarshalStoredJSON(dataSourceBytes, &objectType.DataSource)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal dataSource after getting object type, err", err)
			return map[string]*interfaces.ObjectType{}, err
		}

		// 2.1 Deserialize data properties.
		err = common.UnmarshalStoredJSON(dataPropertiesBytes, &objectType.DataProperties)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal dataProperties after getting object type, err", err)
			return map[string]*interfaces.ObjectType{}, err
		}

		// 2.2 Deserialize logical properties.
		err = common.UnmarshalStoredJSON(logicPropertiesBytes, &objectType.LogicProperties)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal logicProperties after getting object type, err", err)
			return map[string]*interfaces.ObjectType{}, err
		}

		// 2.3 Deserialize primary keys.
		err = common.UnmarshalStoredJSON(primaryKeysBytes, &objectType.PrimaryKeys)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal primaryKeys after getting object type, err", err)
			return map[string]*interfaces.ObjectType{}, err
		}

		objectTypes[objectType.OTID] = &objectType
	}

	span.SetStatus(codes.Ok, "")
	return objectTypes, nil
}
