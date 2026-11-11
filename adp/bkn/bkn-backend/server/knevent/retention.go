// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	// CheckedRetention is how long the feed keeps a rule check's refresh of
	// an event that it found unchanged. Every check adds one for each open
	// rule event, so without a limit the feed grows for as long as an event
	// stays open. Consumers read the feed within seconds; a week covers one
	// that was down. The other changes are the events' history and stay.
	CheckedRetention = 7 * 24 * time.Hour

	purgePeriod = time.Hour
	// purgeRun bounds one purge; what is left goes on the next.
	purgeRun   = time.Minute
	purgeBatch = 1000
	// purgeLock lets one replica purge at a time.
	purgeLock = "bkn_kn_event_change_purge"
)

// purgeEvery removes expired refreshes at start and then every period until
// ctx ends, so a service restarted more often than the period still purges.
func purgeEvery(ctx context.Context, s *store, period time.Duration) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		purgeOnce(ctx, s, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func purgeOnce(ctx context.Context, s *store, now time.Time) {
	ctx, cancel := context.WithTimeout(ctx, purgeRun)
	defer cancel()
	started := time.Now()
	deleted, err := s.purgeChecked(ctx, now.Add(-CheckedRetention).UnixMilli())
	if err != nil {
		logger.Errorf("knowledge-network event: purge expired check refreshes after deleting %d: %v", deleted, err)
		return
	}
	if deleted > 0 {
		logger.Infof("knowledge-network event: purged %d expired check refreshes in %s", deleted, time.Since(started))
	}
}

// purgeChecked deletes the "checked" changes recorded before a time, a batch
// per transaction, until none is left or ctx ends. The feed then skips their
// numbers; a reader following it reads on from the next one. A replica that
// finds another purging leaves it to that one.
func (s *store) purgeChecked(ctx context.Context, before int64) (deleted int64, err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(), ':', ?), 0)", purgeLock).Scan(&acquired); err != nil {
		return 0, err
	}
	if acquired.Int64 != 1 {
		return 0, nil
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(releaseCtx, "SELECT RELEASE_LOCK(CONCAT(DATABASE(), ':', ?))", purgeLock)
	}()
	for {
		count, err := purgeBatchOf(ctx, conn, before)
		deleted += count
		switch {
		case ctx.Err() != nil:
			// Out of time: the next run goes on from here.
			return deleted, nil
		case err != nil:
			return deleted, err
		case count < purgeBatch:
			return deleted, nil
		}
	}
}

// purgeBatchOf deletes one batch. Read committed takes no gap locks, so event
// writes appending to the feed meanwhile do not wait for it.
func purgeBatchOf(ctx context.Context, conn *sql.Conn, before int64) (int64, error) {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM "+changeTable+" WHERE f_action = ? AND f_time < ? ORDER BY f_time LIMIT ?",
		interfaces.KnEventChecked, before, purgeBatch)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
