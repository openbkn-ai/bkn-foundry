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
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContentStreamEscapesTruncationAndBudget(t *testing.T) {
	for _, value := range []string{`"ordinary"`, `"\\\"\u003c"`, `😀<>&`, "\b\f\n\r\t", strings.Repeat("x", 100000)} {
		raw, _ := json.Marshal(map[string]any{"_source": map[string]any{"content_json": value}})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("version") != "2" || r.URL.Query().Get("filter_path") != "_source.content_json" {
				t.Error("missing version/filter")
			}
			_, _ = w.Write(raw)
		}))
		client := NewWithHTTPClient(server.URL, AuthConfig{}, server.Client())
		stream, err := client.OpenContentJSON(context.Background(), "i", "id", 2, int64(len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil || string(got) != value || stream.ReadBytes() != int64(len(raw)) {
			t.Fatalf("stream mismatch len=%d err=%v", len(got), err)
		}
		stream, err = client.OpenContentJSON(context.Background(), "i", "id", 2, int64(len(raw)-1))
		if err == nil {
			_, err = io.Copy(io.Discard, stream)
			_ = stream.Close()
		}
		if err == nil {
			t.Fatal("unbounded response")
		}
		server.Close()
	}
	for _, raw := range []string{`{"_source":{"content_json":"{}"}`, `{"_source":{"content_json":"{}`, `{"_source":{"content_json":"{}"}}x`, `{"_source":{"content_json":"\uD800x"}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, raw) }))
		client := NewWithHTTPClient(server.URL, AuthConfig{}, server.Client())
		stream, err := client.OpenContentJSON(context.Background(), "i", "id", 2, 1024)
		if err == nil {
			_, err = io.Copy(io.Discard, stream)
			_ = stream.Close()
		}
		server.Close()
		if err == nil {
			t.Fatalf("truncated or invalid stream accepted: %s", raw)
		}
	}
	// The outer JSON string can itself use a surrogate pair.
	raw := `{"_source":{"content_json":"\u003c\ud83d\ude00"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, raw) }))
	defer server.Close()
	stream, err := NewWithHTTPClient(server.URL, AuthConfig{}, server.Client()).OpenContentJSON(context.Background(), "i", "id", 1, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	got, err := io.ReadAll(stream)
	if err != nil || string(got) != "<😀" {
		t.Fatalf("surrogate mismatch %q %v", got, err)
	}
}
