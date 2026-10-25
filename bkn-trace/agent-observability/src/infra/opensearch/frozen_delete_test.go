// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type frozenDeleteTransport func(*http.Request) (*http.Response, error)

func (f frozenDeleteTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func frozenFixture() FrozenDocument {
	seq, term := int64(4), int64(1)
	return FrozenDocument{Index: "concrete", ID: "one", SeqNo: &seq, PrimaryTerm: &term, Routing: "route"}
}
func frozenResponse(status int, result string) map[string]any {
	return map[string]any{"errors": false, "items": []any{map[string]any{"delete": map[string]any{"_index": "concrete", "_id": "one", "status": status, "result": result, "_shards": map[string]int{"failed": 0}}}}}
}
func TestDeleteFrozenDocumentsCASAndRetry(t *testing.T) {
	currentSeq := int64(5)
	exists := true
	calls := 0
	c := NewWithHTTPClient("http://test", AuthConfig{Enabled: true, Username: "u", Password: "p"}, &http.Client{Transport: frozenDeleteTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			raw, _ := json.Marshal(map[string]any{"_index": "concrete", "_id": "one", "found": exists})
			status := 200
			if !exists {
				status = 404
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		}
		calls++
		u, p, ok := r.BasicAuth()
		if !ok || u != "u" || p != "p" || r.URL.RawQuery != "refresh=true" || r.URL.Path != "/_bulk" {
			t.Fatal("request contract")
		}
		var action struct {
			Delete struct {
				Seq     int64  `json:"if_seq_no"`
				Term    int64  `json:"if_primary_term"`
				Routing string `json:"routing"`
			} `json:"delete"`
		}
		if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
			t.Fatal(err)
		}
		if action.Delete.Term != 1 || action.Delete.Routing != "route" {
			t.Fatal("CAS metadata missing")
		}
		status, result := 200, "deleted"
		if !exists {
			status, result = 404, "not_found"
		} else if action.Delete.Seq != currentSeq {
			status, result = 409, "conflict"
		} else {
			exists = false
		}
		raw, _ := json.Marshal(frozenResponse(status, result))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})})
	d := frozenFixture()
	if err := c.DeleteFrozenDocuments(context.Background(), []FrozenDocument{d}); err == nil || !exists {
		t.Fatal("changed version must survive")
	}
	*d.SeqNo = currentSeq
	if err := c.DeleteFrozenDocuments(context.Background(), []FrozenDocument{d}); err != nil || exists {
		t.Fatal("matching CAS", err)
	}
	if err := c.DeleteFrozenDocuments(context.Background(), []FrozenDocument{d}); err != nil {
		t.Fatal("404 retry", err)
	}
	invalid := frozenFixture()
	invalid.PrimaryTerm = nil
	if err := c.DeleteFrozenDocuments(context.Background(), []FrozenDocument{d, invalid}); err == nil || calls != 3 {
		t.Fatal("whole-list prevalidation failed")
	}
}
func TestDeleteFrozenDocumentsRejectsPartialResponses(t *testing.T) {
	for _, kind := range []string{"missing_item", "wrong_id", "item_error", "failed_shard", "missing_shard", "top_error", "http_error", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			v := frozenResponse(200, "deleted")
			d := v["items"].([]any)[0].(map[string]any)["delete"].(map[string]any)
			status := 200
			switch kind {
			case "missing_item":
				v["items"] = []any{}
			case "wrong_id":
				d["_id"] = "other"
			case "item_error":
				d["error"] = map[string]string{"reason": "private"}
			case "failed_shard":
				d["_shards"] = map[string]int{"failed": 1}
			case "missing_shard":
				delete(d, "_shards")
			case "top_error":
				v["errors"] = true
			case "http_error":
				status = 500
			}
			raw, _ := json.Marshal(v)
			if kind == "oversize" {
				raw = []byte(strings.Repeat("x", frozenDeleteResponseBytes+1))
			}
			c := NewWithHTTPClient("http://test", AuthConfig{}, &http.Client{Transport: frozenDeleteTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})})
			if err := c.DeleteFrozenDocuments(context.Background(), []FrozenDocument{frozenFixture()}); err == nil || strings.Contains(err.Error(), "private") {
				t.Fatal("failure/privacy", err)
			}
		})
	}
}

func TestDeleteFrozenConflictRequiresConfirmedAbsence(t *testing.T) {
	for _, kind := range []string{"absent", "present", "wrong_identity", "missing_found", "server_error"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			c := NewWithHTTPClient("http://test", AuthConfig{}, &http.Client{Transport: frozenDeleteTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var raw []byte
				status := 200
				if r.Method == http.MethodPost {
					v := frozenResponse(409, "conflict")
					v["errors"] = true
					raw, _ = json.Marshal(v)
				} else {
					if r.URL.Query().Get("routing") != "route" || r.URL.Query().Get("realtime") != "true" || r.URL.Query().Get("_source") != "false" {
						t.Fatal("confirmation contract")
					}
					v := map[string]any{"_index": "concrete", "_id": "one", "found": false}
					status = 404
					switch kind {
					case "present":
						v["found"] = true
						status = 200
					case "wrong_identity":
						v["_id"] = "other"
					case "missing_found":
						delete(v, "found")
					case "server_error":
						status = 503
					}
					raw, _ = json.Marshal(v)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})})
			err := c.DeleteFrozenDocuments(context.Background(), []FrozenDocument{frozenFixture()})
			if (err == nil) != (kind == "absent") || calls != 2 {
				t.Fatalf("kind=%s calls=%d error=%v", kind, calls, err)
			}
		})
	}
}
