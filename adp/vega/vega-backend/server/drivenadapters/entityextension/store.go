// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package entityextension reads and writes t_entity_extension (Issue #382 Option B)
package entityextension

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"

	"vega-backend/common"
)

const tableName = "t_entity_extension"

// Consistent with t_entity_extension.f_entity_kind, distinguish catalog/resource to avoid conflicts with the primary key of the string id extension line
const (
	KindCatalog  = "catalog"
	KindResource = "resource"
)

var (
	storeOnce sync.Once
	st        *Store
)

// Store physical-level extensions row storage
type Store struct {
	db *sql.DB
}

// The NewStore singleton shares the same DB connection pool with catalog/resource access
func NewStore(appSetting *common.AppSetting) *Store {
	storeOnce.Do(func() {
		st = &Store{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return st
}

// Replace replaces all KV under a certain entity in the entire package within the caller's transaction (an empty map indicates the deletion of all rows)
func (s *Store) Replace(ctx context.Context, tx *sql.Tx, kind string, entityID string, kv map[string]string) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	if err := s.deleteByEntityID(ctx, tx, kind, entityID); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for k, v := range kv {
		q, args, err := sq.Insert(tableName).
			Columns("f_entity_kind", "f_entity_id", "f_key", "f_value", "f_create_time", "f_update_time").
			Values(kind, entityID, k, v, now, now).
			ToSql()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) deleteByEntityID(ctx context.Context, tx *sql.Tx, kind string, entityID string) error {
	q, args, err := sq.Delete(tableName).Where(sq.Eq{
		"f_entity_kind": kind,
		"f_entity_id":   entityID,
	}).ToSql()
	if err != nil {
		return err
	}
	if tx != nil {
		_, err = tx.ExecContext(ctx, q, args...)
	} else {
		_, err = s.db.ExecContext(ctx, q, args...)
	}
	return err
}

// DeleteByEntityIDs deletes all extended rows under multiple entities (used for batch deletion of catalogs/resources)
func (s *Store) DeleteByEntityIDs(ctx context.Context, tx *sql.Tx, kind string, entityIDs []string) error {
	if len(entityIDs) == 0 {
		return nil
	}
	q, args, err := sq.Delete(tableName).Where(sq.Eq{
		"f_entity_kind": kind,
		"f_entity_id":   entityIDs,
	}).ToSql()
	if err != nil {
		return err
	}
	if tx != nil {
		_, err = tx.ExecContext(ctx, q, args...)
	} else {
		_, err = s.db.ExecContext(ctx, q, args...)
	}
	return err
}

// GetByEntityID reads a single entity KV and returns an empty map (not nil) when there are no lines.
func (s *Store) GetByEntityID(ctx context.Context, kind string, entityID string) (map[string]string, error) {
	q, args, err := sq.Select("f_key", "f_value").From(tableName).
		Where(sq.Eq{"f_entity_kind": kind, "f_entity_id": entityID}).
		OrderBy("f_key").
		ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// GetByEntityIDs batch reads and returns entityID -> kv
func (s *Store) GetByEntityIDs(ctx context.Context, kind string, entityIDs []string) (map[string]map[string]string, error) {
	res := make(map[string]map[string]string)
	if len(entityIDs) == 0 {
		return res, nil
	}
	q, args, err := sq.Select("f_entity_id", "f_key", "f_value").From(tableName).
		Where(sq.Eq{"f_entity_kind": kind, "f_entity_id": entityIDs}).
		OrderBy("f_entity_id", "f_key").
		ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var eid, k, v string
		if err := rows.Scan(&eid, &k, &v); err != nil {
			return nil, err
		}
		if res[eid] == nil {
			res[eid] = make(map[string]string)
		}
		res[eid][k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

// ApplyJoinsForCatalog appends INNER joins for queries FROM t_catalog to implement multi-pair AND extended filtering
func ApplyJoinsForCatalog(builder sq.SelectBuilder, keys, values []string) sq.SelectBuilder {
	for i := range keys {
		alias := fmt.Sprintf("vex%d", i)
		join := fmt.Sprintf(
			"t_entity_extension %s ON %s.f_entity_kind = ? AND %s.f_entity_id = t_catalog.f_id AND %s.f_key = ? AND %s.f_value = ?",
			alias, alias, alias, alias, alias,
		)
		builder = builder.Join(join, KindCatalog, keys[i], values[i])
	}
	return builder
}

// ApplyJoinsForResource appends extended filters for queries FROM t_resource
func ApplyJoinsForResource(builder sq.SelectBuilder, keys, values []string) sq.SelectBuilder {
	for i := range keys {
		alias := fmt.Sprintf("vex%d", i)
		join := fmt.Sprintf(
			"t_entity_extension %s ON %s.f_entity_kind = ? AND %s.f_entity_id = t_resource.f_id AND %s.f_key = ? AND %s.f_value = ?",
			alias, alias, alias, alias, alias,
		)
		builder = builder.Join(join, KindResource, keys[i], values[i])
	}
	return builder
}

// If keysCSV is not empty, FilterKeys will only retain the listed keys (for include_extension_keys).
func FilterKeys(in map[string]string, keysCSV string) map[string]string {
	if keysCSV == "" || len(in) == 0 {
		return in
	}
	parts := strings.Split(keysCSV, ",")
	allow := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			allow[p] = struct{}{}
		}
	}
	if len(allow) == 0 {
		return in
	}
	out := make(map[string]string)
	for k, v := range in {
		if _, ok := allow[k]; ok {
			out[k] = v
		}
	}
	return out
}
