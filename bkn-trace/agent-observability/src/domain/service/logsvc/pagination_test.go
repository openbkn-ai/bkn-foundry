// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package logsvc

import (
	"context"
	"errors"
	"fmt"
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

// A legacy source intentionally has no SearchNumbered capability.
type cursorOnlySource struct{ source *numberedPageSource }

func (s cursorOnlySource) ID() string { return "bkn-trace-core" }
func (s cursorOnlySource) Search(ctx context.Context, q observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	return s.source.Search(ctx, q)
}

func TestLegacyAndMixedSourcesRetainBoundedNumberedPages(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		source := newNumberedPageSource(2020)
		for index := range source.records {
			source.records[index].EventName = "conversation.created"
			source.records[index] = validTestRecord(source.records[index])
		}
		sources := []Source{cursorOnlySource{source: source}}
		if mixed {
			sources = append(sources, newNumberedPageSource(0))
		}
		service := NewWithOptions(sources, Options{OperationAuditOnly: true})
		for _, page := range []int{2, 100} {
			result, err := service.List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{Page: page, Limit: 20})
			if err != nil || len(result.Records) != 20 || result.Records[0].LogID != "log-"+strconv.Itoa((page-1)*20) || result.Page != page {
				t.Fatalf("mixed=%v page=%d result=%+v err=%v", mixed, page, result, err)
			}
		}
		calls := source.calls
		_, err := service.List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{Page: 101, Limit: 20})
		if !errors.Is(err, ErrNumberedPaginationUnsupported) || source.calls != calls {
			t.Fatalf("legacy jump escaped bound: err=%v calls=%d", err, source.calls)
		}
	}
}

func TestLegacyNumberedReplayHasOneTotalDeadline(t *testing.T) {
	source := newNumberedPageSource(2020)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source.cancel = cancel
	_, err := New([]Source{cursorOnlySource{source: source}}).List(ctx, activeProfile("admin-a", "admin"), observabilityvo.LogQuery{Page: 100, Limit: 20})
	if !errors.Is(err, context.Canceled) || source.calls != 1 {
		t.Fatalf("replay continued after cancellation: calls=%d err=%v", source.calls, err)
	}
}

type slowLegacySource struct{ calls int }

func (s *slowLegacySource) ID() string { return "bkn-trace-core" }
func (s *slowLegacySource) Search(ctx context.Context, q observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	s.calls++
	select {
	case <-ctx.Done():
		return observabilityvo.SourcePage{}, ctx.Err()
	case <-time.After(40 * time.Millisecond):
	}
	position := observabilityvo.SourcePosition{EventTimestamp: time.Now().UTC().Add(-time.Duration(s.calls) * time.Second), LogID: strconv.Itoa(s.calls)}
	return observabilityvo.SourcePage{Count: 201, CountAccuracy: "partial", LastPosition: &position}, nil
}
func TestLegacyReplayDoesNotRenewDeadlineForEachPage(t *testing.T) {
	source := &slowLegacySource{}
	_, err := NewWithOptions([]Source{source}, Options{SourceTimeout: 60 * time.Millisecond}).List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{Page: 100, Limit: 20})
	if !errors.Is(err, ErrSourcesUnavailable) || source.calls > 2 {
		t.Fatalf("replay renewed deadline: calls=%d err=%v", source.calls, err)
	}
}

// The ledger has a native descending event-ID tie-break, across business sources.
type ledgerOrderSource struct{ records []observabilityvo.LogRecord }

func (s ledgerOrderSource) ID() string { return "audit-ledger" }
func (s ledgerOrderSource) Search(ctx context.Context, q observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	return s.SearchNumbered(ctx, q, evidencevo.AccessProfile{})
}
func (s ledgerOrderSource) SearchNumbered(_ context.Context, q observabilityvo.LogQuery, _ evidencevo.AccessProfile) (observabilityvo.SourcePage, error) {
	rows := s.records
	if q.PageBefore != nil {
		rows = nil
		for _, r := range s.records {
			if r.EventTimestamp.Before(q.PageBefore.EventTimestamp) || (r.EventTimestamp.Equal(q.PageBefore.EventTimestamp) && r.EventID < q.PageBefore.LogID) {
				rows = append(rows, r)
			}
		}
	} else {
		offset := (q.Page - 1) * q.Limit
		if offset >= len(rows) {
			rows = nil
		} else {
			rows = rows[offset:]
		}
	}
	page := observabilityvo.SourcePage{Count: int64(len(rows)), CountAccuracy: "partial"}
	if len(rows) > q.Limit {
		page.NextCursor = "more"
		rows = rows[:q.Limit]
	}
	page.Records = rows
	if len(rows) > 0 {
		page.LastPosition = &observabilityvo.SourcePosition{EventTimestamp: rows[len(rows)-1].EventTimestamp, LogID: rows[len(rows)-1].EventID}
	}
	return page, nil
}
func TestLedgerFirstPageAndCursorKeepNativeOrderForTimestampTies(t *testing.T) {
	timestamp := time.Now().UTC().Add(-time.Minute)
	source := ledgerOrderSource{}
	for index := 21; index > 0; index-- {
		id := fmt.Sprintf("evt-%02d", index)
		businessSource := "a"
		if index == 21 {
			businessSource = "b"
		}
		source.records = append(source.records, validTestRecord(observabilityvo.LogRecord{EventID: id, LogID: id, SourceLogID: id, SourceID: businessSource, EventTimestamp: timestamp, Category: observabilityvo.CategoryAuditAdmin}))
	}
	service := NewWithOptions([]Source{source}, Options{OperationAuditOnly: true})
	profile := activeProfile("admin-a", "admin")
	first, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Page: 1, Limit: 20})
	if err != nil || len(first.Records) != 20 || first.Records[0].EventID != "evt-21" || first.Records[19].EventID != "evt-02" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	next, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Page: 181, Limit: 20, Cursor: first.NextCursor})
	if err != nil || len(next.Records) != 1 || next.Records[0].EventID != "evt-01" {
		t.Fatalf("cursor omitted or duplicated tied record: next=%+v err=%v", next, err)
	}
	numbered, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Page: 2, Limit: 20})
	if err != nil || len(numbered.Records) != 1 || numbered.Records[0].EventID != next.Records[0].EventID {
		t.Fatalf("numbered and cursor orders differ: result=%+v err=%v", numbered, err)
	}
}
