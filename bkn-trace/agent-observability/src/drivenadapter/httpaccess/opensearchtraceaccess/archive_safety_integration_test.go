// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package opensearchtraceaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

func TestArchiveFrozenSafetyRealOpenSearch(t *testing.T) {
	endpoint := os.Getenv("BKN_FROZEN_ARCHIVE_TEST_URL")
	if endpoint == "" {
		t.Skip("isolated OpenSearch required")
	}
	if endpoint != "http://127.0.0.1:39258" && endpoint != "http://127.0.0.1:9200" {
		t.Fatal("endpoint guard")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	prefix := fmt.Sprintf("frozen-test-%d", time.Now().UnixNano())
	indices := []string{prefix + "-a", prefix + "-b"}
	alias := prefix + "-alias"
	call := func(method, path string, body []byte) int {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		res, e := httpClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	for _, index := range indices {
		if call("PUT", "/"+index, []byte(`{"mappings":{"properties":{"traceId":{"type":"text","fields":{"keyword":{"type":"keyword"}}}}}}`)) != 200 {
			t.Fatal("create index")
		}
		defer func(index string) {
			r, _ := http.NewRequest("DELETE", endpoint+"/"+index, nil)
			res, e := httpClient.Do(r)
			if e != nil {
				t.Error(e)
				return
			}
			res.Body.Close()
			if res.StatusCode >= 300 {
				t.Error("cleanup", res.StatusCode)
			}
		}(index)
		if call("POST", "/_aliases", []byte(fmt.Sprintf(`{"actions":[{"add":{"index":%q,"alias":%q}}]}`, index, alias))) != 200 {
			t.Fatal("alias")
		}
	}
	put := func(index, id, trace string) {
		t.Helper()
		if status := call("PUT", "/"+index+"/_doc/"+id+"?routing=route&refresh=true", []byte(fmt.Sprintf(`{"traceId":%q,"spanId":"span"}`, trace))); status != 200 && status != 201 {
			t.Fatal("put", status)
		}
	}
	for _, index := range indices {
		put(index, "same", "trace")
	}
	store := NewArchiveStore(opensearch.New(endpoint, opensearch.AuthConfig{}, 5*time.Second), alias)
	candidates, e := store.Enrich(ctx, []archivesvc.Candidate{{ID: "candidate", Payload: []byte(`{"call_facts":[{"trace_id":"trace"}]}`)}})
	if e != nil {
		t.Fatal(e)
	}
	var payload struct {
		Records []opensearch.FrozenDocument `json:"technical_trace_records"`
	}
	if e = json.Unmarshal(candidates[0].Payload, &payload); e != nil || len(payload.Records) != 2 {
		t.Fatal("freeze", e)
	}
	for _, d := range payload.Records {
		if d.Routing != "route" || d.SeqNo == nil || d.PrimaryTerm == nil {
			t.Fatal("missing version/routing")
		}
	}
	put(indices[0], "late", "trace")
	if e = store.Purge(ctx, []archivesvc.Candidate{{Payload: []byte(`{"technical_trace_ids":["trace"],"technical_traces":[]}`)}}); e == nil {
		t.Fatal("legacy cleanup allowed")
	}
	// Change one frozen document. The other may delete, but changed/late must survive.
	put(indices[1], "same", "trace")
	if e = store.Purge(ctx, candidates); e == nil {
		t.Fatal("updated version deleted")
	}
	for _, path := range []string{indices[0] + "/_doc/late", indices[1] + "/_doc/same"} {
		if call("GET", "/"+path+"?routing=route", nil) != 200 {
			t.Fatal("unfrozen data removed")
		}
	}
	// A previously removed frozen version is a safe retry, even when CAS returns 409.
	if call("DELETE", "/"+indices[1]+"/_doc/same?routing=route&refresh=true", nil) != 200 {
		t.Fatal("fixture delete")
	}
	if e = store.Purge(ctx, candidates); e != nil {
		t.Fatalf("partially completed retry: %v", e)
	}
	if call("GET", "/"+indices[0]+"/_doc/late?routing=route", nil) != 200 {
		t.Fatal("late record deleted")
	}
	t.Log("alias concrete identities, routing/version freeze, CAS update protection, legacy refusal, partial-delete retry, late record retention passed")
}
