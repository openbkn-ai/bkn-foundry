// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecheckIDsRequireExplicitUniqueBoundedTargets(t *testing.T) {
	for _, s := range []string{`[]`, `[""]`, `["a","a"]`, `[" a"]`, `["a"] {}`, `["` + strings.Repeat("x", 129) + `"]`} {
		if _, err := readIDs(strings.NewReader(s)); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	ids, err := readIDs(strings.NewReader(`["int_a","int_b"]`))
	if err != nil || len(ids) != 2 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
}

func TestRecheckUsesServiceAuthEnvironmentForBothArtifactReads(t *testing.T) {
	t.Setenv("OPENSEARCH_AUTH_ENABLED", "true")
	t.Setenv("OPENSEARCH_AUTH_USERNAME", "configured-user")
	t.Setenv("OPENSEARCH_AUTH_PASSWORD", "configured-password")
	// Unrelated legacy tool names must not override the deployed service config.
	t.Setenv("OPENSEARCH_USERNAME", "wrong-user")
	t.Setenv("OPENSEARCH_PASSWORD", "wrong-password")
	auth, err := recheckOpenSearchAuth()
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		user, password, ok := r.BasicAuth()
		if !ok || user != "configured-user" || password != "configured-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("filter_path") != "" {
			_, _ = io.WriteString(w, `{"_source":{"content_json":"{}"}}`)
		} else {
			_, _ = io.WriteString(w, `{"_version":1,"_source":{}}`)
		}
	}))
	defer server.Close()
	client := opensearch.New(server.URL, auth, time.Second)
	document, _, err := client.GetDocumentBounded(context.Background(), "artifacts", "id", 1024, "content_json")
	if err != nil {
		t.Fatalf("authenticated metadata read failed: %v", err)
	}
	body, err := client.OpenContentJSON(context.Background(), "artifacts", "id", document.Version, 1024)
	if err != nil {
		t.Fatalf("authenticated content read failed: %v", err)
	}
	defer func() { _ = body.Close() }()
	raw, err := io.ReadAll(body)
	if err != nil || string(raw) != "{}" || requests.Load() != 2 {
		t.Fatalf("authenticated content did not complete: requests=%d err=%v", requests.Load(), err)
	}

	t.Setenv("OPENSEARCH_AUTH_PASSWORD", "incorrect-password")
	wrongAuth, err := recheckOpenSearchAuth()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = opensearch.New(server.URL, wrongAuth, time.Second).GetDocumentBounded(context.Background(), "artifacts", "id", 1024)
	if err == nil || recheckFailureReason(err) != "opensearch_auth_failed" {
		t.Fatal("real upstream 401 did not produce a safe authentication reason")
	}
}

func TestRecheckAuthConfigurationFailsBeforeDatabaseAccess(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ids.json")
	if err := os.WriteFile(file, []byte(`["int_a"]`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BKN_TRACE_CORE_MARIADB_DSN", "not-a-valid-dsn")
	t.Setenv("OPENSEARCH_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OPENSEARCH_AUTH_ENABLED", "true")
	for _, missing := range []string{"username", "password"} {
		t.Run(missing, func(t *testing.T) {
			t.Setenv("OPENSEARCH_AUTH_USERNAME", "configured-user")
			t.Setenv("OPENSEARCH_AUTH_PASSWORD", "configured-password")
			if missing == "username" {
				t.Setenv("OPENSEARCH_AUTH_USERNAME", "")
			} else {
				t.Setenv("OPENSEARCH_AUTH_PASSWORD", "")
			}
			if err := run([]string{"--ids-file", file}, io.Discard); !errors.Is(err, errOpenSearchAuthConfig) {
				t.Fatalf("missing auth did not fail before opening invalid database: %v", err)
			}
		})
	}
	t.Setenv("OPENSEARCH_AUTH_ENABLED", "false")
	t.Setenv("OPENSEARCH_AUTH_USERNAME", "")
	t.Setenv("OPENSEARCH_AUTH_PASSWORD", "")
	if auth, err := recheckOpenSearchAuth(); err != nil || auth.Enabled {
		t.Fatal("disabled authentication requires credentials")
	}
}

func TestRecheckAuthFailureReasonDoesNotExposeUpstreamBody(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		err := fmt.Errorf("wrapped: %w", &opensearch.StatusError{Operation: "bounded get", StatusCode: status, Body: "private-upstream-content"})
		want := "recheck_failed"
		if status == 401 || status == 403 {
			want = "opensearch_auth_failed"
		}
		if got := recheckFailureReason(err); got != want {
			t.Fatalf("reason=%q want=%q", got, want)
		}
	}
}
