// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

func TestCapturePackagePreservesLargeCompressibleSourceWithinBudget(t *testing.T) {
	const budget = 64 << 20
	body := append([]byte{'"'}, bytes.Repeat([]byte{'x'}, 49<<20)...)
	body = append(body, '"')
	m := CaptureManifest{Snapshot: sessionvo.EvidenceSnapshot{
		Interaction: sessionvo.EvidenceInteraction{ID: "synthetic-021-capture"},
		CallFacts: []sessionvo.OperationCallFact{{
			InteractionID: "synthetic-021-capture", OperationID: "synthetic-operation", Attempt: 1,
			Input: sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(body)},
		}},
	}}
	if len(body) >= budget {
		t.Fatal("fixture must be under the original raw budget")
	}
	raw, hash, err := EncodeCapture(m, budget)
	if err != nil {
		t.Fatalf("capture raw=%d bytes within %d budget must not fail only because of base64 expansion: %v", len(body), budget, err)
	}
	if len(raw) > budget || !json.Valid(raw) {
		t.Fatal("capture must remain bounded valid JSON")
	}
	got, err := DecodeCapture(raw, hash, budget)
	if err != nil {
		t.Fatal(err)
	}
	if got.Snapshot.Interaction.ID != m.Snapshot.Interaction.ID || !bytes.Equal(got.Snapshot.CallFacts[0].Input.Inline, body) {
		t.Fatal("source identity or exact bytes changed")
	}
}
