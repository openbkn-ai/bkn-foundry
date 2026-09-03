// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package capability_binding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

const (
	CAPABILITY_BINDING_TABLE_NAME = "t_kn_capability_binding"
)

var (
	capabilityBindingAccessOnce sync.Once
	capabilityBindingAccessInst interfaces.CapabilityBindingAccess
)

type capabilityBindingAccess struct {
	appSetting *common.AppSetting
	db         *sql.DB
}

func NewCapabilityBindingAccess(appSetting *common.AppSetting) interfaces.CapabilityBindingAccess {
	capabilityBindingAccessOnce.Do(func() {
		capabilityBindingAccessInst = &capabilityBindingAccess{
			appSetting: appSetting,
			db:         libdb.NewDB(&appSetting.DBSetting),
		}
	})
	return capabilityBindingAccessInst
}

func bindingSelectColumns() []string {
	return []string{
		"f_id",
		"f_kn_id",
		"f_branch",
		"f_capability_type",
		"f_owner_id",
		"f_capability_id",
		"f_bound_as_box",
		"f_comment",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	}
}

func scanBindingFromRow(scanner interface {
	Scan(dest ...any) error
}) (*interfaces.CapabilityBinding, error) {
	binding := &interfaces.CapabilityBinding{}
	var boundAsBox int
	err := scanner.Scan(
		&binding.ID,
		&binding.KNID,
		&binding.Branch,
		&binding.CapabilityType,
		&binding.OwnerID,
		&binding.CapabilityID,
		&boundAsBox,
		&binding.Comment,
		&binding.Creator.ID,
		&binding.Creator.Type,
		&binding.CreateTime,
		&binding.Updater.ID,
		&binding.Updater.Type,
		&binding.UpdateTime,
	)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			logger.Errorf("capability binding row scan error: %v", common.SafeErrorSummary(err))
		}
		return nil, err
	}
	binding.BoundAsBox = boundAsBox != 0
	return binding, nil
}

func processBindingQueryCondition(query interfaces.CapabilityBindingsQueryParams, builder sq.SelectBuilder) sq.SelectBuilder {
	if query.KNID != "" {
		builder = builder.Where(sq.Eq{"f_kn_id": query.KNID})
	}
	if query.Branch != "" {
		builder = builder.Where(sq.Eq{"f_branch": query.Branch})
	} else {
		builder = builder.Where(sq.Eq{"f_branch": interfaces.MAIN_BRANCH})
	}
	if query.CapabilityType != "" {
		builder = builder.Where(sq.Eq{"f_capability_type": query.CapabilityType})
	}
	if query.OwnerID != "" {
		builder = builder.Where(sq.Eq{"f_owner_id": query.OwnerID})
	}
	if len(query.CapabilityIDs) > 0 {
		builder = builder.Where(sq.Eq{"f_capability_id": query.CapabilityIDs})
	}
	return builder
}

func (ca *capabilityBindingAccess) CreateBindings(ctx context.Context, tx *sql.Tx, bindings []*interfaces.CapabilityBinding) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CreateCapabilityBindings")
	defer span.End()

	if len(bindings) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	builder := sq.Insert(CAPABILITY_BINDING_TABLE_NAME).Columns(bindingSelectColumns()...)
	for _, binding := range bindings {
		boundAsBox := 0
		if binding.BoundAsBox {
			boundAsBox = 1
		}
		builder = builder.Values(
			binding.ID,
			binding.KNID,
			binding.Branch,
			binding.CapabilityType,
			binding.OwnerID,
			binding.CapabilityID,
			boundAsBox,
			binding.Comment,
			binding.Creator.ID,
			binding.Creator.Type,
			binding.CreateTime,
			binding.Updater.ID,
			binding.Updater.Type,
			binding.UpdateTime,
		)
	}
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		logger.Errorf("CreateBindings build sql error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return err
	}
	if tx != nil {
		_, err = tx.Exec(sqlStr, vals...)
	} else {
		_, err = ca.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		logger.Errorf("CreateBindings insert error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (ca *capabilityBindingAccess) GetBindingByID(ctx context.Context, knID, branch, bindingID string) (*interfaces.CapabilityBinding, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetCapabilityBindingByID")
	defer span.End()

	sqlStr, vals, err := sq.Select(bindingSelectColumns()...).
		From(CAPABILITY_BINDING_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": bindingID}).
		ToSql()
	if err != nil {
		logger.Errorf("GetBindingByID build sql error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, err
	}
	binding, err := scanBindingFromRow(ca.db.QueryRow(sqlStr, vals...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			span.SetStatus(codes.Ok, "")
			return nil, nil
		}
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return binding, nil
}

func (ca *capabilityBindingAccess) GetBindingByCapability(ctx context.Context, knID, branch, capabilityType, ownerID,
	capabilityID string) (*interfaces.CapabilityBinding, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetCapabilityBindingByCapability")
	defer span.End()

	sqlStr, vals, err := sq.Select(bindingSelectColumns()...).
		From(CAPABILITY_BINDING_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_capability_type": capabilityType}).
		Where(sq.Eq{"f_owner_id": ownerID}).
		Where(sq.Eq{"f_capability_id": capabilityID}).
		ToSql()
	if err != nil {
		logger.Errorf("GetBindingByCapability build sql error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, err
	}
	binding, err := scanBindingFromRow(ca.db.QueryRow(sqlStr, vals...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			span.SetStatus(codes.Ok, "")
			return nil, nil
		}
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return binding, nil
}

func (ca *capabilityBindingAccess) DeleteBindingsByIDs(ctx context.Context, tx *sql.Tx, knID, branch string,
	bindingIDs []string) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "DeleteCapabilityBindingsByIDs")
	defer span.End()

	if len(bindingIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return 0, nil
	}
	sqlStr, vals, err := sq.Delete(CAPABILITY_BINDING_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		Where(sq.Eq{"f_id": bindingIDs}).
		ToSql()
	if err != nil {
		logger.Errorf("DeleteBindingsByIDs build sql error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return 0, err
	}
	var ret sql.Result
	if tx != nil {
		ret, err = tx.Exec(sqlStr, vals...)
	} else {
		ret, err = ca.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		logger.Errorf("DeleteBindingsByIDs delete error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return 0, err
	}
	rowsAffected, err := ret.RowsAffected()
	if err != nil {
		logger.Errorf("DeleteBindingsByIDs RowsAffected error: requested_count=%d, %s",
			len(bindingIDs), common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return 0, err
	}
	// Fewer rows than requested means some IDs were already gone; deletion still happened,
	// so this is reported to the caller as a count rather than raised as an error.
	if rowsAffected != int64(len(bindingIDs)) {
		logger.Warnf("Delete capability bindings affected unexpected row count: requested_count=%d, rows=%d",
			len(bindingIDs), rowsAffected)
	}
	span.SetStatus(codes.Ok, "")
	return rowsAffected, nil
}

func (ca *capabilityBindingAccess) ListBindings(ctx context.Context,
	query interfaces.CapabilityBindingsQueryParams) ([]*interfaces.CapabilityBinding, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ListCapabilityBindings")
	defer span.End()

	builder := processBindingQueryCondition(query, sq.Select(bindingSelectColumns()...).From(CAPABILITY_BINDING_TABLE_NAME))
	if query.Sort != "" {
		dir := query.Direction
		if dir == "" {
			dir = interfaces.DESC_DIRECTION
		}
		// f_id breaks ties: bindings mounted in one request share f_create_time, and without a
		// tie-breaker the order across page boundaries is unstable — rows repeat or get skipped.
		builder = builder.OrderBy(fmt.Sprintf("%s %s", query.Sort, dir), "f_id ASC")
	}
	if query.Limit > 0 {
		builder = builder.Limit(uint64(query.Limit))
		if query.Offset > 0 {
			builder = builder.Offset(uint64(query.Offset))
		}
	}

	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		logger.Errorf("ListBindings build sql error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, err
	}
	rows, err := ca.db.Query(sqlStr, vals...)
	if err != nil {
		logger.Errorf("ListBindings query error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	bindings := []*interfaces.CapabilityBinding{}
	for rows.Next() {
		binding, err := scanBindingFromRow(rows)
		if err != nil {
			span.SetStatus(codes.Error, common.SafeErrorSummary(err))
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("ListBindings rows error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return bindings, nil
}

func (ca *capabilityBindingAccess) GetBindingsTotal(ctx context.Context,
	query interfaces.CapabilityBindingsQueryParams) (int, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetCapabilityBindingsTotal")
	defer span.End()

	builder := processBindingQueryCondition(query, sq.Select("COUNT(f_id)").From(CAPABILITY_BINDING_TABLE_NAME))
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		logger.Errorf("GetBindingsTotal build sql error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return 0, err
	}
	var total int
	if err = ca.db.QueryRow(sqlStr, vals...).Scan(&total); err != nil {
		logger.Errorf("GetBindingsTotal scan error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return 0, err
	}
	span.SetStatus(codes.Ok, "")
	return total, nil
}

func (ca *capabilityBindingAccess) GetBindingsTotalByType(ctx context.Context, knID, branch string) (map[string]int, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetCapabilityBindingsTotalByType")
	defer span.End()

	sqlStr, vals, err := sq.Select("f_capability_type", "COUNT(f_id)").
		From(CAPABILITY_BINDING_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knID}).
		Where(sq.Eq{"f_branch": branch}).
		GroupBy("f_capability_type").
		ToSql()
	if err != nil {
		logger.Errorf("GetBindingsTotalByType build sql error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, err
	}
	rows, err := ca.db.Query(sqlStr, vals...)
	if err != nil {
		logger.Errorf("GetBindingsTotalByType query error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	totals := map[string]int{}
	for rows.Next() {
		var capabilityType string
		var total int
		if err := rows.Scan(&capabilityType, &total); err != nil {
			logger.Errorf("GetBindingsTotalByType scan error: %v", common.SafeErrorSummary(err))
			span.SetStatus(codes.Error, common.SafeErrorSummary(err))
			return nil, err
		}
		totals[capabilityType] = total
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("GetBindingsTotalByType rows error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return totals, nil
}

func (ca *capabilityBindingAccess) DeleteBindingsByKnID(ctx context.Context, tx *sql.Tx, knID, branch string) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "DeleteCapabilityBindingsByKnID")
	defer span.End()

	builder := sq.Delete(CAPABILITY_BINDING_TABLE_NAME).Where(sq.Eq{"f_kn_id": knID})
	// An empty branch deletes every branch of the network, which is what knowledge-network
	// deletion needs; a non-empty branch stays scoped to that branch.
	if branch != "" {
		builder = builder.Where(sq.Eq{"f_branch": branch})
	}
	sqlStr, vals, err := builder.ToSql()
	if err != nil {
		logger.Errorf("DeleteBindingsByKnID build sql error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return 0, err
	}
	var ret sql.Result
	if tx != nil {
		ret, err = tx.Exec(sqlStr, vals...)
	} else {
		ret, err = ca.db.Exec(sqlStr, vals...)
	}
	if err != nil {
		logger.Errorf("DeleteBindingsByKnID delete error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return 0, err
	}
	rowsAffected, err := ret.RowsAffected()
	if err != nil {
		logger.Errorf("DeleteBindingsByKnID RowsAffected error: %v", common.SafeErrorSummary(err))
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return 0, err
	}
	span.SetStatus(codes.Ok, "")
	return rowsAffected, nil
}
