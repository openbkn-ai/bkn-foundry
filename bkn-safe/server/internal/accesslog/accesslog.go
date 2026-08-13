// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package accesslog persists the minimal user access facts emitted at the
// authentication boundary. It intentionally does not depend on HTTP or Hydra.
package accesslog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

type Store struct{ db *gorm.DB }

func New(db *gorm.DB) *Store { return &Store{db: db} }

type Entry struct {
	ActorID           string
	ActorNameSnapshot string
	AuthMethod        string
	SourceChannel     string
	Action            string
	Outcome           string
	FailureCode       string
	RequestID         string
	ClientIP          string
}

// Record persists one independently identifiable access fact. Callers must
// treat any returned error as non-fatal to the authentication flow.
func (s *Store) Record(ctx context.Context, entry Entry) error {
	return s.db.WithContext(ctx).Create(&model.AccessLog{
		ID:                NewID(),
		ActorID:           entry.ActorID,
		ActorNameSnapshot: entry.ActorNameSnapshot,
		AuthMethod:        entry.AuthMethod,
		SourceChannel:     entry.SourceChannel,
		Action:            entry.Action,
		Outcome:           entry.Outcome,
		FailureCode:       entry.FailureCode,
		RequestID:         entry.RequestID,
		ClientIP:          entry.ClientIP,
	}).Error
}

type Filter struct {
	ActorID string
	Action  string
	Outcome string
	From    time.Time
	To      time.Time
	Offset  int
	Limit   int
}

func (s *Store) List(ctx context.Context, filter Filter) ([]model.AccessLog, int64, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	q := s.db.WithContext(ctx).Model(&model.AccessLog{})
	if filter.ActorID != "" {
		q = q.Where("actor_id = ?", filter.ActorID)
	}
	if filter.Action != "" {
		q = q.Where("action = ?", filter.Action)
	}
	if filter.Outcome != "" {
		q = q.Where("outcome = ?", filter.Outcome)
	}
	if !filter.From.IsZero() {
		q = q.Where("created_at >= ?", filter.From)
	}
	if !filter.To.IsZero() {
		q = q.Where("created_at < ?", filter.To)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// Do not preallocate from a request-derived page size. GORM grows the result
	// slice only for rows returned by the bounded SQL query.
	var logs []model.AccessLog
	if err := q.Order("created_at DESC").Order("id ASC").Offset(max(filter.Offset, 0)).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

func (s *Store) Get(ctx context.Context, id string) (model.AccessLog, bool, error) {
	var row model.AccessLog
	err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err == gorm.ErrRecordNotFound {
		return model.AccessLog{}, false, nil
	}
	if err != nil {
		return model.AccessLog{}, false, err
	}
	return row, true, nil
}

func NewID() string {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
