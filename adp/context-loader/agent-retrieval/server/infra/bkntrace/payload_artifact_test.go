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

	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
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

func TestPayloadEscapedByCoreUsesReferencedArtifact(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		number string
	}{
		{name: "HTML and wide integer", body: strings.Repeat("<", 180000), number: "9007199254740993"},
		{name: "HTML and decimal", body: strings.Repeat("<>&", 60000), number: "12.50"},
		{name: "line separators and decimal", body: strings.Repeat("\u2028\u2029", 90000), number: "12.50"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &payloadArtifactWriterStub{}
			raw := json.RawMessage(`{"body":"` + tc.body + `","id":` + tc.number + `}`)
			payload, ref, err := boundedJSONPayloadWithWriter(t.Context(), writer, raw)
			if err != nil || payload.Mode != "referenced" || ref == "" || payload.ByteLength <= maxInlinePayloadBytes {
				t.Fatalf("payload mode = %q, size = %d, ref = %q, err = %v", payload.Mode, payload.ByteLength, ref, err)
			}
			if !bytes.Equal(writer.raw, raw) {
				t.Fatalf("artifact encoding changed the input bytes: got %d bytes, want %d", len(writer.raw), len(raw))
			}

			decoder := json.NewDecoder(bytes.NewReader(writer.raw))
			decoder.UseNumber()
			var content any
			if err := decoder.Decode(&content); err != nil {
				t.Fatal(err)
			}
			var canonical bytes.Buffer
			encoder := json.NewEncoder(&canonical)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(content); err != nil {
				t.Fatal(err)
			}
			escaped, err := json.Marshal(content)
			if err != nil {
				t.Fatal(err)
			}
			canonicalLength := canonical.Len() - 1 // Encoder.Encode appends a newline.
			if payload.ByteLength != canonicalLength && payload.ByteLength != len(escaped) {
				t.Fatalf("reference size = %d, Core accepts %d or %d", payload.ByteLength, canonicalLength, len(escaped))
			}
		})
	}
}

func TestPayloadNumberExpandedByCoreIsOmitted(t *testing.T) {
	writer := &payloadArtifactWriterStub{}
	raw := json.RawMessage(`{"body":"` + strings.Repeat("x", maxInlinePayloadBytes-32) + `","id":1e20}`)
	payload, ref, err := boundedJSONPayloadWithWriter(t.Context(), writer, raw)
	if err != nil || payload.Mode != "omitted" || payload.OmittedReason != "payload_too_large" || payload.ByteLength <= maxInlinePayloadBytes {
		t.Fatalf("payload mode = %q, reason = %q, size = %d, err = %v", payload.Mode, payload.OmittedReason, payload.ByteLength, err)
	}
	if ref != "" || payload.Ref != "" || writer.raw != nil {
		t.Fatalf("unexpected artifact: ref = %q, payload.ref = %q, size = %d", ref, payload.Ref, len(writer.raw))
	}
}

func TestCoreInlinePayloadLengthMatchesCoreEncoding(t *testing.T) {
	for _, raw := range []string{
		`{"a":"<b>&","b":"\u2028\u2029","c":[true,null,{"<key>":"value"}]}`,
		`{"numbers":[9007199254740993,-0,1e20,1e21,1e-6,1e-7,1.7976931348623157e308]}`,
	} {
		// Trace Core sizes its inline payload with encoding/json's v1 semantics.
		var coreValue any
		if err := json.Unmarshal([]byte(raw), &coreValue); err != nil {
			t.Fatal(err)
		}
		coreJSON, err := json.Marshal(coreValue)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := coreInlinePayloadLength([]byte(raw)); err != nil || got != len(coreJSON) {
			t.Errorf("Core length for %s = %d, %v, want %d", raw, got, err, len(coreJSON))
		}
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
		if err := jsoncodec.Unmarshal(body, &artifact); err != nil {
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
