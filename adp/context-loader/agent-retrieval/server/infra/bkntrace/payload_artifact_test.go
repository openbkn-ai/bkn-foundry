// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
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
	previous := evidenceHTTPClient
	t.Cleanup(func() { evidenceHTTPClient = previous })
	evidenceHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/agent-observability/v1/evidence/artifacts" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := common.UnmarshalPreciseJSON(body, &artifact); err != nil {
			t.Fatalf("decode artifact: %v", err)
		}
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	t.Setenv(envEvidenceIngestURL, "http://trace.local/api/agent-observability/v1/evidence/events")
	t.Setenv(envEvidenceIngestToken, "test-ingest-token")

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
