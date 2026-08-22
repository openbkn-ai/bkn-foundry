// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInlineJSONPayloadKeepsCanonicalPayloadAtLimit(t *testing.T) {
	raw := json.RawMessage(`"` + strings.Repeat("a", MaxInlinePayloadBytes-2) + `"`)

	payload, err := InlineJSONPayload(raw)
	if err != nil {
		t.Fatalf("InlineJSONPayload() error = %v", err)
	}
	if payload.Mode != PayloadInline {
		t.Fatalf("Mode = %q, want %q", payload.Mode, PayloadInline)
	}
	if payload.MediaType != "application/json" {
		t.Fatalf("MediaType = %q, want application/json", payload.MediaType)
	}
	if payload.ByteLength != MaxInlinePayloadBytes {
		t.Fatalf("ByteLength = %d, want %d", payload.ByteLength, MaxInlinePayloadBytes)
	}
	if len(payload.Inline) != MaxInlinePayloadBytes {
		t.Fatalf("len(Inline) = %d, want %d", len(payload.Inline), MaxInlinePayloadBytes)
	}
	if payload.Ref != "" || payload.OmittedReason != "" {
		t.Fatalf("inline payload has unexpected ref or omitted reason: %+v", payload)
	}
}

func TestInlineJSONPayloadOmitsCanonicalPayloadOverLimit(t *testing.T) {
	raw := json.RawMessage(`"` + strings.Repeat("a", MaxInlinePayloadBytes-1) + `"`)

	payload, err := InlineJSONPayload(raw)
	if err != nil {
		t.Fatalf("InlineJSONPayload() error = %v", err)
	}
	if payload.Mode != PayloadOmitted {
		t.Fatalf("Mode = %q, want %q", payload.Mode, PayloadOmitted)
	}
	if payload.MediaType != "application/json" {
		t.Fatalf("MediaType = %q, want application/json", payload.MediaType)
	}
	if payload.ByteLength != MaxInlinePayloadBytes+1 {
		t.Fatalf("ByteLength = %d, want %d", payload.ByteLength, MaxInlinePayloadBytes+1)
	}
	if payload.Inline != nil || payload.Ref != "" {
		t.Fatalf("omitted payload retained content or ref: %+v", payload)
	}
	if payload.OmittedReason != PayloadOmittedReasonTooLarge {
		t.Fatalf("OmittedReason = %q, want %q", payload.OmittedReason, PayloadOmittedReasonTooLarge)
	}
}
