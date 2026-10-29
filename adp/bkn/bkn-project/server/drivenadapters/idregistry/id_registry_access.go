// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package idregistry

import (
	"context"
	"database/sql"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const ID_TABLE_NAME = "t_bkn_project_id"

// batchSize keeps a statement well under the 65535 placeholders MySQL allows: a catalog can have
// tens of thousands of fields.
const batchSize = 500

var (
	iraOnce   sync.Once
	iraAccess interfaces.IDRegistryAccess
)

var idColumns = []string{
	"f_project_id", "f_kind", "f_source_key", "f_id", "f_origin", "f_basis", "f_locked",
	"f_create_time", "f_update_time",
}

// replaceFallback is true for a row whose unlocked placeholder the incoming semantic ID replaces.
// MariaDB applies the assignments left to right, so f_origin, which the condition reads, is
// assigned last.
const replaceFallback = "f_locked = 0 AND f_origin = '" + interfaces.ID_ORIGIN_FALLBACK +
	"' AND VALUES(f_origin) = '" + interfaces.ID_ORIGIN_SEMANTIC + "'"

const onDuplicate = "ON DUPLICATE KEY UPDATE " +
	"f_id = IF(" + replaceFallback + ", VALUES(f_id), f_id), " +
	"f_basis = IF(" + replaceFallback + ", VALUES(f_basis), f_basis), " +
	"f_update_time = IF(" + replaceFallback + ", VALUES(f_update_time), f_update_time), " +
	"f_origin = IF(" + replaceFallback + ", VALUES(f_origin), f_origin)"

type idRegistryAccess struct {
	db *sql.DB
}

func NewIDRegistryAccess(appSetting *common.AppSetting) interfaces.IDRegistryAccess {
	iraOnce.Do(func() {
		iraAccess = &idRegistryAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return iraAccess
}

func (ira *idRegistryAccess) ListIDs(ctx context.Context, projectID string) ([]*interfaces.RegisteredID, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ListIDs")
	defer span.End()

	sqlStr, vals, err := sq.Select(idColumns...).From(ID_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID}).
		OrderBy("f_kind", "f_source_key").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	rows, err := ira.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []*interfaces.RegisteredID{}
	for rows.Next() {
		id := &interfaces.RegisteredID{}
		var basis string
		if err := rows.Scan(&id.ProjectID, &id.Kind, &id.SourceKey, &id.ID, &id.Origin, &basis,
			&id.Locked, &id.CreateTime, &id.UpdateTime); err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}
		id.Basis = []byte(basis)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Read rows failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return ids, nil
}

func (ira *idRegistryAccess) RecordIDs(ctx context.Context, ids []*interfaces.RegisteredID) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "RecordIDs")
	defer span.End()

	for start := 0; start < len(ids); start += batchSize {
		builder := sq.Insert(ID_TABLE_NAME).Columns(idColumns...).Suffix(onDuplicate)
		for _, id := range ids[start:min(start+batchSize, len(ids))] {
			basis := string(id.Basis)
			if basis == "" {
				basis = "{}"
			}
			builder = builder.Values(id.ProjectID, id.Kind, id.SourceKey, id.ID, id.Origin, basis,
				id.Locked, id.CreateTime, id.UpdateTime)
		}
		sqlStr, vals, err := builder.ToSql()
		if err != nil {
			span.SetStatus(codes.Error, "Build sql failed")
			return err
		}
		if _, err := ira.db.ExecContext(ctx, sqlStr, vals...); err != nil {
			span.SetStatus(codes.Error, "Insert data failed")
			return err
		}
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (ira *idRegistryAccess) LockIDs(ctx context.Context, projectID string, keys []interfaces.IDKey,
	updateTime int64) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "LockIDs")
	defer span.End()
	return ira.setLocked(ctx, span, projectID, keys, true, updateTime)
}

func (ira *idRegistryAccess) UnlockIDs(ctx context.Context, projectID string, keys []interfaces.IDKey,
	updateTime int64) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "UnlockIDs")
	defer span.End()
	return ira.setLocked(ctx, span, projectID, keys, false, updateTime)
}

// setLocked sets the lock of the project's ids of keys that are not set so already, in batches.
func (ira *idRegistryAccess) setLocked(ctx context.Context, span trace.Span, projectID string,
	keys []interfaces.IDKey, locked bool, updateTime int64) error {
	for start := 0; start < len(keys); start += batchSize {
		match := sq.Or{}
		for _, key := range keys[start:min(start+batchSize, len(keys))] {
			match = append(match, sq.And{sq.Eq{"f_kind": key.Kind}, sq.Eq{"f_source_key": key.SourceKey}})
		}
		sqlStr, vals, err := sq.Update(ID_TABLE_NAME).
			Set("f_locked", locked).
			Set("f_update_time", updateTime).
			Where(sq.Eq{"f_project_id": projectID, "f_locked": !locked}).
			Where(match).
			ToSql()
		if err != nil {
			span.SetStatus(codes.Error, "Build sql failed")
			return err
		}
		if _, err := ira.db.ExecContext(ctx, sqlStr, vals...); err != nil {
			span.SetStatus(codes.Error, "Update data failed")
			return err
		}
	}
	span.SetStatus(codes.Ok, "")
	return nil
}
