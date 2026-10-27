// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchevidencestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

func TestStreamingIntegrityCanonicalHash(t *testing.T) {
	for _, raw := range []string{`{"a":9007199254740993,"b":["<>&😀",null,true,false,1.2300]}`, `[{},[],"x\\u003c",-2e3]`} {
		var v any
		d := json.NewDecoder(strings.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		var want bytes.Buffer
		encoder := json.NewEncoder(&want)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(v); err != nil {
			t.Fatal(err)
		}
		expected := bytes.TrimSuffix(want.Bytes(), []byte("\n"))
		var got bytes.Buffer
		n, escaped, err := hashCanonicalIntegrityJSON(strings.NewReader(raw), &got)
		if err != nil || !bytes.Equal(got.Bytes(), expected) || n != len(expected) || escaped != len(expected)+5*bytes.Count(expected, []byte("<"))+5*bytes.Count(expected, []byte(">"))+5*bytes.Count(expected, []byte("&")) {
			t.Fatalf("canonical mismatch: %s n=%d escaped=%d err=%v", got.Bytes(), n, escaped, err)
		}
	}
	for _, raw := range []string{`{"b":1,"a":2}`, `{"a":1,"a":2}`, `{"a":]}`, `[1,]`, `{} {}`, `null`, `"` + strings.Repeat("x", integrityTokenBytes) + `"`} {
		if _, _, err := hashCanonicalIntegrityJSON(strings.NewReader(raw), io.Discard); err == nil {
			t.Fatalf("invalid or unbounded JSON accepted: prefix %.30s", raw)
		}
	}
}

func TestVerifyArtifactForIntegrityStreamsVersionsAndScope(t *testing.T) {
	values := make([]string, 18)
	for i := range values {
		values[i] = strings.Repeat("x", 512<<10)
	}
	content := map[string]any{"items": values, "quantity": json.Number("9007199254740993"), "unicode": "😀<>&"}
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(content); err != nil {
		t.Fatal(err)
	}
	canonicalBytes := bytes.TrimSuffix(canonical.Bytes(), []byte("\n"))
	sum := sha256.Sum256(canonicalBytes)
	artifact := captureFixtureForIntegrity(content, "sha256:"+hex.EncodeToString(sum[:]))
	doc, err := toArtifactDocument(artifact)
	if err != nil {
		t.Fatal(err)
	}
	text := doc.ContentJSON
	doc.ContentJSON = ""
	for _, mode := range []string{"success", "changed-version", "hash", "scope", "missing", "truncated", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			calls, bytesRead := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/evidence-artifacts/_doc/"+artifact.ArtifactID {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if mode == "missing" {
					w.WriteHeader(404)
					return
				}
				if calls == 1 {
					if r.URL.Query().Get("_source_excludes") != "content_json" {
						t.Error("metadata downloaded content")
					}
					local := doc
					if mode == "hash" {
						local.ContentHash = "sha256:" + strings.Repeat("0", 64)
					}
					raw, _ := json.Marshal(map[string]any{"_version": 1, "_source": local})
					bytesRead += len(raw)
					_, _ = w.Write(raw)
					return
				}
				if r.URL.Query().Get("version") != "1" || r.URL.Query().Get("filter_path") != "_source.content_json" {
					t.Error("content request not version pinned/filtered")
				}
				if mode == "changed-version" {
					w.WriteHeader(409)
					return
				}
				raw, _ := json.Marshal(map[string]any{"_source": map[string]any{"content_json": text}})
				if mode == "truncated" {
					raw = raw[:len(raw)-3]
				}
				if mode == "trailing" {
					raw = append(raw, []byte("x")...)
				}
				bytesRead += len(raw)
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			store := New(opensearch.NewWithHTTPClient(server.URL, opensearch.AuthConfig{}, server.Client()), "evidence")
			scope := evidencevo.QueryScope{AccountID: artifact.AccountID, AccountType: artifact.AccountType}
			if mode == "scope" {
				scope.AccountID = "foreign"
			}
			got, err := store.VerifyArtifactForIntegrity(context.Background(), artifact.ArtifactID, scope)
			switch mode {
			case "success":
				if err != nil || !got.Found || got.CanonicalLength != len(canonicalBytes) || got.ReadBytes != int64(bytesRead) || calls != 2 {
					t.Fatalf("incomplete verification: %+v calls=%d err=%v", got, calls, err)
				}
			case "scope":
				if err != nil || got.Found || !got.Exists || calls != 1 {
					t.Fatalf("scope leak: %+v calls=%d err=%v", got, calls, err)
				}
			case "missing":
				if err != nil || got.Found || got.Exists || calls != 1 {
					t.Fatalf("missing object: %+v %v", got, err)
				}
			default:
				if err == nil || got.Found || got.CanonicalLength != 0 {
					t.Fatalf("bad object certified: %+v %v", got, err)
				}
			}
		})
	}
}
func captureFixtureForIntegrity(content any, hash string) evidencevo.EvidenceArtifact {
	return evidencevo.EvidenceArtifact{ArtifactID: "artifact_large_integrity", ArtifactType: evidencevo.ArtifactTypeDataResult, AccountID: "owner", AccountType: "user", Content: content, ContentHash: hash, SchemaVersion: evidencevo.ArtifactContractVersion, InteractionID: "int", OperationID: "op", RequestID: "req", TraceID: "trace"}
}
