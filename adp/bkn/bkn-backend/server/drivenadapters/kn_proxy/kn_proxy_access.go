// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_proxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"bkn-backend/interfaces"
)

const tableName = "t_kn_proxy_account"

type access struct {
	db *sql.DB
}

// NewAccess creates the BKN-side proxy mapping repository.
func NewAccess(db *sql.DB) interfaces.KNProxyAccess {
	return &access{db: db}
}

func (a *access) Get(ctx context.Context, knID string) (*interfaces.KNProxyAccount, error) {
	query, args, err := sq.Select(proxyColumns()...).From(tableName).
		Where(sq.Eq{"f_kn_id": knID}).ToSql()
	if err != nil {
		return nil, err
	}
	return scanMapping(a.db.QueryRowContext(ctx, query, args...))
}

func (a *access) List(ctx context.Context) ([]*interfaces.KNProxyAccount, error) {
	query, args, err := sq.Select(proxyColumns()...).From(tableName).OrderBy("f_kn_id ASC").ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*interfaces.KNProxyAccount, 0)
	for rows.Next() {
		mapping, err := scanMapping(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, mapping)
	}
	return result, rows.Err()
}

func (a *access) Ensure(ctx context.Context, mapping *interfaces.KNProxyAccount) (*interfaces.KNProxyAccount, bool, error) {
	if existing, err := a.Get(ctx, mapping.KNID); err != nil {
		return nil, false, err
	} else if existing != nil {
		if existing.ProxyAccountID != mapping.ProxyAccountID {
			return nil, false, fmt.Errorf("knowledge network proxy mapping conflict")
		}
		return existing, false, nil
	}

	query, args, err := sq.Insert(tableName).Columns(
		"f_kn_id", "f_proxy_account_id", "f_proxy_account_type", "f_lifecycle_status", "f_version",
		"f_sync_status", "f_published_model_version", "f_synced_model_version", "f_last_sync_error",
		"f_last_grantor_id", "f_lock_owner", "f_lock_until", "f_created_at", "f_updated_at",
	).Values(
		mapping.KNID, mapping.ProxyAccountID, mapping.ProxyAccountType, mapping.LifecycleStatus, mapping.Version,
		mapping.SyncStatus, mapping.PublishedModelVersion, mapping.SyncedModelVersion, mapping.LastSyncError,
		mapping.LastGrantorID, "", int64(0), mapping.CreatedAt, mapping.UpdatedAt,
	).ToSql()
	if err != nil {
		return nil, false, err
	}
	if _, err = a.db.ExecContext(ctx, query, args...); err == nil {
		copy := *mapping
		return &copy, true, nil
	}

	// A concurrent creator may have won either unique key. Resolve the durable
	// mapping before returning the insert error so identical retries are safe.
	existing, getErr := a.Get(ctx, mapping.KNID)
	if getErr == nil && existing != nil && existing.ProxyAccountID == mapping.ProxyAccountID {
		return existing, false, nil
	}
	return nil, false, err
}

func (a *access) SetPending(ctx context.Context, tx *sql.Tx, knID, modelVersion, grantorID string, updatedAt int64) error {
	query, args, err := sq.Update(tableName).SetMap(map[string]any{
		"f_sync_status":             interfaces.KNProxySyncPending,
		"f_published_model_version": modelVersion,
		"f_last_sync_error":         "",
		"f_last_grantor_id":         grantorID,
		"f_version":                 sq.Expr("f_version + 1"),
		"f_updated_at":              updatedAt,
	}).Where(sq.Eq{"f_kn_id": knID, "f_lifecycle_status": interfaces.KNProxyLifecycleActive}).ToSql()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	return requireOneRow(result, "mark knowledge network proxy pending")
}

func (a *access) SetSyncResult(ctx context.Context, knID, modelVersion, syncStatus, syncedVersion, lastError string, updatedAt int64) (bool, error) {
	query, args, err := sq.Update(tableName).SetMap(map[string]any{
		"f_sync_status":          syncStatus,
		"f_synced_model_version": syncedVersion,
		"f_last_sync_error":      lastError,
		"f_version":              sq.Expr("f_version + 1"),
		"f_updated_at":           updatedAt,
	}).Where(sq.Eq{"f_kn_id": knID, "f_published_model_version": modelVersion}).ToSql()
	if err != nil {
		return false, err
	}
	result, err := a.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (a *access) SetLifecycle(ctx context.Context, knID, lifecycleStatus string, updatedAt int64) error {
	query, args, err := sq.Update(tableName).SetMap(map[string]any{
		"f_lifecycle_status": lifecycleStatus,
		"f_version":          sq.Expr("f_version + 1"),
		"f_updated_at":       updatedAt,
	}).Where(sq.Eq{"f_kn_id": knID}).ToSql()
	if err != nil {
		return err
	}
	result, err := a.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	return requireOneRow(result, "update knowledge network proxy lifecycle")
}

func (a *access) TryAcquireLock(ctx context.Context, knID, owner string, now, lockUntil int64) (bool, error) {
	query, args, err := sq.Update(tableName).SetMap(map[string]any{
		"f_lock_owner": owner,
		"f_lock_until": lockUntil,
		"f_updated_at": now,
	}).Where(sq.Eq{"f_kn_id": knID}).Where(sq.Or{
		sq.Eq{"f_lock_owner": ""},
		sq.Eq{"f_lock_owner": owner},
		sq.LtOrEq{"f_lock_until": now},
	}).ToSql()
	if err != nil {
		return false, err
	}
	result, err := a.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (a *access) ReleaseLock(ctx context.Context, knID, owner string, updatedAt int64) error {
	query, args, err := sq.Update(tableName).SetMap(map[string]any{
		"f_lock_owner": "",
		"f_lock_until": int64(0),
		"f_updated_at": updatedAt,
	}).Where(sq.Eq{"f_kn_id": knID, "f_lock_owner": owner}).ToSql()
	if err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx, query, args...)
	return err
}

func (a *access) ListProxyConflicts(ctx context.Context) (map[string][]string, error) {
	mappings, err := a.List(ctx)
	if err != nil {
		return nil, err
	}
	byProxy := make(map[string][]string, len(mappings))
	for _, mapping := range mappings {
		byProxy[mapping.ProxyAccountID] = append(byProxy[mapping.ProxyAccountID], mapping.KNID)
	}
	conflicts := make(map[string][]string)
	for proxyID, knIDs := range byProxy {
		if len(knIDs) > 1 {
			conflicts[proxyID] = knIDs
		}
	}
	return conflicts, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func proxyColumns() []string {
	return []string{
		"f_kn_id", "f_proxy_account_id", "f_proxy_account_type", "f_lifecycle_status", "f_version",
		"f_sync_status", "f_published_model_version", "f_synced_model_version", "f_last_sync_error",
		"f_last_grantor_id", "f_lock_owner", "f_lock_until", "f_created_at", "f_updated_at",
	}
}

func scanMapping(row rowScanner) (*interfaces.KNProxyAccount, error) {
	var mapping interfaces.KNProxyAccount
	err := row.Scan(
		&mapping.KNID, &mapping.ProxyAccountID, &mapping.ProxyAccountType, &mapping.LifecycleStatus, &mapping.Version,
		&mapping.SyncStatus, &mapping.PublishedModelVersion, &mapping.SyncedModelVersion, &mapping.LastSyncError,
		&mapping.LastGrantorID, &mapping.LockOwner, &mapping.LockUntil, &mapping.CreatedAt, &mapping.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mapping, nil
}

func requireOneRow(result sql.Result, operation string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%s affected %d rows", operation, rows)
	}
	return nil
}
