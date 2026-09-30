// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package risk_type

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

const (
	RT_TABLE_NAME                  = "t_risk_type"
	riskTypeIdentityQueryBatchSize = 500
	riskTypeInsertBatchSize        = 200
	riskTypeUpdateBatchSize        = 200
)

var (
	rtAccessOnce sync.Once
	rtAccess     interfaces.RiskTypeAccess
)

type riskTypeAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

func NewRiskTypeAccess(appSetting *common.AppSetting) interfaces.RiskTypeAccess {
	rtAccessOnce.Do(func() {
		rtAccess = &riskTypeAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return rtAccess
}

func (rta *riskTypeAccess) CheckRiskTypeExistByID(ctx context.Context, knID string, branch string, rtID string) (string, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CheckRiskTypeExistByID")
	defer span.End()

	sqlStr, vals, err := sq.Select("f_name").
		From(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": rtID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return "", false, err
	}

	var name string
	err = rta.db.QueryRow(sqlStr, vals...).Scan(&name)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "")
		return "", false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return "", false, err
	}
	span.SetStatus(codes.Ok, "")
	return name, true, nil
}

func (rta *riskTypeAccess) CheckRiskTypeExistByName(ctx context.Context, knID string, branch string, rtName string) (string, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CheckRiskTypeExistByName")
	defer span.End()

	sqlStr, vals, err := sq.Select("f_id").
		From(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_name": rtName}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return "", false, err
	}

	var rtID string
	err = rta.db.QueryRow(sqlStr, vals...).Scan(&rtID)
	if err == sql.ErrNoRows {
		span.SetStatus(codes.Ok, "")
		return "", false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return "", false, err
	}
	span.SetStatus(codes.Ok, "")
	return rtID, true, nil
}

func (rta *riskTypeAccess) GetRiskTypeIdentitiesByIDsOrNames(ctx context.Context, knID string,
	branch string, rtIDs, rtNames []string) ([]*interfaces.RiskType, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetRiskTypeIdentitiesByIDsOrNames")
	defer span.End()
	span.SetAttributes(
		attr.Int("id_count", len(rtIDs)),
		attr.Int("name_count", len(rtNames)),
	)

	result := make([]*interfaces.RiskType, 0)
	seen := make(map[string]struct{})
	requestCount := max(len(rtIDs), len(rtNames))
	for start := 0; start < requestCount; start += riskTypeIdentityQueryBatchSize {
		idEnd := min(start+riskTypeIdentityQueryBatchSize, len(rtIDs))
		nameEnd := min(start+riskTypeIdentityQueryBatchSize, len(rtNames))
		conditions := sq.Or{}
		if start < len(rtIDs) {
			conditions = append(conditions, sq.Eq{"f_id": rtIDs[start:idEnd]})
		}
		if start < len(rtNames) {
			conditions = append(conditions, sq.Eq{"f_name": rtNames[start:nameEnd]})
		}
		sqlStr, vals, err := sq.Select("f_id", "f_name").From(RT_TABLE_NAME).
			Where(sq.Eq{"f_kn_id": knID}).
			Where(sq.Eq{"f_branch": branch}).
			Where(conditions).
			ToSql()
		if err != nil {
			span.SetStatus(codes.Error, "Build sql failed")
			return nil, err
		}
		otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))
		rows, err := rta.db.Query(sqlStr, vals...)
		if err != nil {
			span.SetStatus(codes.Error, "Query data failed")
			return nil, err
		}
		for rows.Next() {
			riskType := &interfaces.RiskType{ModuleType: interfaces.MODULE_TYPE_RISK_TYPE}
			if err = rows.Scan(&riskType.RTID, &riskType.RTName); err != nil {
				_ = rows.Close()
				span.SetStatus(codes.Error, "Scan data failed")
				return nil, err
			}
			if _, duplicate := seen[riskType.RTID]; duplicate {
				continue
			}
			seen[riskType.RTID] = struct{}{}
			result = append(result, riskType)
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			span.SetStatus(codes.Error, "Iterate data failed")
			return nil, err
		}
		_ = rows.Close()
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

func (rta *riskTypeAccess) CreateRiskType(ctx context.Context, tx *sql.Tx, riskType *interfaces.RiskType) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CreateRiskType")
	defer span.End()

	tagsStr := libCommon.TagSlice2TagString(riskType.Tags)

	sqlStr, vals, err := sq.Insert(RT_TABLE_NAME).
		Columns(
			"f_id",
			"f_name",
			"f_comment",
			"f_tags",
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
			riskType.RTID,
			riskType.RTName,
			riskType.Comment,
			tagsStr,
			riskType.Icon,
			riskType.Color,
			riskType.BKNRawContent,
			riskType.KNID,
			riskType.Branch,
			riskType.Creator.ID,
			riskType.Creator.Type,
			riskType.CreateTime,
			riskType.Updater.ID,
			riskType.Updater.Type,
			riskType.UpdateTime,
		).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	_, err = tx.Exec(sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Insert data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (rta *riskTypeAccess) CreateRiskTypes(ctx context.Context, tx *sql.Tx,
	riskTypes []*interfaces.RiskType) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CreateRiskTypes")
	defer span.End()
	span.SetAttributes(attr.Int("risk_type_count", len(riskTypes)))

	for start := 0; start < len(riskTypes); start += riskTypeInsertBatchSize {
		end := min(start+riskTypeInsertBatchSize, len(riskTypes))
		builder := sq.Insert(RT_TABLE_NAME).Columns(
			"f_id", "f_name", "f_comment", "f_tags", "f_icon", "f_color", "f_bkn_raw_content",
			"f_kn_id", "f_branch", "f_creator", "f_creator_type", "f_create_time", "f_updater",
			"f_updater_type", "f_update_time",
		)
		for _, riskType := range riskTypes[start:end] {
			builder = builder.Values(riskTypeInsertValues(riskType)...)
		}
		sqlStr, vals, err := builder.ToSql()
		if err != nil {
			span.SetStatus(codes.Error, "Build sql failed")
			return err
		}
		otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(vals)))
		if _, err = tx.Exec(sqlStr, vals...); err != nil {
			span.SetStatus(codes.Error, "Insert data failed")
			return err
		}
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func riskTypeInsertValues(riskType *interfaces.RiskType) []any {
	return []any{
		riskType.RTID, riskType.RTName, riskType.Comment, libCommon.TagSlice2TagString(riskType.Tags),
		riskType.Icon, riskType.Color, riskType.BKNRawContent, riskType.KNID, riskType.Branch,
		riskType.Creator.ID, riskType.Creator.Type, riskType.CreateTime, riskType.Updater.ID,
		riskType.Updater.Type, riskType.UpdateTime,
	}
}

func (rta *riskTypeAccess) ListRiskTypes(ctx context.Context, query interfaces.RiskTypesQueryParams) ([]*interfaces.RiskType, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ListRiskTypes")
	defer span.End()

	subBuilder := sq.Select(
		"f_id",
		"f_name",
		"f_comment",
		"f_tags",
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
	).From(RT_TABLE_NAME)

	builder := processRiskTypeQueryCondition(query, subBuilder)
	if query.Sort != "" {
		dir := query.Direction
		if dir == "" {
			dir = interfaces.DESC_DIRECTION
		}
		orderBy, err := common.SafeOrderBy(query.Sort, dir)
		if err != nil {
			return nil, err
		}
		builder = builder.OrderBy(orderBy)
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := rta.db.Query(sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var riskTypes []*interfaces.RiskType
	for rows.Next() {
		rt := &interfaces.RiskType{ModuleType: interfaces.MODULE_TYPE_RISK_TYPE}
		var tagsStr string

		err := rows.Scan(
			&rt.RTID,
			&rt.RTName,
			&rt.Comment,
			&tagsStr,
			&rt.Icon,
			&rt.Color,
			&rt.BKNRawContent,
			&rt.KNID,
			&rt.Branch,
			&rt.Creator.ID,
			&rt.Creator.Type,
			&rt.CreateTime,
			&rt.Updater.ID,
			&rt.Updater.Type,
			&rt.UpdateTime,
		)
		if err != nil {
			span.SetStatus(codes.Error, "Scan data failed")
			return nil, err
		}

		rt.Tags = libCommon.TagString2TagSlice(tagsStr)

		riskTypes = append(riskTypes, rt)
	}

	span.SetStatus(codes.Ok, "")
	return riskTypes, nil
}

func (rta *riskTypeAccess) GetRiskTypesTotal(ctx context.Context, query interfaces.RiskTypesQueryParams) (int, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetRiskTypesTotal")
	defer span.End()

	subBuilder := sq.Select("COUNT(f_id)").From(RT_TABLE_NAME)
	builder := processRiskTypeQueryCondition(query, subBuilder)
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}

	var total int
	err = rta.db.QueryRow(sqlStr, vals...).Scan(&total)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return 0, err
	}
	span.SetStatus(codes.Ok, "")
	return total, nil
}

func (rta *riskTypeAccess) GetRiskTypesByIDs(ctx context.Context, knID string, branch string, rtIDs []string) ([]*interfaces.RiskType, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetRiskTypesByIDs")
	defer span.End()

	if len(rtIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.RiskType{}, nil
	}

	sqlStr, vals, err := sq.Select(
		"f_id",
		"f_name",
		"f_comment",
		"f_tags",
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
	).From(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": rtIDs}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}

	rows, err := rta.db.Query(sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var riskTypes []*interfaces.RiskType
	for rows.Next() {
		rt := &interfaces.RiskType{ModuleType: interfaces.MODULE_TYPE_RISK_TYPE}
		var tagsStr string

		err := rows.Scan(
			&rt.RTID,
			&rt.RTName,
			&rt.Comment,
			&tagsStr,
			&rt.Icon,
			&rt.Color,
			&rt.BKNRawContent,
			&rt.KNID,
			&rt.Branch,
			&rt.Creator.ID,
			&rt.Creator.Type,
			&rt.CreateTime,
			&rt.Updater.ID,
			&rt.Updater.Type,
			&rt.UpdateTime,
		)
		if err != nil {
			span.SetStatus(codes.Error, "Scan data failed")
			return nil, err
		}

		rt.Tags = libCommon.TagString2TagSlice(tagsStr)

		riskTypes = append(riskTypes, rt)
	}

	span.SetStatus(codes.Ok, "")
	return riskTypes, nil
}

func (rta *riskTypeAccess) UpdateRiskType(ctx context.Context, tx *sql.Tx, riskType *interfaces.RiskType) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "UpdateRiskType")
	defer span.End()

	tagsStr := libCommon.TagSlice2TagString(riskType.Tags)

	data := map[string]any{
		"f_name":            riskType.RTName,
		"f_comment":         riskType.Comment,
		"f_tags":            tagsStr,
		"f_icon":            riskType.Icon,
		"f_color":           riskType.Color,
		"f_bkn_raw_content": riskType.BKNRawContent,
		"f_updater":         riskType.Updater.ID,
		"f_updater_type":    riskType.Updater.Type,
		"f_update_time":     riskType.UpdateTime,
	}

	sqlStr, vals, err := sq.Update(RT_TABLE_NAME).
		SetMap(data).
		Where(sq.Eq{"f_id": riskType.RTID}).
		Where(sq.Eq{"f_kn_id": riskType.KNID}).
		Where(sq.Eq{"f_branch": riskType.Branch}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}

	_, err = tx.Exec(sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Update data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (rta *riskTypeAccess) UpdateRiskTypes(ctx context.Context, tx *sql.Tx,
	riskTypes []*interfaces.RiskType) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "UpdateRiskTypes")
	defer span.End()
	span.SetAttributes(attr.Int("risk_type_count", len(riskTypes)))

	if len(riskTypes) == 0 {
		return nil
	}
	knID, branch := riskTypes[0].KNID, riskTypes[0].Branch
	for _, riskType := range riskTypes {
		if riskType.KNID != knID || riskType.Branch != branch {
			return fmt.Errorf("batch update risk types must have one knowledge network and branch")
		}
	}
	columns := []string{
		"f_name", "f_comment", "f_tags", "f_icon", "f_color", "f_bkn_raw_content",
		"f_updater", "f_updater_type", "f_update_time",
	}
	for start := 0; start < len(riskTypes); {
		end := start
		seenIDs := make(map[string]struct{}, min(riskTypeUpdateBatchSize, len(riskTypes)-start))
		for end < len(riskTypes) && end-start < riskTypeUpdateBatchSize {
			if _, duplicate := seenIDs[riskTypes[end].RTID]; duplicate {
				break
			}
			seenIDs[riskTypes[end].RTID] = struct{}{}
			end++
		}
		batch := riskTypes[start:end]
		valuesByRow := make([][]any, 0, len(batch))
		for _, riskType := range batch {
			valuesByRow = append(valuesByRow, riskTypeUpdateValues(riskType))
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
			for rowIndex, riskType := range batch {
				statement.WriteString(" WHEN ? THEN ?")
				args = append(args, riskType.RTID, valuesByRow[rowIndex][columnIndex])
			}
			statement.WriteString(" ELSE ")
			statement.WriteString(column)
			statement.WriteString(" END")
		}
		statement.WriteString(" WHERE f_kn_id = ? AND f_branch = ? AND f_id IN (")
		args = append(args, knID, branch)
		for index, riskType := range batch {
			if index > 0 {
				statement.WriteByte(',')
			}
			statement.WriteByte('?')
			args = append(args, riskType.RTID)
		}
		statement.WriteByte(')')
		sqlStr := statement.String()
		otellog.LogInfo(ctx, common.SafeQuerySummary(sqlStr, len(args)))
		if _, err := tx.Exec(sqlStr, args...); err != nil {
			span.SetStatus(codes.Error, "Update data failed")
			return err
		}
		start = end
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func riskTypeUpdateValues(riskType *interfaces.RiskType) []any {
	return []any{
		riskType.RTName, riskType.Comment, libCommon.TagSlice2TagString(riskType.Tags), riskType.Icon,
		riskType.Color, riskType.BKNRawContent, riskType.Updater.ID, riskType.Updater.Type,
		riskType.UpdateTime,
	}
}

func (rta *riskTypeAccess) DeleteRiskTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, rtIDs []string) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "DeleteRiskTypesByIDs")
	defer span.End()

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
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}

	result, err := tx.Exec(sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Delete data failed")
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "Get RowsAffected failed")
		return 0, err
	}
	span.SetStatus(codes.Ok, "")
	return rowsAffected, nil
}

func (rta *riskTypeAccess) GetAllRiskTypesByKnID(ctx context.Context, knID string, branch string) ([]*interfaces.RiskType, error) {
	return rta.ListRiskTypes(ctx, interfaces.RiskTypesQueryParams{
		KNID:   knID,
		Branch: branch,
	})
}

func (rta *riskTypeAccess) DeleteRiskTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "DeleteRiskTypesByKnID")
	defer span.End()

	sqlStr, vals, err := sq.Delete(RT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}

	result, err := tx.Exec(sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Delete data failed")
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "Get RowsAffected failed")
		return 0, err
	}
	span.SetStatus(codes.Ok, "")
	return rowsAffected, nil
}

func processRiskTypeQueryCondition(query interfaces.RiskTypesQueryParams, subBuilder sq.SelectBuilder) sq.SelectBuilder {
	if query.NamePattern != "" {
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
		subBuilder = subBuilder.Where(sq.Eq{"f_branch": interfaces.MAIN_BRANCH})
	}
	return subBuilder
}
