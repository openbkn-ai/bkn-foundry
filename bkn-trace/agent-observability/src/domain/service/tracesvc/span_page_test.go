// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package tracesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
	"strings"
	"testing"
)

type spanPagePort struct {
	response []byte
	query    []byte
}

func (p *spanPagePort) SearchTraces(context.Context, json.RawMessage) (opensearchvo.SearchResult, error) {
	panic("unbounded fallback")
}
func (p *spanPagePort) SearchTraceSpans(_ context.Context, q json.RawMessage) (opensearchvo.SearchResult, error) {
	p.query = q
	return p.response, nil
}
func spanPageFixture(start, n int) map[string]any {
	b := []any{}
	for i := start; i < start+n; i++ {
		id := fmt.Sprintf("s%05d", i)
		b = append(b, map[string]any{"key": map[string]string{"span_id": id}, "sample": map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_source": map[string]any{"traceId": "trace", "spanId": id, "name": "step", "startTimeUnixNano": "1", "endTimeUnixNano": "2", "status": map[string]string{"code": "ERROR"}}}}}}})
	}
	a := map[string]any{"buckets": b}
	if n > 0 {
		a["after_key"] = map[string]string{"span_id": fmt.Sprintf("s%05d", start+n-1)}
	}
	return map[string]any{"timed_out": false, "_shards": map[string]any{"failed": 0}, "aggregations": map[string]any{"missing_span_id": map[string]any{"doc_count": 0}, "spans": a}}
}
func pagePort(t *testing.T, v map[string]any) *spanPagePort {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return &spanPagePort{response: b}
}
func TestSpanPageBoundedContinuation(t *testing.T) {
	p := pagePort(t, spanPageFixture(0, 100))
	s := New(p)
	page, e := s.ListTraceSpanPage(context.Background(), "trace", "")
	if e != nil || len(page.Entries) != 100 || !page.HasMore || page.AfterKey != "s00099" {
		t.Fatalf("page=%+v error=%v", page, e)
	}
	if !strings.Contains(string(p.query), `"size":100`) || strings.Contains(string(p.query), "status.message") || strings.Contains(string(p.query), "attributes") {
		t.Fatalf("unsafe query=%s", p.query)
	}
	p.response = pagePort(t, spanPageFixture(100, 1)).response
	page, e = s.ListTraceSpanPage(context.Background(), "trace", page.AfterKey)
	if e != nil || page.HasMore || len(page.Entries) != 1 || page.Entries[0].SpanID != "s00100" {
		t.Fatalf("last=%+v %v", page, e)
	}
	p.response = pagePort(t, spanPageFixture(0, 0)).response
	page, e = s.ListTraceSpanPage(context.Background(), "trace", "")
	if e != nil || len(page.Entries) != 0 || page.HasMore {
		t.Fatalf("empty=%+v %v", page, e)
	}
}
func TestSpanPageRejectsPartialOrUnsupported(t *testing.T) {
	for _, kind := range []string{"timeout", "shard", "missing", "duplicate", "regress", "wrong_trace", "wrong_span", "missing_metadata"} {
		t.Run(kind, func(t *testing.T) {
			v := spanPageFixture(0, 2)
			a := v["aggregations"].(map[string]any)
			b := a["spans"].(map[string]any)["buckets"].([]any)
			expected := ErrSpanPageIncomplete
			switch kind {
			case "timeout":
				v["timed_out"] = true
			case "shard":
				v["_shards"] = map[string]any{"failed": 1}
			case "missing":
				a["missing_span_id"] = map[string]any{"doc_count": 1}
				expected = ErrSpanStorageUnsupported
			case "duplicate":
				b[1] = b[0]
			case "regress":
				a["spans"].(map[string]any)["after_key"] = map[string]string{"span_id": "a"}
			case "wrong_trace", "wrong_span":
				src := b[0].(map[string]any)["sample"].(map[string]any)["hits"].(map[string]any)["hits"].([]any)[0].(map[string]any)["_source"].(map[string]any)
				if kind == "wrong_trace" {
					src["traceId"] = "other"
				} else {
					src["spanId"] = "other"
				}
			case "missing_metadata":
				delete(v, "timed_out")
			}
			_, e := New(pagePort(t, v)).ListTraceSpanPage(context.Background(), "trace", "")
			if !errors.Is(e, expected) {
				t.Fatalf("error=%v want=%v", e, expected)
			}
		})
	}
}

func TestSpanPageFlatSourceAndCursorValidation(t *testing.T) {
	v := spanPageFixture(0, 1)
	b := v["aggregations"].(map[string]any)["spans"].(map[string]any)["buckets"].([]any)[0].(map[string]any)
	b["sample"] = map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_source": map[string]any{"traceId": "trace", "spanId": "s00000", "startTime": "2026-01-01T00:00:00Z", "endTime": "2026-01-01T00:00:01Z", "resource": map[string]string{"service.name": "svc"}}}}}}
	s := New(pagePort(t, v))
	p, e := s.ListTraceSpanPage(context.Background(), "trace", "")
	if e != nil || len(p.Entries) != 1 || p.Entries[0].ServiceName != "svc" || p.Entries[0].DurationNano != 1000000000 {
		t.Fatalf("flat page=%+v err=%v", p, e)
	}
	if _, e = s.ListTraceSpanPage(context.Background(), "trace", strings.Repeat("a", 129)); !errors.Is(e, ErrSpanPageCursor) {
		t.Fatal(e)
	}
	if _, e = New(nil).ListTraceSpanPage(context.Background(), "trace", ""); !errors.Is(e, ErrSpanPageUnavailable) {
		t.Fatal(e)
	}
	if _, e = s.ListTraceSpanPage(context.Background(), "trace", "s00000"); !errors.Is(e, ErrSpanPageIncomplete) {
		t.Fatal("nonadvancing bucket", e)
	}
}
