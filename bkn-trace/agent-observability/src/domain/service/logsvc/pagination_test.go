// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package logsvc

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type numberedPageSource struct {
	records           []observabilityvo.LogRecord
	calls             int
	deadline          bool
	deadlineRemaining time.Duration
	cancel            context.CancelFunc
}

func (source *numberedPageSource) ID() string { return "numbered-pages" }

func (source *numberedPageSource) Search(ctx context.Context, query observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	source.calls++
	deadline, hasDeadline := ctx.Deadline()
	source.deadline = hasDeadline
	source.deadlineRemaining = time.Until(deadline)
	if source.cancel != nil {
		source.cancel()
	}
	page := observabilityvo.SourcePage{Count: int64(len(source.records)), CountAccuracy: "exact"}
	for _, record := range source.records {
		if afterSourcePosition(record, query.PageBefore) {
			page.Records = append(page.Records, record)
			if len(page.Records) == query.Limit {
				break
			}
		}
	}
	return page, nil
}

func (source *numberedPageSource) SearchNumbered(ctx context.Context, query observabilityvo.LogQuery, _ evidencevo.AccessProfile) (observabilityvo.SourcePage, error) {
	if query.PageBefore != nil {
		return source.Search(ctx, query)
	}
	source.calls++
	deadline, sourceHasDeadline := ctx.Deadline()
	source.deadline = sourceHasDeadline
	source.deadlineRemaining = time.Until(deadline)
	if source.cancel != nil {
		source.cancel()
		return observabilityvo.SourcePage{}, ctx.Err()
	}
	offset := (query.Page - 1) * query.Limit
	page := observabilityvo.SourcePage{Count: int64(len(source.records)), CountAccuracy: "exact"}
	if offset >= len(source.records) {
		return page, nil
	}
	end := min(offset+query.Limit, len(source.records))
	page.Records = source.records[offset:end]
	if end < len(source.records) {
		page.NextCursor = "more"
	}
	position := positionForRecord(page.Records[len(page.Records)-1])
	page.LastPosition = &position
	return page, nil
}

func newNumberedPageSource(count int) *numberedPageSource {
	base := time.Now().UTC().Add(-time.Minute)
	records := make([]observabilityvo.LogRecord, count)
	for index := range records {
		records[index] = observabilityvo.LogRecord{
			LogID: "log-" + strconv.Itoa(index), SourceID: "numbered-pages",
			Category: observabilityvo.CategoryRuntimeBusiness, EventName: "sandbox.session.changed",
			EventTimestamp: base.Add(-time.Duration(index) * time.Second),
		}
	}
	return &numberedPageSource{records: validTestRecords(records)}
}

func TestNumberedLogPagesQueryTargetDirectly(t *testing.T) {
	for _, page := range []int{101, 181, 500} {
		t.Run(strconv.Itoa(page), func(t *testing.T) {
			source := newNumberedPageSource(10020)
			service := NewWithOptions([]Source{source}, Options{CursorKey: []byte("test-cursor-key"), SourceTimeout: time.Hour})
			profile := activeProfile("admin-a", "admin")
			result, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Page: page, Limit: 20})
			offset := (page - 1) * 20
			if err != nil || result.Page != page || result.PageSize != 20 || len(result.Records) != 20 || result.Records[0].LogID != "log-"+strconv.Itoa(offset) {
				t.Fatalf("wrong requested page: result=%+v err=%v", result, err)
			}
			wantCalls := 1
			if source.calls != wantCalls {
				t.Fatalf("source calls=%d, want %d rather than %d", source.calls, wantCalls, page)
			}
			if !source.deadline {
				t.Fatal("numbered page queries must have a deadline")
			}
			if source.deadlineRemaining > time.Hour {
				t.Fatalf("numbered jump deadline is too long: %s", source.deadlineRemaining)
			}
			next, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Page: page, Limit: 20, Cursor: result.NextCursor})
			if err != nil || len(next.Records) != 20 || next.Records[0].LogID != "log-"+strconv.Itoa(offset+20) {
				t.Fatalf("cursor lost or duplicated records: result=%+v err=%v", next, err)
			}
		})
	}
}

type filteredNumberedPageSource struct{ calls int }

func (source *filteredNumberedPageSource) ID() string { return "filtered-numbered-pages" }

func (source *filteredNumberedPageSource) Search(_ context.Context, _ observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	source.calls++
	position := observabilityvo.SourcePosition{EventTimestamp: time.Now().UTC().Add(-time.Duration(source.calls) * time.Second), LogID: strconv.Itoa(source.calls)}
	return observabilityvo.SourcePage{Count: 201, CountAccuracy: "partial", LastPosition: &position}, nil
}

func TestNumberedLogPageRejectsSourcesWithoutDirectPagination(t *testing.T) {
	source := &filteredNumberedPageSource{}
	_, err := New([]Source{source}).List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{Page: 1000000, Limit: 20})
	if !errors.Is(err, ErrNumberedPaginationUnsupported) || source.calls != 0 {
		t.Fatalf("unbounded traversal: err=%v calls=%d", err, source.calls)
	}
}

func TestNumberedLogPageStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := newNumberedPageSource(1000)
	source.cancel = cancel
	_, err := New([]Source{source}).List(ctx, activeProfile("admin-a", "admin"), observabilityvo.LogQuery{Page: 101, Limit: 20})
	if !errors.Is(err, context.Canceled) || source.calls != 1 {
		t.Fatalf("canceled traversal continued: err=%v calls=%d", err, source.calls)
	}
}

func TestNumberedLogPageBeyondAvailableRecordsIsEmpty(t *testing.T) {
	source := newNumberedPageSource(25)
	result, err := New([]Source{source}).List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{Page: 101, Limit: 20})
	if err != nil || len(result.Records) != 0 || result.NextCursor != "" || result.Page != 101 || result.PageSize != 20 {
		t.Fatalf("out-of-range page must not repeat the last page: result=%+v err=%v", result, err)
	}
}

func TestNumberedLogPageStillChecksEachReturnedRecord(t *testing.T) {
	source := newNumberedPageSource(100)
	result, err := New([]Source{source}).List(context.Background(), activeProfile("builder-a", "network_builder"), observabilityvo.LogQuery{Page: 2, Limit: 20})
	if err != nil || len(result.Records) != 0 || result.Count != 0 || result.CountExact || !result.Partial {
		t.Fatalf("source contract violation disclosed records or raw total: result=%+v err=%v", result, err)
	}
}
