// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	plannedGrantSourceTable = "t_kn_proxy_planned_grant_source"
	proxyOutboxTable        = "t_kn_proxy_sync_outbox"
	cleanupLockName         = "bkn_proxy_outbox_cleanup"
)

type outboxAccess struct {
	db *sql.DB
}

// NewOutboxAccess creates the durable proxy publication repository.
func NewOutboxAccess(db *sql.DB) interfaces.KNProxyOutboxAccess {
	return &outboxAccess{db: db}
}

func (a *outboxAccess) ListPlannedSources(ctx context.Context, knID string,
	bindings []interfaces.KNProxyBindingRef) ([]interfaces.ProxyGrantSourceSpec, error) {
	if len(bindings) == 0 {
		return []interfaces.ProxyGrantSourceSpec{}, nil
	}
	result := make([]interfaces.ProxyGrantSourceSpec, 0)
	for start := 0; start < len(bindings); start += maxSnapshotInsertRows {
		end := min(start+maxSnapshotInsertRows, len(bindings))
		query, args, err := sq.Select(
			"f_resource_type", "f_resource_id", "f_operation", "f_source_type", "f_source_id",
			"f_kn_id", "f_binding_type", "f_binding_id",
		).From(plannedGrantSourceTable).Where(sq.Eq{"f_kn_id": knID}).
			Where(publishedBindingConditions(bindings[start:end])).
			OrderBy("f_binding_type, f_binding_id, f_source_id, f_resource_type, f_resource_id, f_operation").ToSql()
		if err != nil {
			return nil, err
		}
		rows, err := a.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var source interfaces.ProxyGrantSourceSpec
			if err := rows.Scan(&source.ResourceType, &source.ResourceID, &source.Operation,
				&source.SourceType, &source.SourceID, &source.KNID, &source.BindingType, &source.BindingID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			result = append(result, source)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (a *outboxAccess) ListPlannedSnapshot(ctx context.Context, knID string) ([]interfaces.ProxyGrantSourceSpec, error) {
	query, args, err := sq.Select(
		"f_resource_type", "f_resource_id", "f_operation", "f_source_type", "f_source_id",
		"f_kn_id", "f_binding_type", "f_binding_id",
	).From(plannedGrantSourceTable).Where(sq.Eq{"f_kn_id": knID}).
		OrderBy("f_binding_type, f_binding_id, f_source_id, f_resource_type, f_resource_id, f_operation").ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]interfaces.ProxyGrantSourceSpec, 0)
	for rows.Next() {
		var source interfaces.ProxyGrantSourceSpec
		if err := rows.Scan(&source.ResourceType, &source.ResourceID, &source.Operation,
			&source.SourceType, &source.SourceID, &source.KNID, &source.BindingType, &source.BindingID); err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, rows.Err()
}

func (a *outboxAccess) StageDelta(ctx context.Context, tx *sql.Tx, event *interfaces.KNProxyOutboxEvent,
	lockOwner string, bindings []interfaces.KNProxyBindingRef, planned []interfaces.ProxyGrantSourceSpec,
	updatedAt int64) (int64, error) {
	if tx == nil || event == nil || strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.KNID) == "" ||
		strings.TrimSpace(event.ProxyAccountID) == "" || strings.TrimSpace(event.GrantorID) == "" ||
		strings.TrimSpace(event.TargetVersion) == "" || strings.TrimSpace(lockOwner) == "" {
		return 0, errors.New("invalid proxy outbox stage request")
	}
	for _, binding := range bindings {
		if strings.TrimSpace(binding.BindingType) == "" || strings.TrimSpace(binding.BindingID) == "" {
			return 0, errors.New("invalid proxy outbox binding")
		}
	}
	for _, sources := range [][]interfaces.ProxyGrantSourceSpec{planned, event.Upserts, event.Removals} {
		for _, source := range sources {
			if source.KNID != event.KNID || strings.TrimSpace(source.BindingType) == "" ||
				strings.TrimSpace(source.BindingID) == "" {
				return 0, errors.New("invalid proxy outbox grant source")
			}
		}
	}
	// The separately supplied binding slice and planned snapshot are the rows
	// written in this transaction, so make them authoritative in the payload as
	// well. This prevents an adapter caller from persisting two interpretations
	// of the same generation.
	event.Bindings = append([]interfaces.KNProxyBindingRef(nil), bindings...)
	event.DesiredSources = append([]interfaces.ProxyGrantSourceSpec(nil), planned...)
	query, args, err := sq.Update(tableName).SetMap(map[string]any{
		"f_sync_status":           interfaces.KNProxySyncPending,
		"f_pending_model_version": event.TargetVersion,
		"f_last_sync_error":       "",
		"f_last_grantor_id":       event.GrantorID,
		"f_sync_generation":       sq.Expr("f_sync_generation + 1"),
		"f_last_sync_started_at":  updatedAt,
		"f_version":               sq.Expr("f_version + 1"),
		"f_updated_at":            updatedAt,
	}).Where(sq.Eq{
		"f_kn_id": event.KNID, "f_proxy_account_id": event.ProxyAccountID,
		"f_lifecycle_status": interfaces.KNProxyLifecycleActive, "f_lock_owner": lockOwner,
	}).Where("COALESCE(NULLIF(f_pending_model_version, ''), f_published_model_version) = ?", event.BaseVersion).ToSql()
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	if err := requireOneRow(result, "stage knowledge network proxy delta"); err != nil {
		return 0, err
	}
	query, args, err = sq.Select("f_sync_generation").From(tableName).
		Where(sq.Eq{"f_kn_id": event.KNID, "f_proxy_account_id": event.ProxyAccountID,
			"f_lock_owner": lockOwner}).ToSql()
	if err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&event.Generation); err != nil {
		return 0, err
	}
	if err := replaceBindingSnapshot(ctx, tx, plannedGrantSourceTable, event.KNID, bindings, planned, updatedAt); err != nil {
		return 0, err
	}
	event.Status = interfaces.KNProxyOutboxPending
	event.CreatedAt, event.UpdatedAt = updatedAt, updatedAt
	payload, err := json.Marshal(event)
	if err != nil {
		return 0, err
	}
	query, args, err = sq.Insert(proxyOutboxTable).Columns(
		"f_id", "f_kn_id", "f_proxy_account_id", "f_generation", "f_base_version", "f_target_version",
		"f_status", "f_attempt_count", "f_next_retry_at", "f_lease_owner", "f_lease_until", "f_last_error",
		"f_payload", "f_created_at", "f_updated_at", "f_completed_at",
	).Values(event.ID, event.KNID, event.ProxyAccountID, event.Generation, event.BaseVersion, event.TargetVersion,
		interfaces.KNProxyOutboxPending, 0, updatedAt, "", int64(0), "", payload, updatedAt, updatedAt, int64(0)).ToSql()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return 0, err
	}
	return event.Generation, nil
}

func (a *outboxAccess) ClaimNext(ctx context.Context, owner string, now, leaseUntil int64) (*interfaces.KNProxyOutboxEvent, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	const selectEvent = `SELECT e.f_id, e.f_kn_id, e.f_proxy_account_id, e.f_generation,
       e.f_base_version, e.f_target_version, e.f_payload, e.f_attempt_count, e.f_created_at, e.f_status
FROM t_kn_proxy_sync_outbox e
JOIN t_kn_proxy_account m ON m.f_kn_id = e.f_kn_id
WHERE e.f_generation = m.f_published_generation + 1
  AND e.f_proxy_account_id = m.f_proxy_account_id
  AND m.f_lifecycle_status = 'active'
  AND ((e.f_status IN ('pending', 'retrying', 'completing') AND e.f_next_retry_at <= ? AND e.f_lease_until <= ?)
       OR (e.f_status = 'processing' AND e.f_lease_until <= ?))
ORDER BY m.f_last_sync_succeeded_at, e.f_created_at, e.f_id
LIMIT 1 FOR UPDATE SKIP LOCKED`
	var eventID, claimedStatus string
	var knID, proxyAccountID, baseVersion, targetVersion string
	var generation int64
	var payload []byte
	var attempts int
	var createdAt int64
	err = tx.QueryRowContext(ctx, selectEvent, now, now, now).Scan(&eventID, &knID, &proxyAccountID,
		&generation, &baseVersion, &targetVersion, &payload, &attempts, &createdAt, &claimedStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var event interfaces.KNProxyOutboxEvent
	if err := json.Unmarshal(payload, &event); err != nil ||
		event.ID != eventID || event.KNID != knID || event.ProxyAccountID != proxyAccountID ||
		event.Generation != generation || event.BaseVersion != baseVersion || event.TargetVersion != targetVersion ||
		validateOutboxPayload(&event) != nil {
		lastError := "proxy outbox payload is invalid or does not match indexed identity"
		query, args, buildErr := sq.Update(proxyOutboxTable).SetMap(map[string]any{
			"f_status":        interfaces.KNProxyOutboxDead,
			"f_attempt_count": sq.Expr("f_attempt_count + 1"),
			"f_last_error":    lastError,
			"f_lease_owner":   "",
			"f_lease_until":   int64(0),
			"f_updated_at":    now,
		}).Where(sq.Eq{"f_id": eventID}).ToSql()
		if buildErr != nil {
			return nil, buildErr
		}
		result, execErr := tx.ExecContext(ctx, query, args...)
		if execErr != nil {
			return nil, execErr
		}
		if buildErr = requireOneRow(result, "mark invalid proxy outbox event dead"); buildErr != nil {
			return nil, buildErr
		}
		query, args, buildErr = sq.Update(tableName).SetMap(map[string]any{
			"f_sync_status":     interfaces.KNProxySyncFailed,
			"f_last_sync_error": lastError,
			"f_version":         sq.Expr("f_version + 1"),
			"f_updated_at":      now,
		}).Where(sq.Eq{"f_kn_id": knID, "f_proxy_account_id": proxyAccountID,
			"f_lifecycle_status":     interfaces.KNProxyLifecycleActive,
			"f_published_generation": generation - 1}).ToSql()
		if buildErr != nil {
			return nil, buildErr
		}
		result, execErr = tx.ExecContext(ctx, query, args...)
		if execErr != nil {
			return nil, execErr
		}
		if buildErr = requireOneRow(result, "mark proxy mapping failed for invalid outbox payload"); buildErr != nil {
			return nil, buildErr
		}
		if buildErr = tx.Commit(); buildErr != nil {
			return nil, buildErr
		}
		return nil, errors.New(lastError)
	}
	processingStatus := interfaces.KNProxyOutboxProcessing
	if claimedStatus == interfaces.KNProxyOutboxCompleting {
		processingStatus = interfaces.KNProxyOutboxCompleting
	}
	claimUpdates := map[string]any{
		"f_status":      processingStatus,
		"f_lease_owner": owner,
		"f_lease_until": leaseUntil,
		"f_updated_at":  now,
	}
	if claimedStatus != interfaces.KNProxyOutboxCompleting {
		claimUpdates["f_attempt_count"] = sq.Expr("f_attempt_count + 1")
	}
	query, args, err := sq.Update(proxyOutboxTable).SetMap(claimUpdates).
		Where(sq.Eq{"f_id": eventID}).ToSql()
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if err := requireOneRow(result, "claim proxy outbox event"); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	event.ID = eventID
	event.KNID = knID
	event.ProxyAccountID = proxyAccountID
	event.Generation = generation
	event.BaseVersion = baseVersion
	event.TargetVersion = targetVersion
	event.Status = processingStatus
	event.SafeApplied = claimedStatus == interfaces.KNProxyOutboxCompleting
	event.AttemptCount = attempts
	if !event.SafeApplied {
		event.AttemptCount++
	}
	event.LeaseOwner = owner
	event.LeaseUntil = leaseUntil
	event.CreatedAt = createdAt
	event.UpdatedAt = now
	return &event, nil
}

func validateOutboxPayload(event *interfaces.KNProxyOutboxEvent) error {
	if event == nil || strings.TrimSpace(event.GrantorID) == "" ||
		strings.TrimSpace(event.TargetVersion) == "" {
		return errors.New("proxy outbox payload has no grantor or target version")
	}
	bindings := make(map[string]struct{}, len(event.Bindings))
	for _, binding := range event.Bindings {
		if strings.TrimSpace(binding.BindingType) == "" || strings.TrimSpace(binding.BindingID) == "" {
			return errors.New("proxy outbox payload contains an invalid binding")
		}
		key := binding.BindingType + "\x00" + binding.BindingID
		if _, duplicate := bindings[key]; duplicate {
			return errors.New("proxy outbox payload contains a duplicate binding")
		}
		bindings[key] = struct{}{}
	}
	validateSources := func(sources []interfaces.ProxyGrantSourceSpec) (map[string]struct{}, error) {
		keys := make(map[string]struct{}, len(sources))
		for _, source := range sources {
			bindingKey := source.BindingType + "\x00" + source.BindingID
			if source.KNID != event.KNID || strings.TrimSpace(source.ResourceType) == "" ||
				strings.TrimSpace(source.ResourceID) == "" || strings.TrimSpace(source.Operation) == "" ||
				strings.TrimSpace(source.SourceType) == "" || strings.TrimSpace(source.SourceID) == "" {
				return nil, errors.New("proxy outbox payload contains an invalid grant source")
			}
			if _, affected := bindings[bindingKey]; !affected {
				return nil, errors.New("proxy outbox grant source is outside the affected bindings")
			}
			key := strings.Join([]string{source.ResourceType, source.ResourceID, source.Operation,
				source.SourceType, source.SourceID, source.KNID, source.BindingType, source.BindingID}, "\x00")
			if _, duplicate := keys[key]; duplicate {
				return nil, errors.New("proxy outbox payload contains a duplicate grant source")
			}
			keys[key] = struct{}{}
		}
		return keys, nil
	}
	desired, err := validateSources(event.DesiredSources)
	if err != nil {
		return err
	}
	upserts, err := validateSources(event.Upserts)
	if err != nil {
		return err
	}
	removals, err := validateSources(event.Removals)
	if err != nil {
		return err
	}
	if len(desired) != len(upserts) {
		return errors.New("proxy outbox desired sources do not match upserts")
	}
	for key := range desired {
		if _, exists := upserts[key]; !exists {
			return errors.New("proxy outbox desired sources do not match upserts")
		}
	}
	for key := range removals {
		if _, exists := upserts[key]; exists {
			return errors.New("proxy outbox source is both upserted and removed")
		}
	}
	return nil
}

func (a *outboxAccess) RenewLease(ctx context.Context, eventID, owner string, now, leaseUntil int64) (bool, error) {
	query, args, err := sq.Update(proxyOutboxTable).SetMap(map[string]any{
		"f_lease_until": leaseUntil,
		"f_updated_at":  now,
	}).Where(sq.Eq{
		"f_id": eventID, "f_status": []string{interfaces.KNProxyOutboxProcessing, interfaces.KNProxyOutboxCompleting},
		"f_lease_owner": owner,
	}).Where(sq.Gt{"f_lease_until": now}).ToSql()
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

func (a *outboxAccess) Complete(ctx context.Context, event *interfaces.KNProxyOutboxEvent,
	owner string, completedAt int64) error {
	if event == nil {
		return errors.New("proxy outbox event is nil")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var publishedGeneration, syncGeneration int64
	var proxyAccountID, lifecycleStatus, publishedVersion, pendingVersion string
	err = tx.QueryRowContext(ctx, `SELECT f_proxy_account_id, f_lifecycle_status, f_published_generation,
       f_sync_generation, f_published_model_version, f_pending_model_version
FROM t_kn_proxy_account WHERE f_kn_id = ? FOR UPDATE`, event.KNID).
		Scan(&proxyAccountID, &lifecycleStatus, &publishedGeneration, &syncGeneration,
			&publishedVersion, &pendingVersion)
	if err != nil {
		return err
	}
	if proxyAccountID != event.ProxyAccountID || lifecycleStatus != interfaces.KNProxyLifecycleActive {
		return errors.New("proxy outbox mapping identity or lifecycle changed")
	}
	if publishedGeneration+1 != event.Generation {
		return fmt.Errorf("proxy outbox generation %d is not queue head after %d", event.Generation, publishedGeneration)
	}
	if publishedVersion != event.BaseVersion {
		return fmt.Errorf("proxy outbox base version %q does not match published version %q",
			event.BaseVersion, publishedVersion)
	}
	if syncGeneration < event.Generation {
		return fmt.Errorf("proxy outbox generation %d exceeds staged generation %d", event.Generation, syncGeneration)
	}
	if err := replaceBindingSnapshot(ctx, tx, publishedGrantSourceTable, event.KNID,
		event.Bindings, event.DesiredSources, completedAt); err != nil {
		return err
	}
	query, args, err := sq.Update(proxyOutboxTable).SetMap(map[string]any{
		"f_status":       interfaces.KNProxyOutboxDone,
		"f_lease_owner":  "",
		"f_lease_until":  int64(0),
		"f_last_error":   "",
		"f_updated_at":   completedAt,
		"f_completed_at": completedAt,
	}).Where(sq.Eq{
		"f_id": event.ID, "f_status": []string{interfaces.KNProxyOutboxProcessing, interfaces.KNProxyOutboxCompleting},
		"f_lease_owner": owner,
	}).ToSql()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if err := requireOneRow(result, "complete proxy outbox event"); err != nil {
		return err
	}
	status := interfaces.KNProxySyncPending
	if syncGeneration == event.Generation {
		status = interfaces.KNProxySyncReady
		pendingVersion = ""
	}
	query, args, err = sq.Update(tableName).SetMap(map[string]any{
		"f_published_generation":    event.Generation,
		"f_published_model_version": event.TargetVersion,
		"f_synced_model_version":    event.TargetVersion,
		"f_sync_status":             status,
		"f_pending_model_version":   pendingVersion,
		"f_last_sync_error":         "",
		"f_last_sync_succeeded_at":  completedAt,
		"f_version":                 sq.Expr("f_version + 1"),
		"f_updated_at":              completedAt,
	}).Where(sq.Eq{
		"f_kn_id": event.KNID, "f_published_generation": event.Generation - 1,
	}).ToSql()
	if err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if err := requireOneRow(result, "advance proxy published generation"); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *outboxAccess) Retry(ctx context.Context, eventID, owner, lastError string,
	nextRetryAt, updatedAt int64, dead, safeApplied bool) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var knID, proxyAccountID string
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT f_kn_id, f_proxy_account_id, f_generation FROM t_kn_proxy_sync_outbox
WHERE f_id = ? AND f_status IN ('processing', 'completing') AND f_lease_owner = ? FOR UPDATE`, eventID, owner).
		Scan(&knID, &proxyAccountID, &generation)
	if err != nil {
		return err
	}
	status := interfaces.KNProxyOutboxRetrying
	if dead {
		status = interfaces.KNProxyOutboxDead
	} else if safeApplied {
		status = interfaces.KNProxyOutboxCompleting
	}
	updates := map[string]any{
		"f_status":        status,
		"f_next_retry_at": nextRetryAt,
		"f_lease_owner":   "",
		"f_lease_until":   int64(0),
		"f_last_error":    lastError,
		"f_updated_at":    updatedAt,
	}
	query, args, err := sq.Update(proxyOutboxTable).SetMap(updates).
		Where(sq.Eq{"f_id": eventID, "f_lease_owner": owner}).ToSql()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if err := requireOneRow(result, "retry proxy outbox event"); err != nil {
		return err
	}
	if dead {
		query, args, err = sq.Update(tableName).SetMap(map[string]any{
			"f_sync_status":     interfaces.KNProxySyncFailed,
			"f_last_sync_error": lastError,
			"f_version":         sq.Expr("f_version + 1"),
			"f_updated_at":      updatedAt,
		}).Where(sq.Eq{"f_kn_id": knID, "f_proxy_account_id": proxyAccountID,
			"f_lifecycle_status":     interfaces.KNProxyLifecycleActive,
			"f_published_generation": generation - 1}).ToSql()
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		if err := requireOneRow(result, "mark proxy mapping failed for dead outbox event"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *outboxAccess) CleanupTerminal(ctx context.Context, updatedBefore int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	var acquired int
	const acquireCleanupLock = "SELECT GET_LOCK(CONCAT(DATABASE(), ':', ?), 0)"
	const releaseCleanupLock = "SELECT RELEASE_LOCK(CONCAT(DATABASE(), ':', ?))"
	if err := conn.QueryRowContext(ctx, acquireCleanupLock, cleanupLockName).Scan(&acquired); err != nil {
		return 0, err
	}
	if acquired != 1 {
		return 0, nil
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(releaseCtx, releaseCleanupLock, cleanupLockName)
	}()
	result, err := conn.ExecContext(ctx, `DELETE FROM t_kn_proxy_sync_outbox
WHERE f_id IN (SELECT f_id FROM (SELECT e.f_id FROM t_kn_proxy_sync_outbox e
JOIN t_kn_proxy_account m ON m.f_kn_id = e.f_kn_id
WHERE e.f_updated_at < ? AND e.f_lease_owner = '' AND e.f_lease_until = 0
  AND ((e.f_status = 'done' AND e.f_completed_at > 0 AND e.f_generation <= m.f_published_generation)
       OR (e.f_status = 'dead' AND m.f_sync_status = 'failed'))
ORDER BY e.f_updated_at, e.f_id LIMIT ?) AS expired)`, updatedBefore, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func replaceBindingSnapshot(ctx context.Context, tx *sql.Tx, table, knID string,
	bindings []interfaces.KNProxyBindingRef, sources []interfaces.ProxyGrantSourceSpec, updatedAt int64) error {
	for start := 0; start < len(bindings); start += maxSnapshotInsertRows {
		end := min(start+maxSnapshotInsertRows, len(bindings))
		query, args, err := sq.Delete(table).Where(sq.Eq{"f_kn_id": knID}).
			Where(publishedBindingConditions(bindings[start:end])).ToSql()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return insertSnapshotSources(ctx, tx, table, knID, sources, updatedAt)
}

func insertSnapshotSources(ctx context.Context, tx *sql.Tx, table, knID string,
	sources []interfaces.ProxyGrantSourceSpec, updatedAt int64) error {
	for start := 0; start < len(sources); start += maxSnapshotInsertRows {
		end := min(start+maxSnapshotInsertRows, len(sources))
		insert := sq.Insert(table).Columns(
			"f_kn_id", "f_binding_type", "f_binding_id", "f_resource_type", "f_resource_id", "f_operation",
			"f_source_type", "f_source_id", "f_created_at", "f_updated_at",
		)
		for _, source := range sources[start:end] {
			insert = insert.Values(knID, source.BindingType, source.BindingID, source.ResourceType,
				source.ResourceID, source.Operation, source.SourceType, source.SourceID, updatedAt, updatedAt)
		}
		query, args, err := insert.ToSql()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}
