// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package index

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// IndexSessionSnapshot 读取同一索引快照中的命中结果，并返回下一页需要的排序值。
type IndexSessionSnapshot interface {
	SearchPage(ctx context.Context, limit int, after []any, trackTotal bool) (*interfaces.QueryPage, []any, error)
	Close() error
}

type indexReadSession struct {
	snapshot      IndexSessionSnapshot
	trackTotal    bool
	checkPageSize bool
	after         []any
	offset        int64
	total         *int64
	done          bool
}

// NewIndexReadSession 接管已打开的索引快照，并维护连续分页的状态。
// 当命中总数等于可分页的文档总数时，checkPageSize 可启用页长校验。
func NewIndexReadSession(snapshot IndexSessionSnapshot, offset int64, trackTotal, checkPageSize bool) interfaces.QueryPageSession {
	return &indexReadSession{snapshot: snapshot, offset: offset, trackTotal: trackTotal, checkPageSize: checkPageSize}
}

func (s *indexReadSession) ReadPage(ctx context.Context, limit int) (*interfaces.QueryPage, error) {
	if s.snapshot == nil {
		return nil, interfaces.ErrQueryPageSessionExpired
	}
	if s.done {
		return &interfaces.QueryPage{Entries: []map[string]any{}, TotalCount: s.total}, nil
	}
	if limit <= 0 || limit > interfaces.MaxPageLimit {
		return nil, interfaces.NewConditionBuildError("invalid index cursor page limit")
	}

	needTotal := s.trackTotal && s.total == nil
	page, next, err := s.snapshot.SearchPage(ctx, limit, s.after, needTotal)
	if err != nil {
		return nil, err
	}
	total := s.total
	if needTotal {
		total = page.TotalCount
	}
	pageSize := int64(len(page.Entries))
	if s.checkPageSize && total != nil {
		expected := min(int64(limit), max(*total-s.offset, 0))
		if pageSize != expected {
			return nil, errors.New("index cursor page size does not match its exact total")
		}
	}
	offset := s.offset + pageSize
	hasMore := len(page.Entries) == limit && (total == nil || offset < *total)
	if hasMore && len(next) == 0 {
		return nil, errors.New("index cursor page is missing its sort values")
	}

	s.offset = offset
	s.total = total
	s.after = next
	s.done = !hasMore
	page.TotalCount = total
	page.HasMore = hasMore
	return page, nil
}

func (s *indexReadSession) Close() error {
	snapshot := s.snapshot
	if snapshot == nil {
		return nil
	}
	s.snapshot = nil
	return snapshot.Close()
}
