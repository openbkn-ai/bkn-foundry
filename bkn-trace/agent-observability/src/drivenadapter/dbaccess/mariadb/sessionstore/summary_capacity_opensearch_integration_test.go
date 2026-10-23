// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchtraceaccess"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type capacityTimedStore struct {
	*Store
	mu         sync.Mutex
	identityMS float64
}

func (s *capacityTimedStore) ListTraceSummaryIdentities(ctx context.Context, q isessionstore.SummaryPageQuery) (isessionstore.SummaryIdentityPage, error) {
	start := time.Now()
	p, e := s.Store.ListTraceSummaryIdentities(ctx, q)
	s.mu.Lock()
	s.identityMS = float64(time.Since(start).Microseconds()) / 1000
	s.mu.Unlock()
	return p, e
}

type capacityOSRequest struct {
	Path string          `json:"path"`
	Body json.RawMessage `json:"body,omitempty"`
}
type capacityOSTransport struct {
	mu       sync.Mutex
	requests []capacityOSRequest
}

func (c *capacityOSTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	c.mu.Lock()
	c.requests = append(c.requests, capacityOSRequest{r.URL.Path, body})
	c.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}
func (c *capacityOSTransport) take() []capacityOSRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.requests
	c.requests = nil
	return v
}

// Instrumentation delegates to the real production projection source unchanged.
type capacityRealProjection struct {
	source iprojectionsource.ProjectionSourcePort
	mu     sync.Mutex
	query  iprojectionsource.Query
	ms     float64
	calls  int
}

func (p *capacityRealProjection) LoadExecutionProjection(ctx context.Context, q iprojectionsource.Query) (iprojectionsource.Result, error) {
	started := time.Now()
	result, err := p.source.LoadExecutionProjection(ctx, q)
	if err == nil && len(result.Traces) != len(q.TraceIDs) {
		return result, fmt.Errorf("real projection returned %d traces for %d selected identities", len(result.Traces), len(q.TraceIDs))
	}
	p.mu.Lock()
	p.query = q
	p.ms = float64(time.Since(started).Microseconds()) / 1000
	p.calls++
	p.mu.Unlock()
	return result, err
}

func capacityOpenSearchHTTP(t *testing.T, store *Store, now time.Time) map[string]any {
	t.Helper()
	endpoint := os.Getenv("BKN_TRACE_CAPACITY_OPENSEARCH_URL")
	if endpoint == "" {
		return map[string]any{"status": "not run; BKN_TRACE_CAPACITY_OPENSEARCH_URL unset"}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		t.Fatal("capacity OpenSearch must be an explicit dedicated loopback http endpoint")
	}
	ctx := context.Background()
	httpClient := &http.Client{Timeout: 30 * time.Second}
	send := func(method, path string, body []byte) []byte {
		t.Helper()
		req, err := http.NewRequest(method, endpoint+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode >= 300 {
			t.Fatalf("OS %s %s: %d %s", method, path, response.StatusCode, raw)
		}
		return raw
	}
	prefix := fmt.Sprintf("bkn-trace-capacity-%d", time.Now().UnixNano())
	coreIndex, evidenceIndex, spanIndex := prefix+"-core", prefix+"-evidence", prefix+"-spans"
	defer func() {
		for _, index := range []string{coreIndex + "-v1", evidenceIndex, evidenceIndex + "-artifacts", spanIndex} {
			req, _ := http.NewRequest(http.MethodDelete, endpoint+"/"+index, nil)
			resp, err := httpClient.Do(req)
			if err != nil {
				t.Error(err)
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 && resp.StatusCode != 404 {
				t.Errorf("cleanup %s: %d", index, resp.StatusCode)
			}
		}
	}()
	transport := &capacityOSTransport{}
	client := opensearch.NewWithHTTPClient(endpoint, opensearch.AuthConfig{}, &http.Client{Transport: transport, Timeout: 30 * time.Second})
	if err := opensearchprojection.New(client, coreIndex).EnsureBootstrap(ctx, coreIndex+"-v1"); err != nil {
		t.Fatal(err)
	}
	send("PUT", "/"+spanIndex, []byte(`{"mappings":{"properties":{"traceId":{"type":"text","fields":{"keyword":{"type":"keyword"}}},"spanId":{"type":"text","fields":{"keyword":{"type":"keyword"}}}}}}`))
	var bulk bytes.Buffer
	appendDoc := func(index, id string, doc any) {
		meta, _ := json.Marshal(map[string]any{"index": map[string]string{"_index": index, "_id": id}})
		raw, _ := json.Marshal(doc)
		bulk.Write(meta)
		bulk.WriteByte('\n')
		bulk.Write(raw)
		bulk.WriteByte('\n')
	}
	for i := 0; i < 2202; i++ {
		id := fmt.Sprintf("capacity-%05d", i)
		at := now.Add(time.Duration(i/10) * time.Second).Format(time.RFC3339Nano)
		owner := sessionvo.Owner{ApplicationPrincipalID: "capacity-app", EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: "capacity-owner"}
		if i == 2200 {
			owner.EffectiveSubjectID = "foreign-owner"
		}
		for attempt := 0; attempt < 1+boolInt(i == 0); attempt++ {
			receipt := fmt.Sprintf("%s-r-%d", id, attempt)
			appendDoc(coreIndex, receipt, map[string]any{"receipt_id": receipt, "schema_version": "1", "owner": owner, "conversation_id": "capacity-c", "interaction_id": id, "operation_id": id + "-op-1", "operation_key": id + "-op-1", "tool_name": "run_sql", "receipt_status": "completed", "evidence_durability": "durable", "request_id": id, "trace_id": id, "issued_at": at, "terminal_at": at})
		}
		appendDoc(spanIndex, id, map[string]any{"traceId": id, "spanId": id + "-span", "startTime": at, "endTime": at, "name": "run_sql"})
	}
	var bulkResult struct {
		Errors bool `json:"errors"`
	}
	raw := send("POST", "/_bulk?refresh=true", bulk.Bytes())
	if err := json.Unmarshal(raw, &bulkResult); err != nil || bulkResult.Errors {
		t.Fatalf("bulk seed failed: %s", raw)
	}
	base := opensearchevidencestore.New(client, evidenceIndex)
	projection := &capacityRealProjection{source: opensearchcoreprojection.New(client, coreIndex, base)}
	timedStore := &capacityTimedStore{Store: store}
	svc := evidencesvc.New(base, evidencesvc.WithSessionStore(timedStore), evidencesvc.WithProjectionSource(projection), evidencesvc.WithCurrentRecordIntegrity(), evidencesvc.WithTraceStatsSource(tracesvc.New(opensearchtraceaccess.New(client, spanIndex))))
	handler := httphandler.NewEvidenceHandlerWithSecurityConfig(svc, httphandler.EvidenceHandlerSecurityConfig{AllowUnauthenticatedQuery: true})
	server := httptest.NewServer(http.HandlerFunc(handler.ListTraceExecutions))
	defer server.Close()
	get := func(query, owner string) (evidencevo.TraceSummaryPage, int, float64) {
		t.Helper()
		req, _ := http.NewRequest("GET", server.URL+"/api/agent-observability/v1/traces?"+query, nil)
		if owner != "" {
			req.Header.Set("x-account-id", owner)
			req.Header.Set("x-account-type", "service")
		}
		start := time.Now()
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		elapsed := float64(time.Since(start).Microseconds()) / 1000
		var page evidencevo.TraceSummaryPage
		if resp.StatusCode == 200 {
			if err := json.Unmarshal(body, &page); err != nil {
				t.Fatal(err)
			}
		} else if resp.StatusCode >= 500 {
			t.Fatalf("HTTP %d %s", resp.StatusCode, body)
		}
		return page, resp.StatusCode, elapsed
	}
	type samples struct {
		Identity   []float64           `json:"identity_count_page_ms"`
		HTTP       []float64           `json:"http_ms"`
		Projection []float64           `json:"projection_ms"`
		Requests   []int               `json:"opensearch_request_counts"`
		Example    []capacityOSRequest `json:"example_opensearch_requests"`
		Total      int                 `json:"total"`
		Returned   int                 `json:"returned"`
	}
	results := map[string]*samples{}
	const caseCount = 4
	// Interleave all four cases each round, reverse order on alternating rounds.
	for round := 0; round < 43; round++ {
		for ordinal := 0; ordinal < caseCount; ordinal++ {
			j := ordinal
			if round%2 == 1 {
				j = caseCount - 1 - ordinal
			}
			service := ""
			if j >= 2 {
				service = "context-loader"
			}
			limit := 1
			if j%2 == 1 {
				limit = 20
			}
			key := fmt.Sprintf("service=%s/limit=%d", service, limit)
			transport.take()
			projection.mu.Lock()
			projection.calls = 0
			projection.mu.Unlock()
			page, status, elapsed := get(fmt.Sprintf("service=%s&limit=%d", service, limit), "capacity-owner")
			want := 2201
			if service != "" {
				want = 2100
			}
			if status != 200 || page.Total != want || len(page.Entries) != limit || page.Truncated {
				t.Fatalf("%s HTTP%d total%d entries%d truncated%v", key, status, page.Total, len(page.Entries), page.Truncated)
			}
			for _, entry := range page.Entries {
				if entry.SpanCount != 1 {
					t.Fatalf("span count %s=%d", entry.TraceID, entry.SpanCount)
				}
				if service != "" && entry.RootService != service {
					t.Fatalf("root service %s=%q", entry.TraceID, entry.RootService)
				}
			}
			timedStore.mu.Lock()
			identityMS := timedStore.identityMS
			timedStore.mu.Unlock()
			requests := transport.take()
			projection.mu.Lock()
			q, projectionMS, calls := projection.query, projection.ms, projection.calls
			projection.mu.Unlock()
			if calls != 1 || len(q.TraceIDs) != limit {
				t.Fatalf("projection selected page: calls%d ids%d limit%d", calls, len(q.TraceIDs), limit)
			}
			if round >= 3 {
				v := results[key]
				if v == nil {
					v = &samples{Total: want, Returned: limit}
					results[key] = v
				}
				v.HTTP = append(v.HTTP, elapsed)
				v.Identity = append(v.Identity, identityMS)
				v.Projection = append(v.Projection, projectionMS)
				v.Requests = append(v.Requests, len(requests))
				if v.Example == nil {
					v.Example = requests
				}
			}
		}
	}
	// Traverse the complete service selection through the real HTTP + OS path.
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, status, _ := get("service=context-loader&limit=200&cursor="+url.QueryEscape(cursor), "capacity-owner")
		if status != 200 || page.Total != 2100 || page.Truncated {
			t.Fatal("HTTP cursor total/truncation invariant")
		}
		pages++
		for _, entry := range page.Entries {
			if seen[entry.TraceID] {
				t.Fatal("duplicate HTTP cursor identity")
			}
			seen[entry.TraceID] = true
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
		if pages > 12 {
			t.Fatal("HTTP cursor failed to terminate")
		}
	}
	if len(seen) != 2100 {
		t.Fatalf("HTTP cursor count%d", len(seen))
	}
	if _, status, _ := get("limit=1", ""); status != 401 {
		t.Fatalf("missing identity status%d", status)
	}
	if page, status, _ := get("limit=20", "foreign-owner"); status != 200 || page.Total != 1 || len(page.Entries) != 1 {
		t.Fatal("foreign owner isolation")
	}
	if _, status, _ := get("cursor=invalid&limit=1", "capacity-owner"); status != 400 {
		t.Fatalf("invalid cursor status%d", status)
	}
	report := map[string]any{"scope": "real loopback HTTP + production EvidenceHandler/Service + MariaDB + Core/artifact/TraceStats OpenSearch adapters; trusted headers injected; synthetic seed bypasses ingestion; no ingress/OAuth/EE/browser", "opensearch": json.RawMessage(send("GET", "/", nil)), "cursor_entries": len(seen), "cursor_pages": pages, "cases": results, "warmups": 3, "samples_per_case": 40, "sampling": fmt.Sprintf("%d cases interleaved; reversed each alternate round", caseCount)}
	percentiles := map[string]any{}
	p95 := map[string]float64{}
	for key, v := range results {
		values := append([]float64(nil), v.HTTP...)
		sort.Float64s(values)
		p95[key] = values[37]
		pv := append([]float64(nil), v.Projection...)
		sort.Float64s(pv)
		iv := append([]float64(nil), v.Identity...)
		sort.Float64s(iv)
		percentiles[key] = map[string]any{"identity_p50_ms": iv[19], "identity_p95_ms": iv[37], "http_p50_ms": values[19], "http_p95_ms": values[37], "projection_p50_ms": pv[19], "projection_p95_ms": pv[37]}
		t.Logf("real HTTP+OS %s p50 %.3f p95 %.3f OS requests %v", key, values[19], values[37], v.Requests[:3])
	}
	ratio := p95["service=context-loader/limit=1"] / p95["service=/limit=1"]
	report["percentiles"] = percentiles
	report["limit1_service_control_p95_ratio"] = ratio
	report["latency_under_1500ms"] = p95["service=context-loader/limit=1"] <= 1500
	report["ratio_under_2"] = ratio <= 2
	// Report unmet latency budgets instead of discarding the evidence with a fatal.
	if ratio > 2 {
		t.Logf("AC NOT MET: service/control HTTP P95 ratio %.3f > 2", ratio)
	}
	return report
}
