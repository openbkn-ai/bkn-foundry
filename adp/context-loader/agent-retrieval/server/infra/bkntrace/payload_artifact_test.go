// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

type payloadArtifactWriterStub struct {
	raw       []byte
	mediaType string
}

func (w *payloadArtifactWriterStub) Put(_ context.Context, mediaType string, raw []byte) (string, string, error) {
	w.mediaType, w.raw = mediaType, append([]byte(nil), raw...)
	return "artifact:payload-large", "sha256:recorded", nil
}

func TestLargePayloadUsesReferencedArtifactInsteadOfOmission(t *testing.T) {
	writer := &payloadArtifactWriterStub{}
	raw := json.RawMessage(`{"rows":["` + strings.Repeat("x", maxInlinePayloadBytes) + `"]}`)
	envelope, ref, err := boundedJSONPayloadWithWriter(context.Background(), writer, raw)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Mode != "referenced" || envelope.Ref != "artifact:payload-large" || envelope.ByteLength <= maxInlinePayloadBytes || ref != "artifact:payload-large" {
		t.Fatalf("large payload envelope = %#v ref=%q", envelope, ref)
	}
	if writer.mediaType != "application/json" || len(writer.raw) != envelope.ByteLength {
		t.Fatalf("artifact writer did not receive canonical payload: media=%q bytes=%d envelope=%d", writer.mediaType, len(writer.raw), envelope.ByteLength)
	}
}

func TestPayloadArtifactHashMatchesPostedPreciseContent(t *testing.T) {
	var artifact map[string]any
	previous := artifactHTTPClient
	t.Cleanup(func() { artifactHTTPClient = previous })
	artifactHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/agent-observability/v1/evidence/artifacts" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := common.UnmarshalPreciseJSON(body, &artifact); err != nil {
			t.Fatalf("decode artifact: %v", err)
		}
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	t.Setenv(envArtifactEndpoint, "http://trace.local/api/agent-observability/v1/evidence/artifacts")
	t.Setenv(envArtifactToken, "test-artifact-token")

	ctx := withPayloadArtifactScope(testTraceContext(), payloadArtifactScope{Direction: "output"})
	_, digest, err := (evidencePayloadArtifactWriter{}).Put(ctx, "application/json", []byte(`{"value":9007199254740993,"label":"<stock>"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := hashArtifactContent(artifact["content"]); err != nil || got != digest || artifact["content_hash"] != digest {
		t.Fatalf("payload artifact digest=%q content_hash=%q core_digest=%q err=%v", digest, artifact["content_hash"], got, err)
	}
	content := artifact["content"].(map[string]any)
	if content["value"] != json.Number("9007199254740993") {
		t.Fatalf("numeric payload changed before artifact submission: %#v", content)
	}
}

func TestArtifactContentHashMatchesCoreAfterArtifactTransportEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		content any
	}{
		{name: "line separators", content: map[string]any{"text": "first\u2028second\u2029third"}},
		{name: "invalid UTF-8", content: map[string]any{"text": string([]byte{'a', 0xff, 'b'})}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hashArtifactContent(tc.content)
			if err != nil {
				t.Fatalf("sender hash: %v", err)
			}
			body, err := json.Marshal(map[string]any{"content": tc.content})
			if err != nil {
				t.Fatalf("serialize artifact: %v", err)
			}
			var artifact struct {
				Content any `json:"content"`
			}
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.UseNumber()
			if err := decoder.Decode(&artifact); err != nil {
				t.Fatalf("Core decodes artifact: %v", err)
			}
			var canonical bytes.Buffer
			encoder := json.NewEncoder(&canonical)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(artifact.Content); err != nil {
				t.Fatalf("Core canonicalizes artifact: %v", err)
			}
			raw := bytes.TrimSuffix(canonical.Bytes(), []byte("\n"))
			sum := sha256.Sum256(raw)
			want := "sha256:" + hex.EncodeToString(sum[:])
			if got != want {
				t.Fatalf("sender hash=%q, Core hash after transport=%q", got, want)
			}
		})
	}
}
