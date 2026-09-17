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

const (
	tableName                 = "t_kn_proxy_account"
	publishedGrantSourceTable = "t_kn_proxy_published_grant_source"
	maxSnapshotInsertRows     = 500
)

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
	defer func() { _ = rows.Close() }()

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
		"f_sync_status", "f_published_model_version", "f_synced_model_version", "f_pending_model_version",
		"f_sync_generation", "f_last_sync_error", "f_last_grantor_id", "f_lock_owner", "f_lock_until",
		"f_last_sync_started_at", "f_last_sync_succeeded_at", "f_created_at", "f_updated_at",
	).Values(
		mapping.KNID, mapping.ProxyAccountID, mapping.ProxyAccountType, mapping.LifecycleStatus, mapping.Version,
		mapping.SyncStatus, mapping.PublishedModelVersion, mapping.SyncedModelVersion, mapping.PendingModelVersion,
		mapping.SyncGeneration, mapping.LastSyncError, mapping.LastGrantorID, "", int64(0),
		mapping.LastSyncStartedAt, mapping.LastSyncSucceededAt, mapping.CreatedAt, mapping.UpdatedAt,
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

func (a *access) SetPending(ctx context.Context, tx *sql.Tx, knID, modelVersion, grantorID, lockOwner string, updatedAt int64) (int64, error) {
	query, args, err := sq.Update(tableName).SetMap(map[string]any{
		"f_sync_status":           interfaces.KNProxySyncPending,
		"f_pending_model_version": modelVersion,
		"f_last_sync_error":       "",
		"f_last_grantor_id":       grantorID,
		"f_sync_generation":       sq.Expr("f_sync_generation + 1"),
		"f_last_sync_started_at":  updatedAt,
		"f_version":               sq.Expr("f_version + 1"),
		"f_updated_at":            updatedAt,
	}).Where(sq.Eq{
		"f_kn_id": knID, "f_lifecycle_status": interfaces.KNProxyLifecycleActive, "f_lock_owner": lockOwner,
	}).ToSql()
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	if err := requireOneRow(result, "mark knowledge network proxy pending"); err != nil {
		return 0, err
	}
	query, args, err = sq.Select("f_sync_generation").From(tableName).Where(sq.Eq{
		"f_kn_id": knID, "f_lock_owner": lockOwner,
	}).ToSql()
	if err != nil {
		return 0, err
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&generation); err != nil {
		return 0, err
	}
	return generation, nil
}

// ReserveSyncGeneration advances the per-network generation for a lifecycle
// operation that also replaces the full bkn-safe grant set but does not enter
// the normal pending/ready publication state machine (for example deletion).
func (a *access) ReserveSyncGeneration(ctx context.Context, knID, lockOwner string, updatedAt int64) (int64, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	query, args, err := sq.Update(tableName).SetMap(map[string]any{
		"f_sync_generation": sq.Expr("f_sync_generation + 1"),
		"f_version":         sq.Expr("f_version + 1"),
		"f_updated_at":      updatedAt,
	}).Where(sq.Eq{"f_kn_id": knID, "f_lock_owner": lockOwner}).ToSql()
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	if err := requireOneRow(result, "reserve knowledge network proxy sync generation"); err != nil {
		return 0, err
	}
	query, args, err = sq.Select("f_sync_generation").From(tableName).Where(sq.Eq{
		"f_kn_id": knID, "f_lock_owner": lockOwner,
	}).ToSql()
	if err != nil {
		return 0, err
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&generation); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return generation, nil
}

func (a *access) MarkSyncFailed(ctx context.Context, knID string, generation int64, lockOwner, lastError string, updatedAt int64) (bool, error) {
	query, args, err := sq.Update(tableName).SetMap(map[string]any{
		"f_sync_status":     interfaces.KNProxySyncFailed,
		"f_last_sync_error": lastError,
		"f_version":         sq.Expr("f_version + 1"),
		"f_updated_at":      updatedAt,
	}).Where(sq.Eq{
		"f_kn_id": knID, "f_sync_status": interfaces.KNProxySyncPending,
		"f_sync_generation": generation, "f_lock_owner": lockOwner,
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

func (a *access) ReplacePublishedSnapshotAndMarkReady(ctx context.Context, knID string, generation int64,
	lockOwner, snapshotVersion string, sources []interfaces.ProxyGrantSourceSpec, updatedAt int64) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	deleteQuery, deleteArgs, err := sq.Delete(publishedGrantSourceTable).Where(sq.Eq{"f_kn_id": knID}).ToSql()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, deleteQuery, deleteArgs...); err != nil {
		return err
	}
	for start := 0; start < len(sources); start += maxSnapshotInsertRows {
		end := min(start+maxSnapshotInsertRows, len(sources))
		insert := sq.Insert(publishedGrantSourceTable).Columns(
			"f_kn_id", "f_binding_type", "f_binding_id", "f_resource_type", "f_resource_id", "f_operation",
			"f_source_type", "f_source_id", "f_created_at", "f_updated_at",
		)
		for _, source := range sources[start:end] {
			insert = insert.Values(
				source.KNID, source.BindingType, source.BindingID, source.ResourceType, source.ResourceID, source.Operation,
				source.SourceType, source.SourceID, updatedAt, updatedAt,
			)
		}
		insertQuery, insertArgs, err := insert.ToSql()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, insertQuery, insertArgs...); err != nil {
			return err
		}
	}
	updateQuery, updateArgs, err := sq.Update(tableName).SetMap(map[string]any{
		"f_sync_status":             interfaces.KNProxySyncReady,
		"f_published_model_version": snapshotVersion,
		"f_synced_model_version":    snapshotVersion,
		"f_pending_model_version":   "",
		"f_last_sync_error":         "",
		"f_last_sync_succeeded_at":  updatedAt,
		"f_version":                 sq.Expr("f_version + 1"),
		"f_updated_at":              updatedAt,
	}).Where(sq.Eq{
		"f_kn_id": knID, "f_sync_status": interfaces.KNProxySyncPending,
		"f_sync_generation": generation, "f_lock_owner": lockOwner,
	}).ToSql()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, updateQuery, updateArgs...)
	if err != nil {
		return err
	}
	if err := requireOneRow(result, "mark published knowledge network proxy snapshot ready"); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePublishedSnapshot removes the persisted authorization source snapshot
// after bkn-safe has successfully revoked a proxy's full grant set. Keeping it
// would permit a later restore to resolve stale bindings before republishing.
func (a *access) DeletePublishedSnapshot(ctx context.Context, knID string) error {
	query, args, err := sq.Delete(publishedGrantSourceTable).Where(sq.Eq{"f_kn_id": knID}).ToSql()
	if err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx, query, args...)
	return err
}

func (a *access) ResolvePublishedBinding(ctx context.Context, knID string,
	binding interfaces.KNProxyBinding) (*interfaces.KNProxyBinding, error) {
	where := sq.Eq{
		"f_kn_id": knID, "f_binding_type": binding.ChildType, "f_binding_id": binding.ChildID,
		"f_resource_id": binding.TargetID, "f_operation": binding.Operation,
	}
	if binding.TargetType != "" {
		where["f_resource_type"] = binding.TargetType
	}
	query, args, err := sq.Select(
		"f_binding_type", "f_binding_id", "f_resource_type", "f_resource_id", "f_operation",
	).From(publishedGrantSourceTable).Where(where).Limit(2).ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	resolved := &interfaces.KNProxyBinding{}
	if err := rows.Scan(&resolved.ChildType, &resolved.ChildID, &resolved.TargetType,
		&resolved.TargetID, &resolved.Operation); err != nil {
		return nil, err
	}
	if rows.Next() {
		return nil, errors.New("published proxy binding is ambiguous")
	}
	return resolved, rows.Err()
}

func (a *access) ResolvePublishedBindings(ctx context.Context, knID string,
	bindings []interfaces.KNProxyBinding) ([]interfaces.KNProxyBinding, error) {
	if len(bindings) == 0 {
		return []interfaces.KNProxyBinding{}, nil
	}
	query, args, err := sq.Select(
		"f_binding_type", "f_binding_id", "f_resource_type", "f_resource_id", "f_operation",
	).From(publishedGrantSourceTable).Where(sq.Eq{"f_kn_id": knID}).ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	published := make(map[string]struct{})
	for rows.Next() {
		var source interfaces.KNProxyBinding
		if err := rows.Scan(&source.ChildType, &source.ChildID, &source.TargetType, &source.TargetID, &source.Operation); err != nil {
			return nil, err
		}
		published[proxyBindingKey(source)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	resolved := make([]interfaces.KNProxyBinding, 0, len(bindings))
	for _, binding := range bindings {
		if _, ok := published[proxyBindingKey(binding)]; ok {
			resolved = append(resolved, binding)
		}
	}
	return resolved, nil
}

func proxyBindingKey(binding interfaces.KNProxyBinding) string {
	return binding.ChildType + "\x00" + binding.ChildID + "\x00" + binding.TargetType + "\x00" +
		binding.TargetID + "\x00" + binding.Operation
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

// RenewLock extends only a currently held, unexpired lease. Unlike
// TryAcquireLock, it must never reacquire a lease after another publisher has
// owned it, even if that publisher has since released the lock.
func (a *access) RenewLock(ctx context.Context, knID, owner string, now, lockUntil int64) (bool, error) {
	query, args, err := sq.Update(tableName).SetMap(map[string]any{
		"f_lock_until": lockUntil,
		"f_updated_at": now,
	}).Where(sq.Eq{
		"f_kn_id": knID, "f_lock_owner": owner,
	}).Where(sq.Gt{"f_lock_until": now}).ToSql()
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
		"f_sync_status", "f_published_model_version", "f_synced_model_version", "f_pending_model_version",
		"f_sync_generation", "f_last_sync_error", "f_last_grantor_id", "f_lock_owner", "f_lock_until",
		"f_last_sync_started_at", "f_last_sync_succeeded_at", "f_created_at", "f_updated_at",
	}
}

func scanMapping(row rowScanner) (*interfaces.KNProxyAccount, error) {
	var mapping interfaces.KNProxyAccount
	err := row.Scan(
		&mapping.KNID, &mapping.ProxyAccountID, &mapping.ProxyAccountType, &mapping.LifecycleStatus, &mapping.Version,
		&mapping.SyncStatus, &mapping.PublishedModelVersion, &mapping.SyncedModelVersion, &mapping.PendingModelVersion,
		&mapping.SyncGeneration, &mapping.LastSyncError, &mapping.LastGrantorID, &mapping.LockOwner, &mapping.LockUntil,
		&mapping.LastSyncStartedAt, &mapping.LastSyncSucceededAt, &mapping.CreatedAt, &mapping.UpdatedAt,
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
