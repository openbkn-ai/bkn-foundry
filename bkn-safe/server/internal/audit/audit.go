// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package audit records and queries the bkn-safe admin audit trail: one row per
// privileged mutation (who did what, to which target, with what outcome). It is
// dependency-light on purpose (model + gorm only) so the HTTP layer can write to
// it from middleware without coupling auditing to auth/directory.
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"gorm.io/gorm"

	"bkn-safe/internal/model"
)

// Store reads and writes the audit trail over GORM.
type Store struct {
	db *gorm.DB
}

// New builds an audit store.
func New(db *gorm.DB) *Store { return &Store{db: db} }

// Entry is a single audit record to persist. ID and the timestamp are assigned
// by Record (the caller supplies only the request facts).
type Entry struct {
	ActorID    string
	Method     string
	Resource   string
	Action     string
	TargetID   string
	TargetName string
	Detail     string
	Status     int
	ClientIP   string
}

// Record persists one audit entry. The returned error is for logging only —
// auditing must never break the request it is recording, so callers swallow it.
func (s *Store) Record(ctx context.Context, e Entry) error {
	row := model.AuditLog{
		ID:         newID(),
		ActorID:    e.ActorID,
		Method:     e.Method,
		Resource:   e.Resource,
		Action:     e.Action,
		TargetID:   e.TargetID,
		TargetName: e.TargetName,
		Detail:     e.Detail,
		Status:     e.Status,
		ClientIP:   e.ClientIP,
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

// Filter narrows a List query. Zero-value fields are not applied. From/To bound
// CreatedAt (inclusive lower, exclusive upper).
type Filter struct {
	ActorID  string
	Resource string
	Action   string
	TargetID string
	From     time.Time
	To       time.Time
	Offset   int
	Limit    int
}

// List returns a page of audit entries (newest first) matching the filter, plus
// the total matching count. Limit<=0 defaults to 50 and is capped at 500.
func (s *Store) List(ctx context.Context, f Filter) ([]model.AuditLog, int64, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	q := s.db.WithContext(ctx).Model(&model.AuditLog{})
	if f.ActorID != "" {
		q = q.Where("actor_id = ?", f.ActorID)
	}
	if f.Resource != "" {
		q = q.Where("resource = ?", f.Resource)
	}
	if f.Action != "" {
		q = q.Where("action = ?", f.Action)
	}
	if f.TargetID != "" {
		q = q.Where("target_id = ?", f.TargetID)
	}
	if !f.From.IsZero() {
		q = q.Where("created_at >= ?", f.From)
	}
	if !f.To.IsZero() {
		q = q.Where("created_at < ?", f.To)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	logs := make([]model.AuditLog, 0, limit)
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// newID returns a random 128-bit hex id (same scheme as auth.NewID, duplicated
// here to keep this package free of the auth dependency).
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
