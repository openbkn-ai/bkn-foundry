// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knowledge_network

import (
	"context"
	"database/sql"
	"fmt"
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

	"bkn-backend/common"
	"bkn-backend/drivenadapters/object_type"
	"bkn-backend/drivenadapters/relation_type"
	"bkn-backend/interfaces"
)

const (
	KN_TABLE_NAME = "t_knowledge_network"
)

var (
	knAccessOnce sync.Once
	knAccess     interfaces.KNAccess
)

type knowledgeNetworkAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

func NewKNAccess(appSetting *common.AppSetting) interfaces.KNAccess {
	knAccessOnce.Do(func() {
		knAccess = &knowledgeNetworkAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return knAccess
}

// Get business knowledge network existence by ID.
func (kna *knowledgeNetworkAccess) CheckKNExistByID(ctx context.Context,
	knID string, branch string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query knowledge network")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	// Query.
	sqlStr, vals, err := sq.Select(
		"f_name").
		From(KN_TABLE_NAME).
		Where(sq.Eq{"f_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of get id by f_id, error", err)
		return "", false, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var name string
	err = kna.db.QueryRow(sqlStr, vals...).Scan(&name)
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

// Get business knowledge network existence by name.
func (oma *knowledgeNetworkAccess) CheckKNExistByName(ctx context.Context,
	knName string, branch string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Query knowledge network")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	// Query.
	sqlStr, vals, err := sq.Select(
		"f_id").
		From(KN_TABLE_NAME).
		Where(sq.Eq{"f_name": knName}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of get id by name, error", err)
		return "", false, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	var knID string
	err = oma.db.QueryRow(sqlStr, vals...).Scan(
		&knID,
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
	return knID, true, nil
}

// Create a business knowledge network.
func (kna *knowledgeNetworkAccess) CreateKN(ctx context.Context, tx *sql.Tx, KN *interfaces.KN) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Insert into knowledge network")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	// Convert tags to a string.
	tagsStr := libCommon.TagSlice2TagString(KN.Tags)

	sqlStr, vals, err := sq.Insert(KN_TABLE_NAME).
		Columns(
			"f_id",
			"f_name",
			"f_tags",
			"f_comment",
			"f_icon",
			"f_color",
			"f_bkn_raw_content",
			"f_skill_content",
			"f_branch",
			"f_business_domain",
			"f_creator",
			"f_creator_type",
			"f_create_time",
			"f_updater",
			"f_updater_type",
			"f_update_time",
		).
		Values(
			KN.KNID,
			KN.KNName,
			tagsStr,
			KN.Comment,
			KN.Icon,
			KN.Color,
			KN.BKNRawContent,
			KN.SkillContent,
			KN.Branch,
			KN.BusinessDomain,
			KN.Creator.ID,
			KN.Creator.Type,
			KN.CreateTime,
			KN.Updater.ID,
			KN.Updater.Type,
			KN.UpdateTime).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of insert knowledge network, error", err)
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

// Query current business knowledge networks on the main branch.
func (kna *knowledgeNetworkAccess) ListKNs(ctx context.Context, query interfaces.KNsQueryParams) ([]*interfaces.KN, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Select knowledge networks")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	columns := []string{
		"f_id",
		"f_name",
		"f_tags",
		"f_comment",
		"f_icon",
		"f_color",
		"f_bkn_raw_content",
		"f_skill_content",
		"f_branch",
		"f_business_domain",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	}
	if query.OnlyIDs {
		columns = []string{"f_id"}
	}
	subBuilder := sq.Select(columns...).From(KN_TABLE_NAME)

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
		common.LogSafeError(ctx, "Failed to build the sql of select knowledge networks, error", err)
		return []*interfaces.KN{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := kna.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []*interfaces.KN{}, err
	}
	defer func() { _ = rows.Close() }()

	KNs := make([]*interfaces.KN, 0)
	for rows.Next() {
		KN := interfaces.KN{
			ModuleType: interfaces.MODULE_TYPE_KN,
		}
		if query.OnlyIDs {
			if err := rows.Scan(&KN.KNID); err != nil {
				common.LogSafeError(ctx, "Row scan error", err)
				return []*interfaces.KN{}, err
			}
			KNs = append(KNs, &KN)
			continue
		}
		tagsStr := ""
		err := rows.Scan(
			&KN.KNID,
			&KN.KNName,
			&tagsStr,
			&KN.Comment,
			&KN.Icon,
			&KN.Color,
			&KN.BKNRawContent,
			&KN.SkillContent,
			&KN.Branch,
			&KN.BusinessDomain,
			&KN.Creator.ID,
			&KN.Creator.Type,
			&KN.CreateTime,
			&KN.Updater.ID,
			&KN.Updater.Type,
			&KN.UpdateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []*interfaces.KN{}, err
		}

		// Convert a tag string to an array.
		KN.Tags = libCommon.TagString2TagSlice(tagsStr)
		KNs = append(KNs, &KN)
	}

	span.SetStatus(codes.Ok, "")
	return KNs, nil
}

func (kna *knowledgeNetworkAccess) GetKNsTotal(ctx context.Context, query interfaces.KNsQueryParams) (int, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Select knowledge networks total number")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	subBuilder := sq.Select("COUNT(f_id)").From(KN_TABLE_NAME)
	builder := processQueryCondition(query, subBuilder)

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select knowledge networks total, error", err)
		return 0, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	total := 0
	err = kna.db.QueryRow(sqlStr, vals...).Scan(&total)
	if err != nil {
		common.LogSafeError(ctx, "Get knowledge network totals error", err)
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return total, nil
}

func (kna *knowledgeNetworkAccess) GetKNByID(ctx context.Context,
	knID string, branch string) (*interfaces.KN, error) {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, fmt.Sprintf("Get knowledge network[%s]", knID))
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	sqlStr, vals, err := sq.Select(
		"f_id",
		"f_name",
		"f_tags",
		"f_comment",
		"f_icon",
		"f_color",
		"f_bkn_raw_content",
		"f_skill_content",
		"f_branch",
		"f_business_domain",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	).From(KN_TABLE_NAME).
		Where(sq.Eq{"f_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select knowledge network by id, error", err)
		return nil, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	tagsStr := ""
	KN := interfaces.KN{
		ModuleType: interfaces.MODULE_TYPE_KN,
	}
	err = kna.db.QueryRow(sqlStr, vals...).Scan(
		&KN.KNID,
		&KN.KNName,
		&tagsStr,
		&KN.Comment,
		&KN.Icon,
		&KN.Color,
		&KN.BKNRawContent,
		&KN.SkillContent,
		&KN.Branch,
		&KN.BusinessDomain,
		&KN.Creator.ID,
		&KN.Creator.Type,
		&KN.CreateTime,
		&KN.Updater.ID,
		&KN.Updater.Type,
		&KN.UpdateTime,
	)
	if err == sql.ErrNoRows {
		span.SetAttributes(attr.Key("no_rows").Bool(true))
		span.SetStatus(codes.Ok, "")
		return nil, nil
	} else if err != nil {
		common.LogSafeError(ctx, "Get knowledge network by id error", err)
		return nil, err
	}

	// Convert a tag string to an array.
	KN.Tags = libCommon.TagString2TagSlice(tagsStr)

	span.SetStatus(codes.Ok, "")
	return &KN, nil
}

func (kna *knowledgeNetworkAccess) UpdateKN(ctx context.Context, tx *sql.Tx, kn *interfaces.KN) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, fmt.Sprintf("Update knowledge network[%s]", kn.KNID))
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	// Convert tags to a string.
	tagsStr := libCommon.TagSlice2TagString(kn.Tags)

	data := map[string]any{
		"f_name":            kn.KNName,
		"f_tags":            tagsStr,
		"f_comment":         kn.Comment,
		"f_icon":            kn.Icon,
		"f_color":           kn.Color,
		"f_bkn_raw_content": kn.BKNRawContent,
		"f_skill_content":   kn.SkillContent,
		"f_updater":         kn.Updater.ID,
		"f_updater_type":    kn.Updater.Type,
		"f_update_time":     kn.UpdateTime,
	}
	sqlStr, vals, err := sq.Update(KN_TABLE_NAME).
		SetMap(data).
		Where(sq.Eq{"f_id": kn.KNID}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of update knowledge network by knowledge network_id, error", err)
		return err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	ret, err := tx.Exec(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "Update data error", err)
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
		otellog.LogWarn(ctx, fmt.Sprintf("Update knowledge network affected unexpected row count: knowledge_network_id=%s, rows=%d",
			kn.KNID, RowsAffected))
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (kna *knowledgeNetworkAccess) UpdateKNDetail(ctx context.Context,
	knID string, branch string, detail string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, fmt.Sprintf("Update knowledge network detail[%s]", knID))
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
		attr.Key("kn_id").String(knID))

	data := map[string]any{
		"f_bkn_raw_content": detail,
	}
	sqlStr, vals, err := sq.Update(KN_TABLE_NAME).
		SetMap(data).
		Where(sq.Eq{"f_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of update knowledge network detail by knowledge network_id, error", err)
		return err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	ret, err := kna.db.Exec(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "Update data error", err)
		return err
	}

	// Number of rows affected by the SQL statement.
	RowsAffected, err := ret.RowsAffected()
	if err != nil {
		common.LogSafeError(ctx, "Get RowsAffected error", err)
		return err
	}

	if RowsAffected != 1 {
		otellog.LogWarn(ctx, fmt.Sprintf("Update knowledge network detail %s RowsAffected not equal 1, RowsAffected is %d",
			knID, RowsAffected))
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (kna *knowledgeNetworkAccess) DeleteKN(ctx context.Context,
	tx *sql.Tx, knID string, branch string) (int64, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete knowledge networks from db")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()),
		attr.Key("kn_id").String(knID))

	sqlStr, vals, err := sq.Delete(KN_TABLE_NAME).
		Where(sq.Eq{"f_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of delete knowledge network by knowledge network_id, error", err)
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

	if RowsAffected != 1 {
		otellog.LogWarn(ctx, fmt.Sprintf("Delete knowledge network %s RowsAffected not equal 1, RowsAffected is %d",
			knID, RowsAffected))
	}

	logger.Infof("RowsAffected: %d", RowsAffected)
	span.SetStatus(codes.Ok, "")
	return RowsAffected, nil
}

// Build SQL filter conditions.
func processQueryCondition(query interfaces.KNsQueryParams, subBuilder sq.SelectBuilder) sq.SelectBuilder {
	if query.NamePattern != "" {
		// Fuzzy-match name or ID; either match is sufficient.
		subBuilder = subBuilder.Where(sq.Expr("(instr(f_name, ?) > 0 OR instr(f_id, ?) > 0)", query.NamePattern, query.NamePattern))
	}

	if query.Tag != "" {
		subBuilder = subBuilder.Where(sq.Expr("instr(f_tags, ?) > 0", `"`+query.Tag+`"`))
	}

	if query.BusinessDomain != "" {
		subBuilder = subBuilder.Where(sq.Eq{"f_business_domain": query.BusinessDomain})
	}

	if len(query.CandidateIDs) > 0 {
		subBuilder = subBuilder.Where(sq.Eq{"f_id": query.CandidateIDs})
	}

	return subBuilder
}

// Get neighboring object types.
func (kna *knowledgeNetworkAccess) GetNeighborPathsBatch(ctx context.Context, otIDs []string,
	query interfaces.RelationTypePathsBaseOnSource) (map[string][]interfaces.RelationTypePath, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Select relation type paths")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	sqlStr := ""
	vals := []any{}
	var err error

	// When concept groups are present, relation types must be within their scope.
	var subQueryBuilder sq.SelectBuilder
	if len(query.ConceptGroups) > 0 {
		// Subquery: retrieve object type concept IDs in the specified concept group.
		subQueryBuilder = sq.Select("cgr.f_concept_id").
			From("t_concept_group_relation AS cgr").
			Join(object_type.OT_TABLE_NAME + " AS ot ON cgr.f_concept_id = ot.f_id AND cgr.f_branch = ot.f_branch AND cgr.f_kn_id = ot.f_kn_id").
			Join("t_concept_group AS cg on cgr.f_group_id = cg.f_id and cgr.f_branch = cg.f_branch and cgr.f_kn_id = cg.f_kn_id")

		subQueryBuilder = processConceptGroupRelationsQueryCondition(interfaces.ConceptGroupRelationsQueryParams{
			KNID:        query.KNID,
			Branch:      query.Branch,
			CGIDs:       query.ConceptGroups,
			ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
		}, subQueryBuilder, "cgr.")
	}

	switch query.Direction {
	case interfaces.DIRECTION_FORWARD:
		subBuilder := sq.Select(
			// Relation information.
			`"forward" as direction`,
			"rt.f_source_object_type_id",
			"rt.f_target_object_type_id",
			"rt.f_id",
			"rt.f_name",
			"rt.f_source_object_type_id",
			"rt.f_target_object_type_id",
			"rt.f_type",
			"rt.f_mapping_rules",
			// Forward target information. The source was retrieved in the prior traversal step.
			"ot.f_id",
			"ot.f_name",
			"ot.f_data_source",
			"ot.f_data_properties",
			"ot.f_logic_properties",
			"ot.f_primary_keys",
			"ot.f_display_key",
		).From(relation_type.RT_TABLE_NAME + " " + "AS rt").
			Join(object_type.OT_TABLE_NAME + " " + "AS ot on rt.f_target_object_type_id = ot.f_id AND rt.f_branch = ot.f_branch AND rt.f_kn_id = ot.f_kn_id ").
			Where(sq.Eq{"rt.f_source_object_type_id": otIDs}).
			Where(sq.Eq{"rt.f_kn_id": query.KNID})

		// Relation types must be in the group: both source and target must belong to it.
		if len(query.ConceptGroups) > 0 {
			subBuilder = subBuilder.
				Where(sq.Expr("rt.f_source_object_type_id IN (?)", subQueryBuilder)).
				Where(sq.Expr("rt.f_target_object_type_id IN (?)", subQueryBuilder))
		}

		sqlStr, vals, err = subBuilder.ToSql()
		if err != nil {
			common.LogSafeError(ctx, "Failed to build the sql of select model by id, error", err)
			return nil, err
		}
	case interfaces.DIRECTION_BACKWARD:
		subBuilder := sq.Select(
			// Relation information.
			`"backward" as direction`,
			"rt.f_target_object_type_id",
			"rt.f_source_object_type_id",
			"rt.f_id",
			"rt.f_name",
			"rt.f_source_object_type_id",
			"rt.f_target_object_type_id",
			"rt.f_type",
			"rt.f_mapping_rules",
			// Reverse traversal goes from relation target to source. The current target was retrieved in the prior step.
			"ot.f_id",
			"ot.f_name",
			"ot.f_data_source",
			"ot.f_data_properties",
			"ot.f_logic_properties",
			"ot.f_primary_keys",
			"ot.f_display_key",
		).From(relation_type.RT_TABLE_NAME + " " + "AS rt").
			Join(object_type.OT_TABLE_NAME + " " + "AS ot on rt.f_source_object_type_id = ot.f_id AND rt.f_branch = ot.f_branch AND rt.f_kn_id = ot.f_kn_id ").
			Where(sq.Eq{"rt.f_target_object_type_id": otIDs}).
			Where(sq.Eq{"rt.f_kn_id": query.KNID})

		// Relation types must be in the group: both source and target must belong to it.
		if len(query.ConceptGroups) > 0 {
			subBuilder = subBuilder.
				Where(sq.Expr("rt.f_source_object_type_id IN (?)", subQueryBuilder)).
				Where(sq.Expr("rt.f_target_object_type_id IN (?)", subQueryBuilder))
		}

		sqlStr, vals, err = subBuilder.ToSql()
		if err != nil {
			common.LogSafeError(ctx, "Failed to build the sql of select model by id, error", err)
			return nil, err
		}
	case interfaces.DIRECTION_BIDIRECTIONAL:
		subBuilder1 := sq.Select(
			// Relation information.
			`"forward" as direction`,
			"rt.f_source_object_type_id",
			"rt.f_target_object_type_id",
			"rt.f_id",
			"rt.f_name",
			"rt.f_source_object_type_id",
			"rt.f_target_object_type_id",
			"rt.f_type",
			"rt.f_mapping_rules",
			// Forward target information. The source was retrieved in the prior traversal step.
			"ot.f_id",
			"ot.f_name",
			"ot.f_data_source",
			"ot.f_data_properties",
			"ot.f_logic_properties",
			"ot.f_primary_keys",
			"ot.f_display_key",
		).From(relation_type.RT_TABLE_NAME + " " + "AS rt").
			Join(object_type.OT_TABLE_NAME + " " + "AS ot on rt.f_target_object_type_id = ot.f_id AND rt.f_branch = ot.f_branch AND rt.f_kn_id = ot.f_kn_id ").
			Where(sq.Eq{"rt.f_source_object_type_id": otIDs}).
			Where(sq.Eq{"rt.f_kn_id": query.KNID})
		subBuilder2 := sq.Select(
			// Relation information.
			`"backward" as direction`,
			"rt.f_target_object_type_id",
			"rt.f_source_object_type_id",
			"rt.f_id",
			"rt.f_name",
			"rt.f_source_object_type_id",
			"rt.f_target_object_type_id",
			"rt.f_type",
			"rt.f_mapping_rules",
			// Reverse traversal goes from relation target to source. The current target was retrieved in the prior step.
			"ot.f_id",
			"ot.f_name",
			"ot.f_data_source",
			"ot.f_data_properties",
			"ot.f_logic_properties",
			"ot.f_primary_keys",
			"ot.f_display_key",
		).From(relation_type.RT_TABLE_NAME + " " + "AS rt").
			Join(object_type.OT_TABLE_NAME + " " + "AS ot on rt.f_source_object_type_id = ot.f_id AND rt.f_branch = ot.f_branch AND rt.f_kn_id = ot.f_kn_id ").
			Where(sq.Eq{"rt.f_target_object_type_id": otIDs}).
			Where(sq.Eq{"rt.f_kn_id": query.KNID})
		// Relation types must be in the group: both source and target must belong to it.
		if len(query.ConceptGroups) > 0 {
			subBuilder1 = subBuilder1.
				Where(sq.Expr("rt.f_source_object_type_id IN (?)", subQueryBuilder)).
				Where(sq.Expr("rt.f_target_object_type_id IN (?)", subQueryBuilder))
		}
		// Relation types must be in the group: both source and target must belong to it.
		if len(query.ConceptGroups) > 0 {
			subBuilder2 = subBuilder2.
				Where(sq.Expr("rt.f_source_object_type_id IN (?)", subQueryBuilder)).
				Where(sq.Expr("rt.f_target_object_type_id IN (?)", subQueryBuilder))
		}

		sqlStr1, vals1, err := subBuilder1.ToSql()
		if err != nil {
			common.LogSafeError(ctx, "Failed to build the sql of select model by id, error", err)
			return nil, err
		}
		sqlStr2, vals2, err := subBuilder2.ToSql()
		if err != nil {
			common.LogSafeError(ctx, "Failed to build the sql of select model by id, error", err)
			return nil, err
		}

		sqlStr = sqlStr1 + " UNION ALL " + sqlStr2
		vals = append(vals, vals1...)
		vals = append(vals, vals2...)
	}

	rows, err := kna.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	rtPathsMap := map[string][]interfaces.RelationTypePath{}
	for rows.Next() {
		var (
			direction            string
			sourceID, neighborID string
			mappingRulesBytes    []byte
			neighbor             interfaces.ObjectTypeWithKeyField
			relationType         interfaces.RelationTypeWithKeyField
			dataSourceBytes      []byte
			dataPropertiesBytes  []byte
			logicPropertiesBytes []byte
			primaryKeysBytes     []byte
		)

		err := rows.Scan(
			&direction,
			&sourceID,
			&neighborID,
			&relationType.RTID,
			&relationType.RTName,
			&relationType.SourceObjectTypeID,
			&relationType.TargetObjectTypeID,
			&relationType.Type,
			&mappingRulesBytes,
			&neighbor.OTID,
			&neighbor.OTName,
			&dataSourceBytes,
			&dataPropertiesBytes,
			&logicPropertiesBytes,
			&primaryKeysBytes,
			&neighbor.DisplayKey)
		if err != nil {
			return nil, err
		}

		// 2.0 Deserialize mapping rules.
		err = sonic.Unmarshal(mappingRulesBytes, &relationType.MappingRules)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal mappingRules after getting relation type, err", err)
			return nil, err
		}
		// 2.0 Deserialize the data source.
		err = sonic.Unmarshal(dataSourceBytes, &neighbor.DataSource)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal dataSource after getting object type, err", err)
			return nil, err
		}

		// 2.1 Deserialize data properties.
		err = sonic.Unmarshal(dataPropertiesBytes, &neighbor.DataProperties)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal dataProperties after getting object type, err", err)
			return nil, err
		}

		// 2.2 Deserialize logical properties.
		err = sonic.Unmarshal(logicPropertiesBytes, &neighbor.LogicProperties)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal logicProperties after getting object type, err", err)
			return nil, err
		}

		// 2.3 Deserialize primary keys.
		err = sonic.Unmarshal(primaryKeysBytes, &neighbor.PrimaryKeys)
		if err != nil {
			common.LogSafeError(ctx, "Failed to unmarshal primaryKeys after getting object type, err", err)
			return nil, err
		}

		// Neighbor lookup is a one-hop path, so assemble it while retrieving neighbors because relation fields are also needed.
		ots := []interfaces.ObjectTypeWithKeyField{
			{
				OTID: sourceID,
			},
		}
		ots = append(ots, neighbor)
		rtPath := interfaces.RelationTypePath{
			ObjectTypes: ots,
			TypeEdges: []interfaces.TypeEdge{
				{
					RelationTypeId:      relationType.RTID,
					RelationType:        relationType,
					SourceObjectTypeId:  sourceID,
					Target_ObjectTypeId: neighborID,
					Direction:           direction,
				},
			},
			Length: 1,
		}
		rtPathsMap[sourceID] = append(rtPathsMap[sourceID], rtPath)
	}

	return rtPathsMap, nil
}

// Query current business knowledge networks on the main branch.
func (kna *knowledgeNetworkAccess) GetAllKNs(ctx context.Context) (map[string]*interfaces.KN, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Select knowledge networks")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	sqlStr, vals, err := sq.Select(
		"f_id",
		"f_name",
		"f_tags",
		"f_comment",
		"f_icon",
		"f_color",
		"f_bkn_raw_content",
		"f_skill_content",
		"f_branch",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time").
		From(KN_TABLE_NAME).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select knowledge networks, error", err)
		return map[string]*interfaces.KN{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := kna.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return map[string]*interfaces.KN{}, err
	}
	defer func() { _ = rows.Close() }()

	KNs := make(map[string]*interfaces.KN)
	for rows.Next() {
		KN := interfaces.KN{
			ModuleType: interfaces.MODULE_TYPE_KN,
		}
		tagsStr := ""
		err := rows.Scan(
			&KN.KNID,
			&KN.KNName,
			&tagsStr,
			&KN.Comment,
			&KN.Icon,
			&KN.Color,
			&KN.BKNRawContent,
			&KN.SkillContent,
			&KN.Branch,
			&KN.Creator.ID,
			&KN.Creator.Type,
			&KN.CreateTime,
			&KN.Updater.ID,
			&KN.Updater.Type,
			&KN.UpdateTime,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return map[string]*interfaces.KN{}, err
		}

		// Convert a tag string to an array.
		KN.Tags = libCommon.TagString2TagSlice(tagsStr)
		KNs[KN.KNID] = &KN
	}

	span.SetStatus(codes.Ok, "")
	return KNs, nil
}

// GetKNNamesByIDs resolves knowledge network names in bulk using only f_id and f_name. Missing IDs are skipped and an empty input returns an empty slice.
func (kna *knowledgeNetworkAccess) GetKNNamesByIDs(ctx context.Context,
	ids []string, branch string) ([]*interfaces.KNNameEntry, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Select knowledge network names by ids")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	entries := make([]*interfaces.KNNameEntry, 0, len(ids))
	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return entries, nil
	}

	sqlStr, vals, err := sq.Select(
		"f_id",
		"f_name").
		From(KN_TABLE_NAME).
		Where(sq.Eq{"f_id": ids}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select knowledge network names by ids, error", err)
		return nil, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := kna.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		entry := &interfaces.KNNameEntry{}
		if err := rows.Scan(&entry.ID, &entry.Name); err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return nil, err
		}
		entries = append(entries, entry)
	}

	span.SetStatus(codes.Ok, "")
	return entries, nil
}

func (kna *knowledgeNetworkAccess) ListKnSrcs(ctx context.Context,
	query interfaces.KNsQueryParams) ([]interfaces.PermissionResource, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Select knowledge networks")
	defer span.End()

	span.SetAttributes(
		attr.Key("db_url").String(libdb.GetDBUrl()),
		attr.Key("db_type").String(libdb.GetDBType()))

	// New business knowledge network.
	subBuilder := sq.Select(
		"f_id",
		"f_name").
		From(KN_TABLE_NAME)
	builder := processQueryCondition(query, subBuilder)

	// Sort.
	if query.Sort != "" {
		builder = builder.OrderBy(fmt.Sprintf("%s %s", query.Sort, query.Direction))
	}
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		common.LogSafeError(ctx, "Failed to build the sql of select knowledge networks, error", err)
		return []interfaces.PermissionResource{}, err
	}

	// Record the processed SQL string.
	otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))

	rows, err := kna.db.Query(sqlStr, vals...)
	if err != nil {
		common.LogSafeError(ctx, "List data error", err)
		return []interfaces.PermissionResource{}, err
	}
	defer func() { _ = rows.Close() }()

	srcs := make([]interfaces.PermissionResource, 0)
	for rows.Next() {
		src := interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
		}
		err := rows.Scan(
			&src.ID,
			&src.Name,
		)
		if err != nil {
			common.LogSafeError(ctx, "Row scan error", err)
			return []interfaces.PermissionResource{}, err
		}
		srcs = append(srcs, src)
	}

	span.SetStatus(codes.Ok, "")
	return srcs, nil
}

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
