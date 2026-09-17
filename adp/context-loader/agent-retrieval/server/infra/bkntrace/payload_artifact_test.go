// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
