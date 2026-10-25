// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package tracesvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchtraceaccess"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

func TestSpanPageRealOpenSearch(t *testing.T) {
	endpoint := os.Getenv("BKN_SPAN_PAGE_TEST_URL")
	if endpoint == "" {
		t.Skip("explicit disposable OpenSearch required")
	}
	if endpoint != "http://127.0.0.1:39258" && endpoint != "http://127.0.0.1:9200" {
		t.Fatal("isolated endpoint guard")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second}
	index := fmt.Sprintf("span-page-test-%d", time.Now().UnixNano())
	request := func(method, path string, body []byte) []byte {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		res, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil || res.StatusCode >= 300 {
			t.Fatalf("test request status=%d error=%v", res.StatusCode, e)
		}
		return raw
	}
	request("PUT", "/"+index, []byte(`{"mappings":{"properties":{"traceId":{"type":"text","fields":{"keyword":{"type":"keyword"}}},"spanId":{"type":"text","fields":{"keyword":{"type":"keyword"}}}}}}`))
	defer func() {
		r, e := http.NewRequest("DELETE", endpoint+"/"+index, nil)
		if e != nil {
			t.Error(e)
			return
		}
		res, e := client.Do(r)
		if e != nil {
			t.Error(e)
			return
		}
		res.Body.Close()
		if res.StatusCode >= 300 {
			t.Errorf("cleanup status=%d", res.StatusCode)
		}
	}()
	var bulk bytes.Buffer
	add := func(trace, span string) {
		fmt.Fprintf(&bulk, "{\"index\":{}}\n")
		body, _ := json.Marshal(map[string]any{"traceId": trace, "spanId": span, "startTime": "2026-01-01T00:00:00Z", "endTime": "2026-01-01T00:00:01Z", "name": "fixture", "resource": map[string]string{"service.name": "fixture-service"}})
		bulk.Write(body)
		bulk.WriteByte('\n')
	}
	for i := 0; i < 201; i++ {
		add("trace", fmt.Sprintf("s%05d", i))
	}
	add("trace", "s00050")
	for i := 0; i < 10001; i++ {
		add("deep", fmt.Sprintf("s%05d", i))
	}
	bulk.WriteString("{\"index\":{}}\n{\"traceId\":\"legacy\",\"resourceSpans\":[]}\n")
	raw := request("POST", "/"+index+"/_bulk?refresh=true", bulk.Bytes())
	var result struct {
		Errors bool `json:"errors"`
	}
	if e := json.Unmarshal(raw, &result); e != nil || result.Errors {
		t.Fatal("bulk failed", e)
	}
	svc := New(opensearchtraceaccess.New(opensearch.New(endpoint, opensearch.AuthConfig{}, 15*time.Second), index))
	after := ""
	count := 0
	previous := ""
	pages := 0
	for {
		page, e := svc.ListTraceSpanPage(ctx, "trace", after)
		if e != nil {
			t.Fatal(e)
		}
		pages++
		for _, entry := range page.Entries {
			if entry.ServiceName != "fixture-service" {
				t.Fatalf("dotted resource key was not preserved: %q", entry.ServiceName)
			}
			if entry.SpanID <= previous {
				t.Fatal("duplicate/unordered span")
			}
			previous = entry.SpanID
			count++
		}
		if !page.HasMore {
			break
		}
		if page.AfterKey <= after {
			t.Fatal("cursor stalled")
		}
		after = page.AfterKey
		if pages > 4 {
			t.Fatal("pagination did not terminate")
		}
	}
	if count != 201 || pages != 3 {
		t.Fatalf("count=%d pages=%d", count, pages)
	}
	if _, e := svc.ListTraceSpanPage(ctx, "legacy", ""); !errors.Is(e, ErrSpanStorageUnsupported) {
		t.Fatal("legacy not rejected", e)
	}
	page, e := svc.ListTraceSpanPage(ctx, "deep", "s09999")
	if e != nil || len(page.Entries) != 1 || page.Entries[0].SpanID != "s10000" || page.HasMore {
		t.Fatalf("deepseek page=%+v err=%v", page, e)
	}
	t.Log("201 unique spans + duplicate traversed in 3 pages; same timestamp ID order; 10001 deep seek succeeds; legacy unsupported")
}
