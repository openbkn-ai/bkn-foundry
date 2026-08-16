// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package concept_group

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	"bkn-backend/drivenadapters/action_type"
	"bkn-backend/drivenadapters/object_type"
	"bkn-backend/drivenadapters/relation_type"
	"bkn-backend/interfaces"
)

const (
	CONCEPT_GROUP_TABLE_NAME          = "t_concept_group"
	CONCEPT_GROUP_RELATION_TABLE_NAME = "t_concept_group_relation"
)

var (
	cgAccessOnce sync.Once
	cgAccess     interfaces.ConceptGroupAccess
)

type conceptGroupAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

func NewConceptGroupAccess(appSetting *common.AppSetting) interfaces.ConceptGroupAccess {
	cgAccessOnce.Do(func() {
		cgAccess = &conceptGroupAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return cgAccess
}

// Get concept group existence by ID.
func (cga *conceptGroupAccess) CheckConceptGroupExistByID(ctx context.Context, knID string, branch string, cgID string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CheckConceptGroupExistByID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	sqlStr, vals, err := sq.Select(
		"f_name").
		From(CONCEPT_GROUP_TABLE_NAME).
		Where(sq.Eq{"f_id": cgID}).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of get concept group id by f_id, error", err)
		return "", false, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var name string
	err = cga.db.QueryRow(sqlStr, vals...).Scan(&name)
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

// Get concept group existence by name.
func (cga *conceptGroupAccess) CheckConceptGroupExistByName(ctx context.Context, knID string, branch string, name string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CheckConceptGroupExistByName")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	// Query.
	sqlStr, vals, err := sq.Select(
		"f_id").
		From(CONCEPT_GROUP_TABLE_NAME).
		Where(sq.Eq{"f_name": name}).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of get id by name, error", err)
		return "", false, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var cgID string
	err = cga.db.QueryRow(sqlStr, vals...).Scan(
		&cgID,
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
	return cgID, true, nil
}

// Create a concept group.
func (cga *conceptGroupAccess) CreateConceptGroup(ctx context.Context, tx *sql.Tx, conceptGroup *interfaces.ConceptGroup) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateConceptGroup")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Convert tags to a string.
	tagsStr := libCommon.TagSlice2TagString(conceptGroup.Tags)

	sqlStr, vals, err := sq.Insert(CONCEPT_GROUP_TABLE_NAME).
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
			"f_creator",
			"f_creator_type",
			"f_create_time",
			"f_updater",
			"f_updater_type",
			"f_update_time",
		).
		Values(
			conceptGroup.CGID,
			conceptGroup.CGName,
			tagsStr,
			conceptGroup.Comment,
			conceptGroup.Icon,
			conceptGroup.Color,
			conceptGroup.BKNRawContent,
			conceptGroup.KNID,
			conceptGroup.Branch,
			conceptGroup.Creator.ID,
			conceptGroup.Creator.Type,
			conceptGroup.CreateTime,
			conceptGroup.Updater.ID,
			conceptGroup.Updater.Type,
			conceptGroup.UpdateTime).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of insert concept group, error", err)
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

// Query current concept groups on the main branch.
func (cga *conceptGroupAccess) ListConceptGroups(ctx context.Context, query interfaces.ConceptGroupsQueryParams) ([]*interfaces.ConceptGroup, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListConceptGroups")
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
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time").
		From(CONCEPT_GROUP_TABLE_NAME)

	builder := processQueryCondition(query, subBuilder)

	// Sort.
	if query.Sort != "" {
		builder = builder.OrderBy(fmt.Sprintf("%s %s", query.Sort, query.Direction))
	}
	if query.Limit > 0 {
		builder = builder.Limit(uint64(query.Limit))
		if query.Offset > 0 {
			builder = builder.Offset(uint64(query.Offset))
		}
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select concept groups, error", err)
		return []*interfaces.ConceptGroup{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := cga.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []*interfaces.ConceptGroup{}, err
	}
	defer func() { _ = rows.Close() }()

	conceptGroups := make([]*interfaces.ConceptGroup, 0)

	for rows.Next() {
		conceptGroup := interfaces.ConceptGroup{
			ModuleType: interfaces.MODULE_TYPE_CONCEPT_GROUP,
		}
		tagsStr := ""
		err := rows.Scan(
			&conceptGroup.CGID,
			&conceptGroup.CGName,
			&tagsStr,
			&conceptGroup.Comment,
			&conceptGroup.Icon,
			&conceptGroup.Color,
			&conceptGroup.BKNRawContent,
			&conceptGroup.KNID,
			&conceptGroup.Branch,
			&conceptGroup.Creator.ID,
			&conceptGroup.Creator.Type,
			&conceptGroup.CreateTime,
			&conceptGroup.Updater.ID,
			&conceptGroup.Updater.Type,
			&conceptGroup.UpdateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []*interfaces.ConceptGroup{}, err
		}

		// Convert a tag string to an array.
		conceptGroup.Tags = libCommon.TagString2TagSlice(tagsStr)

		conceptGroups = append(conceptGroups, &conceptGroup)
	}

	span.SetStatus(codes.Ok, "")
	return conceptGroups, nil
}

// Get concept groups in bulk.
func (cga *conceptGroupAccess) GetConceptGroupsByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, cgIDs []string) ([]*interfaces.ConceptGroup, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetConceptGroupsByIDs")
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
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	).From(CONCEPT_GROUP_TABLE_NAME).
		Where(sq.Eq{"f_id": cgIDs}).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select concept group by id, error", err)
		return []*interfaces.ConceptGroup{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := tx.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []*interfaces.ConceptGroup{}, err
	}
	defer func() { _ = rows.Close() }()

	conceptGroups := make([]*interfaces.ConceptGroup, 0)
	for rows.Next() {
		conceptGroup := interfaces.ConceptGroup{
			ModuleType: interfaces.MODULE_TYPE_CONCEPT_GROUP,
		}
		tagsStr := ""
		err := rows.Scan(
			&conceptGroup.CGID,
			&conceptGroup.CGName,
			&tagsStr,
			&conceptGroup.Comment,
			&conceptGroup.Icon,
			&conceptGroup.Color,
			&conceptGroup.BKNRawContent,
			&conceptGroup.KNID,
			&conceptGroup.Branch,
			&conceptGroup.Creator.ID,
			&conceptGroup.Creator.Type,
			&conceptGroup.CreateTime,
			&conceptGroup.Updater.ID,
			&conceptGroup.Updater.Type,
			&conceptGroup.UpdateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []*interfaces.ConceptGroup{}, err
		}

		// Convert a tag string to an array.
		conceptGroup.Tags = libCommon.TagString2TagSlice(tagsStr)

		conceptGroups = append(conceptGroups, &conceptGroup)
	}

	span.SetStatus(codes.Ok, "")
	return conceptGroups, nil
}

// Get total concept group count.
func (cga *conceptGroupAccess) GetConceptGroupsTotal(ctx context.Context, query interfaces.ConceptGroupsQueryParams) (int, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetConceptGroupsTotal")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	subBuilder := sq.Select("COUNT(f_id)").From(CONCEPT_GROUP_TABLE_NAME)
	builder := processQueryCondition(query, subBuilder)
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select concept groups total, error", err)
		return 0, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	total := 0
	err = cga.db.QueryRow(sqlStr, vals...).Scan(&total)
	if err != nil {
		common.LogSafeError(ctx, "Get concept group total error", err)
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return total, nil
}

func (cga *conceptGroupAccess) GetConceptGroupByID(ctx context.Context, knID string, branch string, cgID string) (*interfaces.ConceptGroup, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetConceptGroupByID")
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
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	).From(CONCEPT_GROUP_TABLE_NAME).
		Where(sq.Eq{"f_id": cgID}).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select concept group by id, error", err)
		return &interfaces.ConceptGroup{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	tagsStr := ""
	conceptGroup := &interfaces.ConceptGroup{
		ModuleType: interfaces.MODULE_TYPE_CONCEPT_GROUP,
	}
	err = cga.db.QueryRow(sqlStr, vals...).Scan(
		&conceptGroup.CGID,
		&conceptGroup.CGName,
		&tagsStr,
		&conceptGroup.Comment,
		&conceptGroup.Icon,
		&conceptGroup.Color,
		&conceptGroup.BKNRawContent,
		&conceptGroup.KNID,
		&conceptGroup.Branch,
		&conceptGroup.Creator.ID,
		&conceptGroup.Creator.Type,
		&conceptGroup.CreateTime,
		&conceptGroup.Updater.ID,
		&conceptGroup.Updater.Type,
		&conceptGroup.UpdateTime,
	)
	if err == sql.ErrNoRows {
		span.SetAttributes(attr.Key("no_rows").Bool(true))
		span.SetStatus(codes.Ok, "")
		return nil, nil
	} else if err != nil {
		common.LogSafeError(ctx, "Get concept group by id error", err)
		return nil, err
	}

	// Convert a tag string to an array.
	conceptGroup.Tags = libCommon.TagString2TagSlice(tagsStr)

	span.SetStatus(codes.Ok, "")
	return conceptGroup, nil
}

func (cga *conceptGroupAccess) UpdateConceptGroup(ctx context.Context, tx *sql.Tx, conceptGroup *interfaces.ConceptGroup) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "UpdateConceptGroup")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Convert tags to a string.
	tagsStr := libCommon.TagSlice2TagString(conceptGroup.Tags)

	data := map[string]any{
		"f_name":            conceptGroup.CGName,
		"f_tags":            tagsStr,
		"f_comment":         conceptGroup.Comment,
		"f_icon":            conceptGroup.Icon,
		"f_color":           conceptGroup.Color,
		"f_bkn_raw_content": conceptGroup.BKNRawContent,
		"f_updater":         conceptGroup.Updater.ID,
		"f_updater_type":    conceptGroup.Updater.Type,
		"f_update_time":     conceptGroup.UpdateTime,
	}
	sqlStr, vals, err := sq.Update(CONCEPT_GROUP_TABLE_NAME).
		SetMap(data).
		Where(sq.Eq{"f_id": conceptGroup.CGID}).
		Where(sq.Eq{"f_kn_id": conceptGroup.KNID}).
		Where(sq.Eq{"f_branch": conceptGroup.Branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of update concept group by concept group id, error", err)
		return err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	ret, err := tx.Exec(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "update concept group error", err)
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
		otellog.LogWarn(ctx, fmt.Sprintf("Update concept group affected unexpected row count: concept_group_id=%s, rows=%d",
			conceptGroup.CGID, RowsAffected))
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (cga *conceptGroupAccess) UpdateConceptGroupDetail(ctx context.Context, knID string, branch string, cgID string, detail string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "UpdateConceptGroupDetail")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	data := map[string]any{
		"f_bkn_raw_content": detail,
	}
	sqlStr, vals, err := sq.Update(CONCEPT_GROUP_TABLE_NAME).
		SetMap(data).
		Where(sq.Eq{"f_id": cgID}).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of update concept group detail by concept group id, error", err)
		return err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	ret, err := cga.db.Exec(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "update concept group detail error", err)
		return err
	}

	// Number of rows affected by the SQL statement.
	RowsAffected, err := ret.RowsAffected()
	if err != nil {
		common.LogSafeError(ctx, "Get RowsAffected error", err)
		return err
	}

	if RowsAffected != 1 {
		otellog.LogWarn(ctx, fmt.Sprintf("Update concept group detail %s RowsAffected not equal 1, RowsAffected is %d",
			knID, RowsAffected))
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (cga *conceptGroupAccess) DeleteConceptGroupByID(ctx context.Context, tx *sql.Tx, knID string, branch string, cgID string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteConceptGroupByID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	if cgID == "" {
		span.SetStatus(codes.Ok, "")
		return 0, nil
	}

	sqlStr, vals, err := sq.Delete(CONCEPT_GROUP_TABLE_NAME).
		Where(sq.Eq{"f_id": cgID}).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete concept group by concept group id, error", err)
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

func (cga *conceptGroupAccess) DeleteConceptGroupsByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteConceptGroupByKnID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	sqlStr, vals, err := sq.Delete(CONCEPT_GROUP_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete concept group by concept group id, error", err)
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

func (cga *conceptGroupAccess) DeleteConceptGroupRelationsByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteConceptGroupRelationsByKnID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	sqlStr, vals, err := sq.Delete(CONCEPT_GROUP_RELATION_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete concept group relation by kn_id, error", err)
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

func (cga *conceptGroupAccess) GetConceptGroupIDsByKnID(ctx context.Context, knID string, branch string) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetConceptGroupIDsByKnID")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	sqlStr, vals, err := sq.Select(
		"f_id",
	).From(CONCEPT_GROUP_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select concept group ids by kn_id, error", err)
		return nil, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := cga.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cgIDs := []string{}
	for rows.Next() {

		var atID string
		err := rows.Scan(
			&atID,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return nil, err
		}

		cgIDs = append(cgIDs, atID)
	}

	span.SetStatus(codes.Ok, "")
	return cgIDs, nil
}

// Build SQL filter conditions.
func processQueryCondition(query interfaces.ConceptGroupsQueryParams, subBuilder sq.SelectBuilder) sq.SelectBuilder {
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

	if len(query.CGIDs) > 0 {
		subBuilder = subBuilder.Where(sq.Eq{"f_id": query.CGIDs})
	}

	return subBuilder
}

// Query current concept groups on the main branch.
func (cga *conceptGroupAccess) GetAllConceptGroupsByKnID(ctx context.Context, knID string, branch string) (map[string]*interfaces.ConceptGroup, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetAllConceptGroupsByKnID")
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
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time").
		From(CONCEPT_GROUP_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()

	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select concept groups, error", err)
		return map[string]*interfaces.ConceptGroup{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := cga.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return map[string]*interfaces.ConceptGroup{}, err
	}
	defer func() { _ = rows.Close() }()

	conceptGroups := make(map[string]*interfaces.ConceptGroup)
	for rows.Next() {
		conceptGroup := interfaces.ConceptGroup{
			ModuleType: interfaces.MODULE_TYPE_CONCEPT_GROUP,
		}
		tagsStr := ""

		err := rows.Scan(
			&conceptGroup.CGID,
			&conceptGroup.CGName,
			&tagsStr,
			&conceptGroup.Comment,
			&conceptGroup.Icon,
			&conceptGroup.Color,
			&conceptGroup.BKNRawContent,
			&conceptGroup.KNID,
			&conceptGroup.Branch,
			&conceptGroup.Creator.ID,
			&conceptGroup.Creator.Type,
			&conceptGroup.CreateTime,
			&conceptGroup.Updater.ID,
			&conceptGroup.Updater.Type,
			&conceptGroup.UpdateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return map[string]*interfaces.ConceptGroup{}, err
		}

		// Convert a tag string to an array.
		conceptGroup.Tags = libCommon.TagString2TagSlice(tagsStr)

		conceptGroups[conceptGroup.CGID] = &conceptGroup
	}

	span.SetStatus(codes.Ok, "")
	return conceptGroups, nil
}

// Get object type bindings for the specified group.
func (cga *conceptGroupAccess) ListConceptGroupRelations(ctx context.Context, tx *sql.Tx,
	query interfaces.ConceptGroupRelationsQueryParams) ([]interfaces.ConceptGroupRelation, error) {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListConceptGroupRelations")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	subBuilder := sq.Select(
		"f_id",
		"f_kn_id",
		"f_branch",
		"f_group_id",
		"f_concept_type",
		"f_concept_id",
		"f_create_time",
	).From(CONCEPT_GROUP_RELATION_TABLE_NAME)

	builder := processConceptGroupRelationsQueryCondition(query, subBuilder, "")

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select concept group by id, error", err)
		return []interfaces.ConceptGroupRelation{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := tx.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []interfaces.ConceptGroupRelation{}, err
	}
	defer func() { _ = rows.Close() }()

	conceptGroupRelations := make([]interfaces.ConceptGroupRelation, 0)
	for rows.Next() {
		conceptGroupRelation := interfaces.ConceptGroupRelation{
			ModuleType: interfaces.MODULE_TYPE_CONCEPT_GROUP_RELATION,
		}

		err := rows.Scan(
			&conceptGroupRelation.ID,
			&conceptGroupRelation.KNID,
			&conceptGroupRelation.Branch,
			&conceptGroupRelation.CGID,
			&conceptGroupRelation.ConceptType,
			&conceptGroupRelation.ConceptID,
			&conceptGroupRelation.CreateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []interfaces.ConceptGroupRelation{}, err
		}

		conceptGroupRelations = append(conceptGroupRelations, conceptGroupRelation)
	}

	return conceptGroupRelations, nil
}

func (cga *conceptGroupAccess) CreateConceptGroupRelation(ctx context.Context, tx *sql.Tx, conceptGroupRelation *interfaces.ConceptGroupRelation) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateConceptGroupRelation")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	sqlStr, vals, err := sq.Insert(CONCEPT_GROUP_RELATION_TABLE_NAME).
		Columns(
			"f_id",
			"f_kn_id",
			"f_branch",
			"f_group_id",
			"f_concept_type",
			"f_concept_id",
			"f_create_time",
		).
		Values(
			conceptGroupRelation.ID,
			conceptGroupRelation.KNID,
			conceptGroupRelation.Branch,
			conceptGroupRelation.CGID,
			conceptGroupRelation.ConceptType,
			conceptGroupRelation.ConceptID,
			conceptGroupRelation.CreateTime).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of insert concept group relation, error", err)
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

// Build SQL filter conditions.
func processConceptGroupRelationsQueryCondition(query interfaces.ConceptGroupRelationsQueryParams, subBuilder sq.SelectBuilder, fieldPrefix string) sq.SelectBuilder {

	if query.KNID != "" {
		subBuilder = subBuilder.Where(sq.Eq{fmt.Sprintf("%s%s", fieldPrefix, "f_kn_id"): query.KNID})
	}

	if query.Branch != "" {
		subBuilder = subBuilder.Where(sq.Eq{fmt.Sprintf("%s%s", fieldPrefix, "f_branch"): query.Branch})
	} else {
		// Query business knowledge networks on the main branch.
		subBuilder = subBuilder.Where(sq.Eq{fmt.Sprintf("%s%s", fieldPrefix, "f_branch"): interfaces.MAIN_BRANCH})
	}

	if len(query.CGIDs) > 0 {
		subBuilder = subBuilder.Where(sq.Eq{fmt.Sprintf("%s%s", fieldPrefix, "f_group_id"): query.CGIDs})
	}

	if query.ConceptType != "" {
		subBuilder = subBuilder.Where(sq.Eq{fmt.Sprintf("%s%s", fieldPrefix, "f_concept_type"): query.ConceptType})
	}

	if len(query.OTIDs) != 0 {
		subBuilder = subBuilder.Where(sq.Eq{fmt.Sprintf("%s%s", fieldPrefix, "f_concept_id"): query.OTIDs})
	}

	return subBuilder
}

// Remove an object type from a group by deleting the concept-to-group binding.
func (cga *conceptGroupAccess) DeleteObjectTypesFromGroup(ctx context.Context, tx *sql.Tx, query interfaces.ConceptGroupRelationsQueryParams) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteObjectTypesFromGroup")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	builder := sq.Delete(CONCEPT_GROUP_RELATION_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": query.KNID}).
		Where(sq.Eq{"f_branch": query.Branch}).
		Where(sq.Eq{"f_concept_type": query.ConceptType})

	if len(query.CGIDs) > 0 {
		builder = builder.Where(sq.Eq{"f_group_id": query.CGIDs})
	}

	if len(query.OTIDs) > 0 {
		builder = builder.Where(sq.Eq{"f_concept_id": query.OTIDs})
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete concept group by concept group id, error", err)
		return 0, err
	}

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

func (cga *conceptGroupAccess) GetConceptIDsByConceptGroupIDs(ctx context.Context, knID string, branch string, cgIDs []string, conceptType string) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetConceptIDsByConceptGroupIDs")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	sqlStr, vals, err := sq.Select(
		"f_concept_id",
	).From(CONCEPT_GROUP_RELATION_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_concept_type": conceptType}).
		Where(sq.Eq{"f_group_id": cgIDs}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select concept ids by concept group, error", err)
		return []string{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := cga.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []string{}, err
	}
	defer func() { _ = rows.Close() }()

	conceptIDs := make([]string, 0)
	for rows.Next() {
		var conceptID string
		err := rows.Scan(
			&conceptID,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []string{}, err
		}

		conceptIDs = append(conceptIDs, conceptID)
	}

	span.SetStatus(codes.Ok, "")
	return conceptIDs, nil
}

// Get relation type IDs in a concept group.
func (cga *conceptGroupAccess) GetRelationTypeIDsFromConceptGroupRelation(ctx context.Context, query interfaces.ConceptGroupRelationsQueryParams) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetRelationTypeIDsFromConceptGroupRelation")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Subquery: retrieve object type concept IDs in the specified concept group.
	subQueryBuilder := sq.Select("cgr.f_concept_id").
		From(CONCEPT_GROUP_RELATION_TABLE_NAME + " AS cgr").
		Join(object_type.OT_TABLE_NAME + " AS ot ON cgr.f_concept_id = ot.f_id AND cgr.f_branch = ot.f_branch AND cgr.f_kn_id = ot.f_kn_id").
		Join(CONCEPT_GROUP_TABLE_NAME + " AS cg on cgr.f_group_id = cg.f_id and cgr.f_branch = cg.f_branch and cgr.f_kn_id = cg.f_kn_id")

	subQueryBuilder = processConceptGroupRelationsQueryCondition(query, subQueryBuilder, "cgr.")

	// Main query.
	builder := sq.Select(
		"f_id",
	).From(relation_type.RT_TABLE_NAME).
		Where(sq.Expr("f_source_object_type_id IN (?)", subQueryBuilder)).
		Where(sq.Expr("f_target_object_type_id IN (?)", subQueryBuilder))

	if query.KNID != "" {
		builder = builder.Where(sq.Eq{"f_kn_id": query.KNID})
	}

	if query.Branch != "" {
		builder = builder.Where(sq.Eq{"f_branch": query.Branch})
	} else {
		// Query business knowledge networks on the main branch.
		builder = builder.Where(sq.Eq{"f_branch": interfaces.MAIN_BRANCH})
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select relation type ids by concept group, error", err)
		return []string{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := cga.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []string{}, err
	}
	defer func() { _ = rows.Close() }()

	rtIDs := make([]string, 0)
	for rows.Next() {
		var rtID string

		err := rows.Scan(
			&rtID,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []string{}, err
		}

		rtIDs = append(rtIDs, rtID)
	}

	span.SetStatus(codes.Ok, "")
	return rtIDs, nil
}

// Get action type IDs in a concept group.
func (cga *conceptGroupAccess) GetActionTypeIDsFromConceptGroupRelation(ctx context.Context, query interfaces.ConceptGroupRelationsQueryParams) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetActionTypeIDsFromConceptGroupRelation")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Subquery: retrieve object type concept IDs in the specified concept group.
	subQueryBuilder := sq.Select("cgr.f_concept_id").
		From(CONCEPT_GROUP_RELATION_TABLE_NAME + " AS cgr").
		Join(object_type.OT_TABLE_NAME + " AS ot ON cgr.f_concept_id = ot.f_id AND cgr.f_branch = ot.f_branch AND cgr.f_kn_id = ot.f_kn_id").
		Join(CONCEPT_GROUP_TABLE_NAME + " AS cg on cgr.f_group_id = cg.f_id and cgr.f_branch = cg.f_branch and cgr.f_kn_id = cg.f_kn_id")

	subQueryBuilder = processConceptGroupRelationsQueryCondition(query, subQueryBuilder, "cgr.")

	// Main query.
	builder := sq.Select(
		"f_id",
	).From(action_type.AT_TABLE_NAME).
		Where(sq.Expr("f_object_type_id IN (?)", subQueryBuilder))

	if query.KNID != "" {
		builder = builder.Where(sq.Eq{"f_kn_id": query.KNID})
	}

	if query.Branch != "" {
		builder = builder.Where(sq.Eq{"f_branch": query.Branch})
	} else {
		// Query business knowledge networks on the main branch.
		builder = builder.Where(sq.Eq{"f_branch": interfaces.MAIN_BRANCH})
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select action type ids by concept group, error", err)
		return []string{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := cga.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []string{}, err
	}
	defer func() { _ = rows.Close() }()

	atIDs := make([]string, 0)
	for rows.Next() {
		var atID string

		err := rows.Scan(
			&atID,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []string{}, err
		}

		atIDs = append(atIDs, atID)
	}

	span.SetStatus(codes.Ok, "")
	return atIDs, nil
}

// Get group information for a concept.
func (cga *conceptGroupAccess) GetConceptGroupsByOTIDs(ctx context.Context, tx *sql.Tx,
	query interfaces.ConceptGroupRelationsQueryParams) (map[string][]*interfaces.ConceptGroup, error) {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetConceptGroupsByOTIDs")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
	)

	// Query.
	subBuilder := sq.Select(
		"cgr.f_concept_id",
		"cg.f_id",
		"cg.f_name",
		"cg.f_tags",
		"cg.f_comment",
		"cg.f_icon",
		"cg.f_color",
		// "cg.f_bkn_raw_content",
		"cg.f_kn_id",
		"cg.f_branch",
	).From(CONCEPT_GROUP_TABLE_NAME + " AS cg").
		Join(CONCEPT_GROUP_RELATION_TABLE_NAME + " AS cgr on cgr.f_group_id = cg.f_id and cgr.f_kn_id  = cg.f_kn_id and cgr.f_branch =cg.f_branch")

	builder := processConceptGroupRelationsQueryCondition(query, subBuilder, "cgr.")
	// Where(sq.Eq{"cgr.f_kn_id": knID}).
	// Where(sq.Eq{"cgr.f_branch": branch}).
	// Where(sq.Eq{"cgr.f_concept_type": interfaces.MODULE_TYPE_OBJECT_TYPE}).
	// Where(sq.Eq{"cgr.f_concept_id": otIDs})

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select concept group by object type ids, error", err)
		return map[string][]*interfaces.ConceptGroup{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := tx.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return map[string][]*interfaces.ConceptGroup{}, err
	}
	defer func() { _ = rows.Close() }()

	results := map[string][]*interfaces.ConceptGroup{}
	for rows.Next() {
		var otID string
		conceptGroup := &interfaces.ConceptGroup{
			ModuleType: interfaces.MODULE_TYPE_CONCEPT_GROUP,
		}
		tagsStr := ""
		err := rows.Scan(
			&otID,
			&conceptGroup.CGID,
			&conceptGroup.CGName,
			&tagsStr,
			&conceptGroup.Comment,
			&conceptGroup.Icon,
			&conceptGroup.Color,
			// &conceptGroup.Detail,
			&conceptGroup.KNID,
			&conceptGroup.Branch,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return map[string][]*interfaces.ConceptGroup{}, err
		}

		// Convert a tag string to an array.
		conceptGroup.Tags = libCommon.TagString2TagSlice(tagsStr)

		results[otID] = append(results[otID], conceptGroup)
	}

	span.SetStatus(codes.Ok, "")
	return results, nil
}
