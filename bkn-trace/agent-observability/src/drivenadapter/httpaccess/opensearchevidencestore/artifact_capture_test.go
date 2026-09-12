// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchevidencestore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
)

func captureFixture(t *testing.T) (string, evidencevo.QueryScope) {
	t.Helper()
	a := normalizedOpenSearchArtifact(t)
	a.ArtifactID = "artifact_capture"
	a.Content = map[string]any{"n": json.Number("9007199254740993")}
	a.ObservedAt = "2026-09-09T08:00:00+08:00"
	d, e := toArtifactDocument(a)
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(map[string]any{"_source": d})
	if e != nil {
		t.Fatal(e)
	}
	return string(b), evidencevo.QueryScope{AccountID: a.AccountID, AccountType: a.AccountType}
}
func TestReadArtifactForCaptureHTTPReadOnlyExactAndPrecision(t *testing.T) {
	raw, scope := captureFixture(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/evidence-artifacts/_doc/artifact_capture" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, raw)
	}))
	defer server.Close()
	s := New(opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "evidence")
	result, err := s.ReadArtifactForCapture(context.Background(), "artifact_capture", scope, int64(len(raw)))
	if err != nil || !result.Found || result.ReadBytes != int64(len(raw)) || calls != 1 {
		t.Fatalf("found=%v bytes=%d calls=%d err=%v", result.Found, result.ReadBytes, calls, err)
	}
	if result.Artifact.Content.(map[string]any)["n"] != json.Number("9007199254740993") {
		t.Fatal("lost integer precision")
	}
	if result.Artifact.ObservedAt != "2026-09-09T08:00:00+08:00" {
		t.Fatal("capture normalized source timestamp")
	}
	old, err := decodeArtifactDocument([]byte(strings.TrimSuffix(strings.TrimPrefix(raw, `{"_source":`), "}")))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := old.Content.(map[string]any)["n"].(float64); !ok || old.ObservedAt == result.Artifact.ObservedAt {
		t.Fatal("existing decoder compatibility changed")
	}
}
func TestReadArtifactForCaptureBudgetMissingScopeAndIdentity(t *testing.T) {
	raw, scope := captureFixture(t)
	for _, tc := range []struct {
		name, raw, id string
		status        int
		budget        int64
		scope         evidencevo.QueryScope
		wantErr       error
		wantMissing   bool
	}{
		{name: "over", raw: raw, status: 200, budget: int64(len(raw) - 1), scope: scope, wantErr: iartifactstore.ErrCaptureReadBudget},
		{name: "huge error", raw: strings.Repeat("private", 100000), status: 500, budget: 32, scope: scope, wantErr: iartifactstore.ErrCaptureReadBudget},
		{name: "missing", raw: `{"found":false}`, status: 404, budget: 32, scope: scope, wantMissing: true},
		{name: "scope", raw: raw, status: 200, budget: int64(len(raw)), scope: evidencevo.QueryScope{AccountID: "other", AccountType: scope.AccountType}, wantMissing: true},
		{name: "identity", raw: strings.Replace(raw, "artifact_capture", "artifact_wrong", 1), status: 200, budget: int64(len(raw)), scope: scope, wantErr: iartifactstore.ErrCaptureIdentityMismatch},
		{name: "invalid source", raw: `{"_source":null}`, status: 200, budget: 100, scope: scope},
		{name: "trailing content", raw: strings.Replace(raw, `9007199254740993}`, `9007199254740993} {}`, 1), status: 200, budget: 10000, scope: scope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(newFakeOpenSearchClient(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.raw)), Header: make(http.Header)}, nil
			}), "evidence")
			r, e := s.ReadArtifactForCapture(context.Background(), "artifact_capture", tc.scope, tc.budget)
			wantRead := int64(len(tc.raw))
			if wantRead > tc.budget+1 {
				wantRead = tc.budget + 1
			}
			if r.ReadBytes != wantRead {
				t.Fatalf("read=%d want=%d", r.ReadBytes, wantRead)
			}
			if r.Found || r.Artifact.ArtifactID != "" || r.Artifact.Content != nil {
				t.Fatal("failed read exposed artifact")
			}
			if tc.wantMissing {
				if e != nil {
					t.Fatal(e)
				}
			} else if tc.wantErr != nil {
				if !errors.Is(e, tc.wantErr) {
					t.Fatalf("err=%v", e)
				}
			} else if e == nil {
				t.Fatal("expected malformed source error")
			}
		})
	}
}
func TestReadArtifactForCaptureValidatesBeforeRequest(t *testing.T) {
	s := New(newFakeOpenSearchClient(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil }), "evidence")
	for _, budget := range []int64{-1, 0, math.MaxInt64} {
		r, e := s.ReadArtifactForCapture(context.Background(), "artifact_capture", evidencevo.QueryScope{}, budget)
		if r.ReadBytes != 0 || !errors.Is(e, iartifactstore.ErrInvalidCaptureBudget) {
			t.Fatalf("r=%+v err=%v", r, e)
		}
	}
	for _, id := range []string{"", "../other", "artifact/capture", " artifact_capture"} {
		r, e := s.ReadArtifactForCapture(context.Background(), id, evidencevo.QueryScope{}, 100)
		if r.ReadBytes != 0 || e == nil {
			t.Fatalf("invalid ID accepted: %q", id)
		}
	}
}
