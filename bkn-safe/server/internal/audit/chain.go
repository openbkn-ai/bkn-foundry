// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// chainMu serialises chain appends within one process so two concurrent audit
// writes cannot read the same head. Across replicas the unique index on seq is
// the arbiter: a lost race surfaces as a duplicate-key error and the append is
// retried on the new head (see Record).
var chainMu sync.Mutex

// chainAppendAttempts bounds the duplicate-key retry loop. Audit writes are
// low-frequency, so losing the race this many times in a row means something
// other than contention is wrong.
const chainAppendAttempts = 5

// canonicalRow is the byte-stable form the row hash is computed over. It is a
// struct, not a map, so encoding/json emits the fields in this fixed order;
// CreatedAt is rendered in UTC so the hash does not depend on the DSN's loc.
// Every field that a reader relies on is included — a change to any of them
// changes the hash.
type canonicalRow struct {
	Seq               uint64 `json:"seq"`
	ID                string `json:"id"`
	ActorID           string `json:"actor_id"`
	ActorNameSnapshot string `json:"actor_name_snapshot"`
	ActorType         string `json:"actor_type"`
	AuthMethod        string `json:"auth_method"`
	CredentialID      string `json:"credential_id"`
	RequestID         string `json:"request_id"`
	SourceChannel     string `json:"source_channel"`
	Method            string `json:"method"`
	Resource          string `json:"resource"`
	Action            string `json:"action"`
	TargetID          string `json:"target_id"`
	TargetName        string `json:"target_name"`
	Detail            string `json:"detail"`
	Status            int    `json:"status"`
	ClientIP          string `json:"client_ip"`
	CreatedAt         string `json:"created_at"`
	PrevHash          string `json:"prev_hash"`
}

// rowHash computes the chain hash of row at position seq, linked to prev.
func rowHash(row model.AuditLog, seq uint64, prev string) string {
	c := canonicalRow{
		Seq: seq, ID: row.ID, ActorID: row.ActorID, ActorNameSnapshot: row.ActorNameSnapshot,
		ActorType: row.ActorType, AuthMethod: row.AuthMethod, CredentialID: row.CredentialID,
		RequestID: row.RequestID, SourceChannel: row.SourceChannel, Method: row.Method,
		Resource: row.Resource, Action: row.Action, TargetID: row.TargetID, TargetName: row.TargetName,
		Detail: row.Detail, Status: row.Status, ClientIP: row.ClientIP,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), PrevHash: prev,
	}
	b, err := json.Marshal(c)
	if err != nil {
		// A struct of strings and ints cannot fail to marshal; keep the
		// signature honest anyway.
		panic(fmt.Sprintf("audit: canonical row marshal: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// chainTimestamp is the CreatedAt an appended row carries. Millisecond
// precision matches what the MySQL dialect stores (datetime(3)), so the value
// read back for verification is byte-identical to the one that was hashed.
func chainTimestamp() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}

// LogHead writes the chain head to the service log at start and then every
// interval until ctx ends, so the platform's log pipeline — an append-only
// store outside this database — holds anchors a later Verify can be compared
// against. interval<=0 disables it.
func (s *Store) LogHead(ctx context.Context, interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}
	emit := func() {
		head, found, err := s.Head(ctx)
		switch {
		case err != nil:
			slog.Warn("audit chain head unavailable", "error", err)
		case !found:
			slog.Info("audit chain head", "seq", 0, "row_hash", "")
		default:
			slog.Info("audit chain head", "seq", head.Seq, "row_hash", head.RowHash,
				"created_at", head.CreatedAt.UTC().Format(time.RFC3339Nano))
		}
	}
	emit()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emit()
		}
	}
}

// Head is the current end of the chain.
type Head struct {
	Seq       uint64    `json:"seq"`
	RowHash   string    `json:"row_hash"`
	CreatedAt time.Time `json:"created_at"`
}

// Head returns the last chained row's position and hash. found is false on an
// empty chain (no row has been appended since the chain was introduced).
func (s *Store) Head(ctx context.Context) (Head, bool, error) {
	// Limit+Find rather than First: an empty chain is the normal state of a
	// fresh install, not a "record not found" worth a log line.
	var rows []model.AuditLog
	if err := s.db.WithContext(ctx).Where("seq IS NOT NULL").Order("seq DESC").Limit(1).Find(&rows).Error; err != nil {
		return Head{}, false, err
	}
	if len(rows) == 0 || rows[0].Seq == nil {
		return Head{}, false, nil
	}
	row := rows[0]
	return Head{Seq: *row.Seq, RowHash: row.RowHash, CreatedAt: row.CreatedAt}, true, nil
}

// append links row to the current head and inserts it. The caller holds
// chainMu. A duplicate-key error means another replica appended first; the
// caller re-reads the head and tries again.
func (s *Store) append(ctx context.Context, row *model.AuditLog) error {
	head, _, err := s.Head(ctx)
	if err != nil {
		return err
	}
	seq := head.Seq + 1
	row.Seq = &seq
	row.PrevHash = head.RowHash
	row.RowHash = rowHash(*row, seq, head.RowHash)
	return s.db.WithContext(ctx).Create(row).Error
}

// isDuplicateKey recognises the unique-index violation of every backend this
// service runs on (MySQL wire via openbkn-rds, sqlite in tests) without
// enabling GORM's error translation globally.
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") || // MySQL/MariaDB 1062
		strings.Contains(msg, "Error 1062") ||
		strings.Contains(msg, "UNIQUE constraint failed") // sqlite
}

// VerifyResult reports a chain walk. OK is true when every checked row
// re-hashes to its stored RowHash, links to its predecessor, and no position
// is missing. When a break is found BrokenSeq names the first bad position and
// Reason says what failed: hash_mismatch (row content or hash edited),
// prev_hash_mismatch (predecessor edited or replaced), or gap (row deleted).
// Rows appended before the chain existed carry no seq; they are reported as
// UnchainedRows and never verified.
type VerifyResult struct {
	OK            bool    `json:"ok"`
	FromSeq       uint64  `json:"from_seq"`
	ToSeq         uint64  `json:"to_seq"`
	Checked       int64   `json:"checked"`
	BrokenSeq     *uint64 `json:"broken_seq,omitempty"`
	Reason        string  `json:"reason,omitempty"`
	Head          *Head   `json:"head,omitempty"`
	UnchainedRows int64   `json:"unchained_rows"`
	// Truncated is true when limit stopped the walk before toSeq; resume from
	// ToSeq+1 to continue.
	Truncated bool `json:"truncated"`
}

const (
	verifyBatch      = 500
	verifyDefaultMax = 10000
	verifyHardMax    = 100000
)

// Verify walks the chain from fromSeq (0 or 1 = the beginning) to toSeq (0 =
// the head), at most limit rows (0 = 10000, capped at 100000), and reports the
// first break. It reads in batches so a long chain does not load into memory
// at once.
func (s *Store) Verify(ctx context.Context, fromSeq, toSeq uint64, limit int) (VerifyResult, error) {
	res := VerifyResult{OK: true}
	if limit <= 0 {
		limit = verifyDefaultMax
	}
	if limit > verifyHardMax {
		limit = verifyHardMax
	}
	if err := s.db.WithContext(ctx).Model(&model.AuditLog{}).Where("seq IS NULL").Count(&res.UnchainedRows).Error; err != nil {
		return res, err
	}
	head, found, err := s.Head(ctx)
	if err != nil {
		return res, err
	}
	if found {
		h := head
		res.Head = &h
	}
	if !found {
		return res, nil
	}
	if fromSeq == 0 {
		fromSeq = 1
	}
	if toSeq == 0 || toSeq > head.Seq {
		toSeq = head.Seq
	}
	res.FromSeq = fromSeq
	res.ToSeq = fromSeq - 1
	if fromSeq > toSeq {
		return res, nil
	}
	// Expected link into the first checked row.
	expectedPrev := ""
	if fromSeq > 1 {
		var prev []model.AuditLog
		if err := s.db.WithContext(ctx).Where("seq = ?", fromSeq-1).Limit(1).Find(&prev).Error; err != nil {
			return res, err
		}
		if len(prev) == 0 {
			// The row just before the window is gone: report it as the gap.
			broken := fromSeq - 1
			res.OK, res.BrokenSeq, res.Reason = false, &broken, "gap"
			return res, nil
		}
		expectedPrev = prev[0].RowHash
	}
	expectedSeq := fromSeq
	for res.Checked < int64(limit) && expectedSeq <= toSeq {
		batch := verifyBatch
		if remaining := int64(limit) - res.Checked; remaining < int64(batch) {
			batch = int(remaining)
		}
		var rows []model.AuditLog
		if err := s.db.WithContext(ctx).Where("seq >= ? AND seq <= ?", expectedSeq, toSeq).
			Order("seq ASC").Limit(batch).Find(&rows).Error; err != nil {
			return res, err
		}
		if len(rows) == 0 {
			// Nothing at or after expectedSeq although toSeq says there should be.
			broken := expectedSeq
			res.OK, res.BrokenSeq, res.Reason = false, &broken, "gap"
			return res, nil
		}
		for _, row := range rows {
			if row.Seq == nil || *row.Seq != expectedSeq {
				broken := expectedSeq
				res.OK, res.BrokenSeq, res.Reason = false, &broken, "gap"
				return res, nil
			}
			if row.PrevHash != expectedPrev {
				broken := *row.Seq
				res.OK, res.BrokenSeq, res.Reason = false, &broken, "prev_hash_mismatch"
				return res, nil
			}
			if rowHash(row, *row.Seq, row.PrevHash) != row.RowHash {
				broken := *row.Seq
				res.OK, res.BrokenSeq, res.Reason = false, &broken, "hash_mismatch"
				return res, nil
			}
			res.Checked++
			res.ToSeq = *row.Seq
			expectedPrev = row.RowHash
			expectedSeq = *row.Seq + 1
		}
	}
	res.Truncated = expectedSeq <= toSeq
	return res, nil
}
