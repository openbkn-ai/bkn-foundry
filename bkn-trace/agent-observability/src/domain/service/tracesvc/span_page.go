// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package tracesvc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/oteltracevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ihttpaccess/tracequeryport"
)

const SpanBrowsePageSize = 100

var (
	ErrSpanPageCursor         = errors.New("invalid span page cursor")
	ErrSpanPageIncomplete     = errors.New("incomplete span page")
	ErrSpanPageBudget         = errors.New("span page exceeds budget")
	ErrSpanStorageUnsupported = errors.New("span storage format is unsupported")
	ErrSpanPageUnavailable    = errors.New("bounded span browsing is unavailable")
)

type SpanPage struct {
	TraceID     string                       `json:"trace_id"`
	Entries     []oteltracevo.TraceGraphNode `json:"entries"`
	HasMore     bool                         `json:"has_more"`
	AfterKey    string                       `json:"-"`
	NextCursor  string                       `json:"next_cursor,omitempty"`
	Sort        string                       `json:"sort"`
	Consistency string                       `json:"consistency"`
}

func (s *TraceQueryService) SupportsSpanBrowsing() bool {
	if s == nil {
		return false
	}
	_, ok := s.traceQueryPort.(tracequeryport.BoundedTraceQueryPort)
	return ok
}
func validSpanKey(v string) bool {
	return v != "" && len(v) <= 128 && utf8.ValidString(v) && !strings.ContainsAny(v, "\x00\r\n")
}
func (s *TraceQueryService) ListTraceSpanPage(ctx context.Context, traceID, after string) (SpanPage, error) {
	page := SpanPage{TraceID: traceID, Entries: []oteltracevo.TraceGraphNode{}, Sort: "span_id_asc", Consistency: "live"}
	if after != "" && !validSpanKey(after) {
		return page, ErrSpanPageCursor
	}
	if !s.SupportsSpanBrowsing() {
		return page, ErrSpanPageUnavailable
	}
	composite := map[string]any{"size": SpanBrowsePageSize, "sources": []any{map[string]any{"span_id": map[string]any{"terms": map[string]any{"field": "spanId.keyword", "order": "asc"}}}}}
	if after != "" {
		composite["after"] = map[string]string{"span_id": after}
	}
	query := map[string]any{"size": 0, "query": map[string]any{"term": map[string]string{"traceId": traceID}}, "aggs": map[string]any{"missing_span_id": map[string]any{"missing": map[string]string{"field": "spanId.keyword"}}, "spans": map[string]any{"composite": composite, "aggs": map[string]any{"sample": map[string]any{"top_hits": map[string]any{"size": 1, "_source": map[string]any{"includes": []string{"traceId", "spanId", "parentSpanId", "name", "kind", "startTime", "endTime", "startTimeUnixNano", "endTimeUnixNano", "resource.service.name", "status.code"}}}}}}}}
	raw, err := json.Marshal(query)
	if err != nil {
		return page, err
	}
	result, err := s.traceQueryPort.(tracequeryport.BoundedTraceQueryPort).SearchTraceSpans(ctx, raw)
	if errors.Is(err, tracequeryport.ErrSpanResponseBudget) {
		return page, ErrSpanPageBudget
	}
	if err != nil {
		return page, err
	}
	var response struct {
		TimedOut *bool `json:"timed_out"`
		Shards   *struct {
			Failed *int `json:"failed"`
		} `json:"_shards"`
		Aggregations *struct {
			Missing *struct {
				Count *int `json:"doc_count"`
			} `json:"missing_span_id"`
			Spans *struct {
				After   map[string]string `json:"after_key"`
				Buckets *[]struct {
					Key    map[string]string `json:"key"`
					Sample json.RawMessage   `json:"sample"`
				} `json:"buckets"`
			} `json:"spans"`
		} `json:"aggregations"`
	}
	if json.Unmarshal(result, &response) != nil || response.TimedOut == nil || *response.TimedOut || response.Shards == nil || response.Shards.Failed == nil || *response.Shards.Failed != 0 || response.Aggregations == nil || response.Aggregations.Missing == nil || response.Aggregations.Missing.Count == nil || *response.Aggregations.Missing.Count < 0 || response.Aggregations.Spans == nil || response.Aggregations.Spans.Buckets == nil {
		return page, ErrSpanPageIncomplete
	}
	if *response.Aggregations.Missing.Count > 0 {
		return page, ErrSpanStorageUnsupported
	}
	agg := response.Aggregations.Spans
	if len(*agg.Buckets) > SpanBrowsePageSize {
		return page, ErrSpanPageIncomplete
	}
	previous := after
	for _, b := range *agg.Buckets {
		key := b.Key["span_id"]
		if len(b.Key) != 1 || !validSpanKey(key) || key <= previous {
			return page, ErrSpanPageIncomplete
		}
		previous = key
		records, e := spansFromSearchResult(b.Sample, traceID)
		if e != nil || len(records) != 1 || records[0].Span.SpanID != key || records[0].Span.TraceID != traceID {
			return page, ErrSpanPageIncomplete
		}
		record := records[0]
		sp := record.Span
		start, ok := parseNano(sp.StartTimeUnixNano)
		end, endOK := parseNano(sp.EndTimeUnixNano)
		if !ok || !endOK || end < start {
			end = start
		}
		page.Entries = append(page.Entries, oteltracevo.TraceGraphNode{SpanID: key, ParentSpanID: sp.ParentSpanID, Name: sp.Name, Kind: sp.Kind, ServiceName: record.ServiceName, Status: spanStatus(sp.Status), StartNano: start, EndNano: end, DurationNano: end - start})
	}
	if len(agg.After) > 0 {
		next := agg.After["span_id"]
		if len(agg.After) != 1 || !validSpanKey(next) || next <= after || next < previous {
			return page, ErrSpanPageIncomplete
		}
		if len(*agg.Buckets) == SpanBrowsePageSize {
			page.HasMore = true
			page.AfterKey = next
		}
	}
	if len(*agg.Buckets) == SpanBrowsePageSize && !page.HasMore {
		return page, ErrSpanPageIncomplete
	}
	return page, nil
}
