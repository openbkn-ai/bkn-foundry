// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package relation_type

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

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	RT_TABLE_NAME                      = "t_relation_type"
	relationTypeIdentityQueryBatchSize = 500
	relationTypeInsertBatchSize        = 200
	relationTypeUpdateBatchSize        = 200
)

var (
	rtAccessOnce sync.Once
	rtAccess     interfaces.RelationTypeAccess
)

type relationTypeAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

func NewRelationTypeAccess(appSetting *common.AppSetting) interfaces.RelationTypeAccess {
	rtAccessOnce.Do(func() {
		rtAccess = &relationTypeAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return rtAccess
}

// Get relation type existence by ID.
func (rta *relationTypeAccess) CheckRelationTypeExistByID(ctx context.Context, knID string, branch string, rtID string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CheckRelationTypeExistByID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	sqlStr, vals, err := sq.Select(
		"f_name").
		From(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": rtID}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of get relation type id by f_id, error", err)
		return "", false, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var name string
	err = rta.db.QueryRow(sqlStr, vals...).Scan(&name)
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

func (rta *relationTypeAccess) GetRelationTypeIDsByIDs(ctx context.Context, knID string,
	branch string, rtIDs []string) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetRelationTypeIDsByIDs")
	defer span.End()
	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
		attr.Int("id_count", len(rtIDs)),
	)

	result := make([]string, 0)
	for start := 0; start < len(rtIDs); start += relationTypeIdentityQueryBatchSize {
		end := min(start+relationTypeIdentityQueryBatchSize, len(rtIDs))
		sqlStr, vals, err := sq.Select("f_id").From(RT_TABLE_NAME).
			Where(sq.Eq{"f_kn_id": knID}).
			Where(sq.Eq{"f_branch": branch}).
			Where(sq.Eq{"f_id": rtIDs[start:end]}).
			ToSql()
		if err != nil {
			common.LogSafeError(ctx, "Failed to build relation type identity query", err)
			return nil, err
		}
		otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))
		rows, err := rta.db.Query(sqlStr, vals...)
		if err != nil {
			common.LogSafeError(ctx, "Query relation type identities failed", err)
			return nil, err
		}
		for rows.Next() {
			var rtID string
			if err = rows.Scan(&rtID); err != nil {
				_ = rows.Close()
				common.LogSafeError(ctx, "Scan relation type identity failed", err)
				return nil, err
			}
			result = append(result, rtID)
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			common.LogSafeError(ctx, "Iterate relation type identities failed", err)
			return nil, err
		}
		_ = rows.Close()
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// Create a relation type.
func (rta *relationTypeAccess) CreateRelationType(ctx context.Context, tx *sql.Tx, relationType *interfaces.RelationType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateRelationType")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Convert tags to a string.
	tagsStr := libCommon.TagSlice2TagString(relationType.Tags)

	// 2.0 Serialize the data source.
	mappingRulesBytes, err := sonic.Marshal(relationType.MappingRules)
	if err != nil {
		logger.Errorf("Failed to marshal MappingRules, err: %v", common.SafeErrorSummary(err))
		return err
	}

	sqlStr, vals, err := sq.Insert(RT_TABLE_NAME).
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
			"f_source_object_type_id",
			"f_target_object_type_id",
			"f_type",
			"f_mapping_rules",
			"f_creator",
			"f_creator_type",
			"f_create_time",
			"f_updater",
			"f_updater_type",
			"f_update_time",
		).
		Values(
			relationType.RTID,
			relationType.RTName,
			tagsStr,
			relationType.Comment,
			relationType.Icon,
			relationType.Color,
			relationType.BKNRawContent,
			relationType.KNID,
			relationType.Branch,
			relationType.SourceObjectTypeID,
			relationType.TargetObjectTypeID,
			relationType.Type,
			mappingRulesBytes,
			relationType.Creator.ID,
			relationType.Creator.Type,
			relationType.CreateTime,
			relationType.Updater.ID,
			relationType.Updater.Type,
			relationType.UpdateTime).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of insert relation type, error", err)
		return err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	_, err = tx.Exec(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "Insert data error", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (rta *relationTypeAccess) CreateRelationTypes(ctx context.Context, tx *sql.Tx,
	relationTypes []*interfaces.RelationType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateRelationTypes")
	defer span.End()
	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
		attr.Int("relation_type_count", len(relationTypes)),
	)

	for start := 0; start < len(relationTypes); start += relationTypeInsertBatchSize {
		end := min(start+relationTypeInsertBatchSize, len(relationTypes))
		builder := sq.Insert(RT_TABLE_NAME).Columns(
			"f_id", "f_name", "f_tags", "f_comment", "f_icon", "f_color", "f_bkn_raw_content",
			"f_kn_id", "f_branch", "f_source_object_type_id", "f_target_object_type_id", "f_type",
			"f_mapping_rules", "f_creator", "f_creator_type", "f_create_time", "f_updater",
			"f_updater_type", "f_update_time",
		)
		for _, relationType := range relationTypes[start:end] {
			values, err := relationTypeInsertValues(relationType)
			if err != nil {
				common.LogSafeError(ctx, "Failed to marshal relation type for batch insert", err)
				return err
			}
			builder = builder.Values(values...)
		}
		sqlStr, vals, err := builder.ToSql()
		if err != nil {
			common.LogSafeError(ctx, "Failed to build the sql of batch insert relation types", err)
			return err
		}
		otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))
		if _, err = tx.Exec(sqlStr, vals...); err != nil {
			common.LogSafeError(ctx, "Batch insert relation types failed", err)
			return err
		}
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func relationTypeInsertValues(relationType *interfaces.RelationType) ([]any, error) {
	mappingRulesBytes, err := sonic.Marshal(relationType.MappingRules)
	if err != nil {
		return nil, err
	}
	return []any{
		relationType.RTID, relationType.RTName, libCommon.TagSlice2TagString(relationType.Tags),
		relationType.Comment, relationType.Icon, relationType.Color, relationType.BKNRawContent,
		relationType.KNID, relationType.Branch, relationType.SourceObjectTypeID,
		relationType.TargetObjectTypeID, relationType.Type, mappingRulesBytes, relationType.Creator.ID,
		relationType.Creator.Type, relationType.CreateTime, relationType.Updater.ID,
		relationType.Updater.Type, relationType.UpdateTime,
	}, nil
}

// Query current relation types on the main branch.
func (rta *relationTypeAccess) ListRelationTypes(ctx context.Context, query interfaces.RelationTypesQueryParams) ([]*interfaces.RelationType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListRelationTypes")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	subBuilder := sq.Select(
		"f_id",
		"f_name",
		"f_tags",
		"f_comment",
		"f_icon",
		"f_color",
		"f_bkn_raw_content",
		"f_kn_id",
		"f_branch",
		"f_source_object_type_id",
		"f_target_object_type_id",
		"f_type",
		"f_mapping_rules",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time").
		From(RT_TABLE_NAME)

	builder := processQueryCondition(query, subBuilder)

	// Sort.
	if query.Sort != "" {
		orderBy, err := common.SafeOrderBy(query.Sort, query.Direction)
		if err != nil {
			return nil, err
		}
		builder = builder.OrderBy(orderBy)
	}
	if query.Limit > 0 {
		builder = builder.Limit(uint64(query.Limit))
		if query.Offset > 0 {
			builder = builder.Offset(uint64(query.Offset))
		}
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select relation types, error", err)
		return []*interfaces.RelationType{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := rta.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []*interfaces.RelationType{}, err
	}
	defer func() { _ = rows.Close() }()

	relationTypes := make([]*interfaces.RelationType, 0)
	for rows.Next() {
		relationType := interfaces.RelationType{
			ModuleType: interfaces.MODULE_TYPE_RELATION_TYPE,
		}
		tagsStr := ""
		var mappingRulesBytes []byte
		err := rows.Scan(
			&relationType.RTID,
			&relationType.RTName,
			&tagsStr,
			&relationType.Comment,
			&relationType.Icon,
			&relationType.Color,
			&relationType.BKNRawContent,
			&relationType.KNID,
			&relationType.Branch,
			&relationType.SourceObjectTypeID,
			&relationType.TargetObjectTypeID,
			&relationType.Type,
			&mappingRulesBytes,
			&relationType.Creator.ID,
			&relationType.Creator.Type,
			&relationType.CreateTime,
			&relationType.Updater.ID,
			&relationType.Updater.Type,
			&relationType.UpdateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []*interfaces.RelationType{}, err
		}

		// Convert a tag string to an array.
		relationType.Tags = libCommon.TagString2TagSlice(tagsStr)

		// 2.0 Deserialize mapping rules.
		if relationType.Type == interfaces.RELATION_TYPE_DIRECT {
			var mappings []interfaces.Mapping
			err = common.UnmarshalStoredJSON(mappingRulesBytes, &mappings)
			if err != nil {
				common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
				return []*interfaces.RelationType{}, err
			}
			relationType.MappingRules = mappings
		}
		if relationType.Type == interfaces.RELATION_TYPE_INDIRECT {
			var mappings interfaces.InDirectMapping
			err = common.UnmarshalStoredJSON(mappingRulesBytes, &mappings)
			if err != nil {
				common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
				return []*interfaces.RelationType{}, err
			}
			relationType.MappingRules = &mappings
		}
		if relationType.Type == interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN {
			var fcj interfaces.FilteredCrossJoinMapping
			err = common.UnmarshalStoredJSON(mappingRulesBytes, &fcj)
			if err != nil {
				common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
				return []*interfaces.RelationType{}, err
			}
			relationType.MappingRules = &fcj
		}

		relationTypes = append(relationTypes, &relationType)
	}

	span.SetStatus(codes.Ok, "")
	return relationTypes, nil
}

// ListRelationTypeSummaries reads the documented list projection without raw
// model content or other detail-only fields.
func (rta *relationTypeAccess) ListRelationTypeSummaries(ctx context.Context,
	query interfaces.RelationTypesQueryParams) ([]*interfaces.RelationType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListRelationTypeSummaries")
	defer span.End()

	builder := processQueryCondition(query, sq.Select(
		"f_id",
		"f_name",
		"f_tags",
		"f_comment",
		"f_icon",
		"f_color",
		"f_kn_id",
		"f_branch",
		"f_source_object_type_id",
		"f_target_object_type_id",
		"f_type",
		"f_mapping_rules",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	).From(RT_TABLE_NAME))
	if query.Sort != "" {
		orderBy, err := common.SafeOrderBy(query.Sort, query.Direction)
		if err != nil {
			return nil, err
		}
		builder = builder.OrderBy(orderBy, "f_id ASC")
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
	rows, err := rta.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]*interfaces.RelationType, 0)
	for rows.Next() {
		item := &interfaces.RelationType{ModuleType: interfaces.MODULE_TYPE_RELATION_TYPE}
		tags := ""
		var mappingRulesBytes []byte
		if err := rows.Scan(
			&item.RTID,
			&item.RTName,
			&tags,
			&item.Comment,
			&item.Icon,
			&item.Color,
			&item.KNID,
			&item.Branch,
			&item.SourceObjectTypeID,
			&item.TargetObjectTypeID,
			&item.Type,
			&mappingRulesBytes,
			&item.Creator.ID,
			&item.Creator.Type,
			&item.CreateTime,
			&item.Updater.ID,
			&item.Updater.Type,
			&item.UpdateTime,
		); err != nil {
			return nil, err
		}
		item.Tags = libCommon.TagString2TagSlice(tags)
		switch item.Type {
		case interfaces.RELATION_TYPE_DIRECT:
			var mappings []interfaces.Mapping
			if err := common.UnmarshalStoredJSON(mappingRulesBytes, &mappings); err != nil {
				return nil, err
			}
			item.MappingRules = mappings
		case interfaces.RELATION_TYPE_INDIRECT:
			var mappings interfaces.InDirectMapping
			if err := common.UnmarshalStoredJSON(mappingRulesBytes, &mappings); err != nil {
				return nil, err
			}
			item.MappingRules = &mappings
		case interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN:
			var mapping interfaces.FilteredCrossJoinMapping
			if err := common.UnmarshalStoredJSON(mappingRulesBytes, &mapping); err != nil {
				return nil, err
			}
			item.MappingRules = &mapping
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

func (rta *relationTypeAccess) GetRelationTypesTotal(ctx context.Context, query interfaces.RelationTypesQueryParams) (int, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetRelationTypesTotal")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	subBuilder := sq.Select("COUNT(f_id)").From(RT_TABLE_NAME)
	builder := processQueryCondition(query, subBuilder)

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select relation types total, error", err)
		return 0, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	total := 0
	err = rta.db.QueryRow(sqlStr, vals...).Scan(&total)
	if err != nil {
		common.LogSafeError(ctx, "Get relation type total error", err)
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return total, nil
}

func (rta *relationTypeAccess) GetRelationTypeByID(ctx context.Context, knID string, branch string, rtID string) (*interfaces.RelationType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetRelationTypeByID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

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
		"f_source_object_type_id",
		"f_target_object_type_id",
		"f_type",
		"f_mapping_rules",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	).From(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": rtID}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select relation type by id, error", err)
		return nil, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	relationType := interfaces.RelationType{
		ModuleType: interfaces.MODULE_TYPE_RELATION_TYPE,
	}
	tagsStr := ""
	var mappingRulesBytes []byte

	row := rta.db.QueryRowContext(ctx, sqlStr, vals...)
	err = row.Scan(
		&relationType.RTID,
		&relationType.RTName,
		&tagsStr,
		&relationType.Comment,
		&relationType.Icon,
		&relationType.Color,
		&relationType.BKNRawContent,
		&relationType.KNID,
		&relationType.Branch,
		&relationType.SourceObjectTypeID,
		&relationType.TargetObjectTypeID,
		&relationType.Type,
		&mappingRulesBytes,
		&relationType.Creator.ID,
		&relationType.Creator.Type,
		&relationType.CreateTime,
		&relationType.Updater.ID,
		&relationType.Updater.Type,
		&relationType.UpdateTime,
	)
	if err != nil {
		common.LogSafeError(ctx, "Row scan error", err)
		return nil, err
	}

	// Convert a tag string to an array.
	relationType.Tags = libCommon.TagString2TagSlice(tagsStr)

	// 2.0 Deserialize mapping rules.
	if relationType.Type == interfaces.RELATION_TYPE_DIRECT {
		var mappings []interfaces.Mapping
		err = common.UnmarshalStoredJSON(mappingRulesBytes, &mappings)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
			return nil, err
		}
		relationType.MappingRules = mappings
	}
	if relationType.Type == interfaces.RELATION_TYPE_INDIRECT {
		var mappings interfaces.InDirectMapping
		err = common.UnmarshalStoredJSON(mappingRulesBytes, &mappings)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
			return nil, err
		}
		relationType.MappingRules = &mappings
	}
	if relationType.Type == interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN {
		var fcj interfaces.FilteredCrossJoinMapping
		err = common.UnmarshalStoredJSON(mappingRulesBytes, &fcj)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
			return nil, err
		}
		relationType.MappingRules = &fcj
	}

	span.SetStatus(codes.Ok, "")
	return &relationType, nil
}

func (rta *relationTypeAccess) GetRelationTypesByIDs(ctx context.Context, knID string, branch string, rtIDs []string) ([]*interfaces.RelationType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetRelationTypesByIDs")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

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
		"f_source_object_type_id",
		"f_target_object_type_id",
		"f_type",
		"f_mapping_rules",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	).From(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": rtIDs}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select relation type by id, error", err)
		return []*interfaces.RelationType{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := rta.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []*interfaces.RelationType{}, err
	}
	defer func() { _ = rows.Close() }()

	relationTypes := make([]*interfaces.RelationType, 0)
	for rows.Next() {
		relationType := interfaces.RelationType{
			ModuleType: interfaces.MODULE_TYPE_RELATION_TYPE,
		}
		tagsStr := ""
		var mappingRulesBytes []byte

		err := rows.Scan(
			&relationType.RTID,
			&relationType.RTName,
			&tagsStr,
			&relationType.Comment,
			&relationType.Icon,
			&relationType.Color,
			&relationType.BKNRawContent,
			&relationType.KNID,
			&relationType.Branch,
			&relationType.SourceObjectTypeID,
			&relationType.TargetObjectTypeID,
			&relationType.Type,
			&mappingRulesBytes,
			&relationType.Creator.ID,
			&relationType.Creator.Type,
			&relationType.CreateTime,
			&relationType.Updater.ID,
			&relationType.Updater.Type,
			&relationType.UpdateTime,
		)

		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []*interfaces.RelationType{}, err
		}

		// Convert a tag string to an array.
		relationType.Tags = libCommon.TagString2TagSlice(tagsStr)

		// 2.0 Deserialize mapping rules.
		if relationType.Type == interfaces.RELATION_TYPE_DIRECT {
			var mappings []interfaces.Mapping
			err = common.UnmarshalStoredJSON(mappingRulesBytes, &mappings)
			if err != nil {
				common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
				return []*interfaces.RelationType{}, err
			}
			relationType.MappingRules = mappings
		}
		if relationType.Type == interfaces.RELATION_TYPE_INDIRECT {
			var mappings interfaces.InDirectMapping
			err = common.UnmarshalStoredJSON(mappingRulesBytes, &mappings)
			if err != nil {
				common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
				return []*interfaces.RelationType{}, err
			}
			relationType.MappingRules = &mappings
		}
		if relationType.Type == interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN {
			var fcj interfaces.FilteredCrossJoinMapping
			err = common.UnmarshalStoredJSON(mappingRulesBytes, &fcj)
			if err != nil {
				common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
				return []*interfaces.RelationType{}, err
			}
			relationType.MappingRules = &fcj
		}

		relationTypes = append(relationTypes, &relationType)
	}

	span.SetStatus(codes.Ok, "")
	return relationTypes, nil
}

func (rta *relationTypeAccess) UpdateRelationType(ctx context.Context, tx *sql.Tx, relationType *interfaces.RelationType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "UpdateRelationType")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Convert tags to a string.
	tagsStr := libCommon.TagSlice2TagString(relationType.Tags)
	// 2.0 Serialize the data source.
	mappingRulesBytes, err := sonic.Marshal(relationType.MappingRules)
	if err != nil {
		logger.Errorf("Failed to marshal MappingRules, err: %v", common.SafeErrorSummary(err))
		return err
	}

	data := map[string]any{
		"f_name":                  relationType.RTName,
		"f_tags":                  tagsStr,
		"f_comment":               relationType.Comment,
		"f_icon":                  relationType.Icon,
		"f_color":                 relationType.Color,
		"f_bkn_raw_content":       relationType.BKNRawContent,
		"f_source_object_type_id": relationType.SourceObjectTypeID,
		"f_target_object_type_id": relationType.TargetObjectTypeID,
		"f_type":                  relationType.Type,
		"f_mapping_rules":         mappingRulesBytes,
		"f_updater":               relationType.Updater.ID,
		"f_updater_type":          relationType.Updater.Type,
		"f_update_time":           relationType.UpdateTime,
	}
	sqlStr, vals, err := sq.Update(RT_TABLE_NAME).
		SetMap(data).
		Where(sq.Eq{"f_id": relationType.RTID}).
		Where(sq.Eq{"f_kn_id": relationType.KNID}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of update relation type by relation type id, error", err)
		return err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	ret, err := tx.Exec(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "update relation type error", err)
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
		otellog.LogWarn(ctx, fmt.Sprintf("Update relation type affected unexpected row count: relation_type_id=%s, rows=%d",
			relationType.RTID, RowsAffected))
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (rta *relationTypeAccess) UpdateRelationTypes(ctx context.Context, tx *sql.Tx,
	relationTypes []*interfaces.RelationType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "UpdateRelationTypes")
	defer span.End()
	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
		attr.Int("relation_type_count", len(relationTypes)),
	)

	if len(relationTypes) == 0 {
		return nil
	}
	knID, branch := relationTypes[0].KNID, relationTypes[0].Branch
	for _, relationType := range relationTypes {
		if relationType.KNID != knID || relationType.Branch != branch {
			return fmt.Errorf("batch update relation types must have one knowledge network and branch")
		}
	}
	columns := []string{
		"f_name", "f_tags", "f_comment", "f_icon", "f_color", "f_bkn_raw_content",
		"f_source_object_type_id", "f_target_object_type_id", "f_type", "f_mapping_rules",
		"f_updater", "f_updater_type", "f_update_time",
	}
	for start := 0; start < len(relationTypes); {
		end := start
		seenIDs := make(map[string]struct{}, min(relationTypeUpdateBatchSize, len(relationTypes)-start))
		for end < len(relationTypes) && end-start < relationTypeUpdateBatchSize {
			if _, duplicate := seenIDs[relationTypes[end].RTID]; duplicate {
				break
			}
			seenIDs[relationTypes[end].RTID] = struct{}{}
			end++
		}
		batch := relationTypes[start:end]
		serialized := make([][]any, 0, len(batch))
		for _, relationType := range batch {
			values, err := relationTypeUpdateValues(relationType)
			if err != nil {
				common.LogSafeError(ctx, "Failed to marshal relation type for batch update", err)
				return err
			}
			serialized = append(serialized, values)
		}

		var statement strings.Builder
		args := make([]any, 0, len(columns)*len(batch)*2+len(batch)+2)
		statement.WriteString("UPDATE ")
		statement.WriteString(RT_TABLE_NAME)
		statement.WriteString(" SET ")
		for columnIndex, column := range columns {
			if columnIndex > 0 {
				statement.WriteString(", ")
			}
			statement.WriteString(column)
			statement.WriteString(" = CASE f_id")
			for rowIndex, relationType := range batch {
				statement.WriteString(" WHEN ? THEN ?")
				args = append(args, relationType.RTID, serialized[rowIndex][columnIndex])
			}
			statement.WriteString(" ELSE ")
			statement.WriteString(column)
			statement.WriteString(" END")
		}
		statement.WriteString(" WHERE f_kn_id = ? AND f_branch = ? AND f_id IN (")
		args = append(args, knID, branch)
		for index, relationType := range batch {
			if index > 0 {
				statement.WriteByte(',')
			}
			statement.WriteByte('?')
			args = append(args, relationType.RTID)
		}
		statement.WriteByte(')')
		sqlStr := statement.String()
		otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(args)))
		if _, err := tx.Exec(sqlStr, args...); err != nil {
			common.LogSafeError(ctx, "Batch update relation types failed", err)
			return err
		}
		start = end
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func relationTypeUpdateValues(relationType *interfaces.RelationType) ([]any, error) {
	mappingRulesBytes, err := sonic.Marshal(relationType.MappingRules)
	if err != nil {
		return nil, err
	}
	return []any{
		relationType.RTName, libCommon.TagSlice2TagString(relationType.Tags), relationType.Comment,
		relationType.Icon, relationType.Color, relationType.BKNRawContent, relationType.SourceObjectTypeID,
		relationType.TargetObjectTypeID, relationType.Type, mappingRulesBytes, relationType.Updater.ID,
		relationType.Updater.Type, relationType.UpdateTime,
	}, nil
}

func (rta *relationTypeAccess) DeleteRelationTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, rtIDs []string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteRelationTypesByIDs")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	if len(rtIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return 0, nil
	}

	sqlStr, vals, err := sq.Delete(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": rtIDs}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete relation type by relation type id, error", err)
		return 0, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	ret, err := tx.Exec(sqlStr, vals...)
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

	if RowsAffected != int64(len(rtIDs)) {
		otellog.LogWarn(ctx, fmt.Sprintf("Delete relation types affected unexpected row count: requested_count=%d, rows=%d",
			len(rtIDs), RowsAffected))
	}

	logger.Infof("RowsAffected: %d", RowsAffected)
	span.SetStatus(codes.Ok, "")
	return RowsAffected, nil
}

func (rta *relationTypeAccess) DeleteRelationTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteRelationTypesByKnID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	sqlStr, vals, err := sq.Delete(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete relation type by relation type id, error", err)
		return 0, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	ret, err := tx.Exec(sqlStr, vals...)
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

func (rta *relationTypeAccess) GetRelationTypeIDsByKnID(ctx context.Context, knID string, branch string) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetRelationTypeIDsByKnID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	sqlStr, vals, err := sq.Select(
		"f_id",
	).From(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select relation type by id, error", err)
		return nil, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := rta.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	rtIDs := []string{}
	for rows.Next() {

		var rtID string

		err := rows.Scan(
			&rtID,
		)

		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return nil, err
		}

		rtIDs = append(rtIDs, rtID)
	}

	span.SetStatus(codes.Ok, "")
	return rtIDs, nil
}

// Build SQL filter conditions.
func processQueryCondition(query interfaces.RelationTypesQueryParams, subBuilder sq.SelectBuilder) sq.SelectBuilder {
	if query.NamePattern != "" {
		// Fuzzy-match name or ID; either match is sufficient.
		subBuilder = subBuilder.Where(sq.Expr("(instr(f_name, ?) > 0 OR instr(f_id, ?) > 0)", query.NamePattern, query.NamePattern))
	}

	if query.Tag != "" {
		subBuilder = subBuilder.Where(sq.Expr("instr(f_tags, ?) > 0", `"`+query.Tag+`"`))
	}

	if query.KNID != "" {
		subBuilder = subBuilder.Where(sq.Eq{"f_kn_id": query.KNID})
	}

	if query.Branch != "" {
		subBuilder = subBuilder.Where(sq.Eq{"f_branch": query.Branch})
	} else {
		// Query business knowledge networks on the main branch.
		subBuilder = subBuilder.Where(sq.Eq{"f_branch": interfaces.MAIN_BRANCH})
	}

	if len(query.SourceObjectTypeIDs) > 0 {
		subBuilder = subBuilder.Where(sq.Eq{"f_source_object_type_id": query.SourceObjectTypeIDs})
	}

	if len(query.TargetObjectTypeIDs) > 0 {
		subBuilder = subBuilder.Where(sq.Eq{"f_target_object_type_id": query.TargetObjectTypeIDs})
	}

	if len(query.BoundObjectTypeIDs) > 0 {
		subBuilder = subBuilder.Where(sq.Or{
			sq.Eq{"f_source_object_type_id": query.BoundObjectTypeIDs},
			sq.Eq{"f_target_object_type_id": query.BoundObjectTypeIDs},
		})
	}

	if query.RTIDS != nil {
		subBuilder = subBuilder.Where(sq.Eq{"f_id": query.RTIDS})
	}
	if query.ValidAuthorizationIDsOnly {
		subBuilder = subBuilder.Where(sq.Expr(
			"f_id <> '' AND f_id = TRIM(f_id) AND instr(f_id, '/') = 0 AND instr(f_id, '*') = 0"))
	}

	return subBuilder
}

func (rta *relationTypeAccess) GetAllRelationTypesByKnID(ctx context.Context, knID string, branch string) (map[string]*interfaces.RelationType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetAllRelationTypesByKnID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

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
		"f_source_object_type_id",
		"f_target_object_type_id",
		"f_type",
		"f_mapping_rules",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time").
		From(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()

	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select relation types, error", err)
		return map[string]*interfaces.RelationType{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := rta.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return map[string]*interfaces.RelationType{}, err
	}
	defer func() { _ = rows.Close() }()

	relationTypes := make(map[string]*interfaces.RelationType)
	for rows.Next() {
		relationType := interfaces.RelationType{
			ModuleType: interfaces.MODULE_TYPE_RELATION_TYPE,
		}
		tagsStr := ""
		var mappingRulesBytes []byte
		err := rows.Scan(
			&relationType.RTID,
			&relationType.RTName,
			&tagsStr,
			&relationType.Comment,
			&relationType.Icon,
			&relationType.Color,
			&relationType.BKNRawContent,
			&relationType.KNID,
			&relationType.Branch,
			&relationType.SourceObjectTypeID,
			&relationType.TargetObjectTypeID,
			&relationType.Type,
			&mappingRulesBytes,
			&relationType.Creator.ID,
			&relationType.Creator.Type,
			&relationType.CreateTime,
			&relationType.Updater.ID,
			&relationType.Updater.Type,
			&relationType.UpdateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return map[string]*interfaces.RelationType{}, err
		}

		// Convert a tag string to an array.
		relationType.Tags = libCommon.TagString2TagSlice(tagsStr)

		// 2.0 Deserialize mapping rules.
		if relationType.Type == interfaces.RELATION_TYPE_DIRECT {
			var mappings []interfaces.Mapping
			err = common.UnmarshalStoredJSON(mappingRulesBytes, &mappings)
			if err != nil {
				common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
				return map[string]*interfaces.RelationType{}, err
			}
			relationType.MappingRules = mappings
		}
		if relationType.Type == interfaces.RELATION_TYPE_INDIRECT {
			var mappings interfaces.InDirectMapping
			err = common.UnmarshalStoredJSON(mappingRulesBytes, &mappings)
			if err != nil {
				common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
				return map[string]*interfaces.RelationType{}, err
			}
			relationType.MappingRules = &mappings
		}
		if relationType.Type == interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN {
			var fcj interfaces.FilteredCrossJoinMapping
			err = common.UnmarshalStoredJSON(mappingRulesBytes, &fcj)
			if err != nil {
				common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
				return map[string]*interfaces.RelationType{}, err
			}
			relationType.MappingRules = &fcj
		}

		relationTypes[relationType.RTID] = &relationType
	}

	span.SetStatus(codes.Ok, "")
	return relationTypes, nil
}
