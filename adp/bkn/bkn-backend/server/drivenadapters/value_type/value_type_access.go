// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package value_type

import (
	"context"
	"database/sql"
	"sync"

	sq "github.com/Masterminds/squirrel"
	"github.com/bytedance/sonic"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	VT_TABLE_NAME = "t_value_type"
	// valueTypeInsertBatchSize keeps one INSERT well under the placeholder limit.
	valueTypeInsertBatchSize = 200
)

var (
	vtAccessOnce sync.Once
	vtAccess     interfaces.ValueTypeAccess
)

var valueTypeColumns = []string{
	"f_id", "f_name", "f_comment", "f_tags", "f_icon", "f_color", "f_bkn_raw_content",
	"f_kn_id", "f_branch", "f_base_type", "f_definition",
	"f_creator", "f_creator_type", "f_create_time", "f_updater", "f_updater_type", "f_update_time",
}

type valueTypeAccess struct {
	db *sql.DB
}

func NewValueTypeAccess(appSetting *common.AppSetting) interfaces.ValueTypeAccess {
	vtAccessOnce.Do(func() {
		vtAccess = &valueTypeAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return vtAccess
}

func (vta *valueTypeAccess) GetValueTypeIdentitiesByIDsOrNames(ctx context.Context, knID string, branch string,
	ids, names []string) ([]*interfaces.ValueType, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetValueTypeIdentitiesByIDsOrNames")
	defer span.End()

	if len(ids) == 0 && len(names) == 0 {
		return nil, nil
	}
	sqlStr, vals, err := sq.Select("f_id", "f_name").From(VT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Or{sq.Eq{"f_id": ids}, sq.Eq{"f_name": names}}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	rows, err := vta.db.Query(sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var valueTypes []*interfaces.ValueType
	for rows.Next() {
		vt := &interfaces.ValueType{}
		if err = rows.Scan(&vt.VTID, &vt.VTName); err != nil {
			span.SetStatus(codes.Error, "Scan data failed")
			return nil, err
		}
		valueTypes = append(valueTypes, vt)
	}
	if err = rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Iterate rows failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return valueTypes, nil
}

func (vta *valueTypeAccess) CreateValueTypes(ctx context.Context, tx *sql.Tx, valueTypes []*interfaces.ValueType) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CreateValueTypes")
	defer span.End()

	for start := 0; start < len(valueTypes); start += valueTypeInsertBatchSize {
		end := min(start+valueTypeInsertBatchSize, len(valueTypes))
		builder := sq.Insert(VT_TABLE_NAME).Columns(valueTypeColumns...)
		for _, vt := range valueTypes[start:end] {
			definition, err := marshalDefinition(vt.ValueTypeDefinition)
			if err != nil {
				span.SetStatus(codes.Error, "Marshal definition failed")
				return err
			}
			builder = builder.Values(
				vt.VTID, vt.VTName, vt.Comment, libCommon.TagSlice2TagString(vt.Tags), vt.Icon, vt.Color, vt.BKNRawContent,
				vt.KNID, vt.Branch, vt.BaseType, definition,
				vt.Creator.ID, vt.Creator.Type, vt.CreateTime, vt.Updater.ID, vt.Updater.Type, vt.UpdateTime,
			)
		}
		sqlStr, vals, err := builder.ToSql()
		if err != nil {
			span.SetStatus(codes.Error, "Build sql failed")
			return err
		}
		if _, err = tx.Exec(sqlStr, vals...); err != nil {
			span.SetStatus(codes.Error, "Insert data failed")
			return err
		}
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// ListValueTypes returns every value type the query matches, in its sort order. A knowledge
// network holds few value types, so the service pages them in memory.
func (vta *valueTypeAccess) ListValueTypes(ctx context.Context, query interfaces.ValueTypesQueryParams) ([]*interfaces.ValueType, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ListValueTypes")
	defer span.End()

	builder := sq.Select(valueTypeColumns...).From(VT_TABLE_NAME).Where(sq.Eq{"f_kn_id": query.KNID})
	branch := query.Branch
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	builder = builder.Where(sq.Eq{"f_branch": branch})
	if query.NamePattern != "" {
		builder = builder.Where(sq.Expr("(instr(f_name, ?) > 0 OR instr(f_id, ?) > 0)", query.NamePattern, query.NamePattern))
	}
	if query.Tag != "" {
		builder = builder.Where(sq.Expr("instr(f_tags, ?) > 0", `"`+query.Tag+`"`))
	}
	if query.Sort != "" {
		dir := query.Direction
		if dir == "" {
			dir = interfaces.DESC_DIRECTION
		}
		orderBy, err := common.SafeOrderBy(query.Sort, dir)
		if err != nil {
			return nil, err
		}
		builder = builder.OrderBy(orderBy, "f_id")
	} else {
		builder = builder.OrderBy("f_id")
	}
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	valueTypes, err := vta.queryIn(nil, sqlStr, vals)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return valueTypes, nil
}

func (vta *valueTypeAccess) GetValueTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, ids []string) ([]*interfaces.ValueType, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetValueTypesByIDs")
	defer span.End()

	if len(ids) == 0 {
		return []*interfaces.ValueType{}, nil
	}
	sqlStr, vals, err := sq.Select(valueTypeColumns...).From(VT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": ids}).
		OrderBy("f_id").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	valueTypes, err := vta.queryIn(tx, sqlStr, vals)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return valueTypes, nil
}

func (vta *valueTypeAccess) UpdateValueType(ctx context.Context, tx *sql.Tx, vt *interfaces.ValueType) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "UpdateValueType")
	defer span.End()

	definition, err := marshalDefinition(vt.ValueTypeDefinition)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal definition failed")
		return err
	}
	sqlStr, vals, err := sq.Update(VT_TABLE_NAME).
		SetMap(map[string]any{
			"f_name":            vt.VTName,
			"f_comment":         vt.Comment,
			"f_tags":            libCommon.TagSlice2TagString(vt.Tags),
			"f_icon":            vt.Icon,
			"f_color":           vt.Color,
			"f_bkn_raw_content": vt.BKNRawContent,
			"f_base_type":       vt.BaseType,
			"f_definition":      definition,
			"f_updater":         vt.Updater.ID,
			"f_updater_type":    vt.Updater.Type,
			"f_update_time":     vt.UpdateTime,
		}).
		Where(sq.Eq{"f_id": vt.VTID}).
		Where(sq.Eq{"f_kn_id": vt.KNID}).
		Where(sq.Eq{"f_branch": vt.Branch}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if _, err = tx.Exec(sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Update data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (vta *valueTypeAccess) DeleteValueTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, ids []string) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "DeleteValueTypesByIDs")
	defer span.End()

	if len(ids) == 0 {
		return 0, nil
	}
	return vta.delete(tx, sq.Delete(VT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": ids}))
}

func (vta *valueTypeAccess) DeleteValueTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "DeleteValueTypesByKnID")
	defer span.End()

	return vta.delete(tx, sq.Delete(VT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}))
}

func (vta *valueTypeAccess) delete(tx *sql.Tx, builder sq.DeleteBuilder) (int64, error) {
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		return 0, err
	}
	result, err := tx.Exec(sqlStr, vals...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// queryIn reads value types inside tx when there is one, so an import sees the value types it has
// just written.
func (vta *valueTypeAccess) queryIn(tx *sql.Tx, sqlStr string, vals []any) ([]*interfaces.ValueType, error) {
	var rows *sql.Rows
	var err error
	if tx != nil {
		rows, err = tx.Query(sqlStr, vals...)
	} else {
		rows, err = vta.db.Query(sqlStr, vals...)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	valueTypes := []*interfaces.ValueType{}
	for rows.Next() {
		vt := &interfaces.ValueType{ModuleType: interfaces.MODULE_TYPE_VALUE_TYPE}
		var tags string
		var definition []byte
		if err = rows.Scan(
			&vt.VTID, &vt.VTName, &vt.Comment, &tags, &vt.Icon, &vt.Color, &vt.BKNRawContent,
			&vt.KNID, &vt.Branch, &vt.BaseType, &definition,
			&vt.Creator.ID, &vt.Creator.Type, &vt.CreateTime, &vt.Updater.ID, &vt.Updater.Type, &vt.UpdateTime,
		); err != nil {
			return nil, err
		}
		vt.Tags = libCommon.TagString2TagSlice(tags)
		if err = common.UnmarshalStoredJSON(definition, &vt.ValueTypeDefinition); err != nil {
			return nil, err
		}
		valueTypes = append(valueTypes, vt)
	}
	return valueTypes, rows.Err()
}

func marshalDefinition(definition interfaces.ValueTypeDefinition) (string, error) {
	b, err := sonic.Marshal(definition)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
