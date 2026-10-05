// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"reflect"
	"strings"
	"testing"
)

func TestTerminalPayloadLargeInlineAllocationBudget(t *testing.T) {
	inline := `{"count":9007199254740993,"text":"` + strings.Repeat("x", 1<<20) + `"}`
	raw := `{"mode":"inline","inline":` + inline + `}`
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			p, c, err := decodeTerminalPayload(raw)
			if err != nil || c != nil || string(p.Inline) != inline {
				b.Fatal("large inline payload changed")
			}
		}
	})
	t.Logf("input bytes=%d allocation bytes/op=%d ns/op=%d", len(raw), result.AllocedBytesPerOp(), result.NsPerOp())
	if result.AllocedBytesPerOp() >= int64(3*len(raw)) {
		t.Fatalf("metadata presence inspection must not retain another full inline body: bytes/op=%d, input=%d", result.AllocedBytesPerOp(), len(raw))
	}
}

func TestTerminalPayloadPreservesLegacyJSONEdges(t *testing.T) {
	valid := `{"expectation":{"version":1,"closed":true,"events":[]},"original_durability":"pending"}`
	for _, raw := range []string{
		`{"mode":"inline","inline":{"n":9007199254740993,"html":"<>&"}}`,
		`{"mode":"inline","inline":null}`,
		`{"mode":"inline","inline":{},"_trace_evidence_completion":` + valid + `}`,
		`{"mode":"inline","inline":{},"_trace_evidence_completion":null}`,
		`{"mode":"inline","inline":{},"_trace_evidence_completion":"wrong","_trace_evidence_completion":` + valid + `}`,
		`{"mode":"inline","inline":{},"_trace_evidence_completion":{"original_durability":"pending"},"_trace_evidence_completion":{"expectation":{"version":1,"closed":true,"events":[]}}}`,
		`{"mode":"inline","inline":{},"_TRACE_EVIDENCE_COMPLETION":{"original_durability":"legacy"}}`,
		`{"mode":"inline","inline":{},"_trace_evidence_completion":` + valid + `,"_TRACE_EVIDENCE_COMPLETION":null}`,
		`{"mode":"inline","inline":{},"_trace_evidence_\u0063ompletion":` + valid + `}`,
		`{"mode":"inline","inline":{"_trace_evidence_completion":null}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			want, wantC, wantErr := legacyTerminalPayloadDecode(raw)
			got, gotC, gotErr := decodeTerminalPayload(raw)
			if (gotErr != nil) != (wantErr != nil) {
				t.Fatalf("error boundary changed: old=%v new=%v", wantErr, gotErr)
			}
			if gotErr == nil && (!reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotC, wantC)) {
				t.Fatal("legacy payload or metadata changed")
			}
		})
	}
}

// Frozen presence check preserves old exact-key and typed duplicate-field semantics.
func legacyTerminalPayloadDecode(raw string) (*sessionvo.PayloadEnvelope, *sessionvo.EvidenceCompletion, error) {
	var value storedTerminalPayload
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, nil, err
	}
	if _, present := fields["_trace_evidence_completion"]; present {
		c := value.EvidenceCompletion
		if c == nil || (c.OriginalDurability != sessionvo.DurabilityPending && c.OriginalDurability != sessionvo.DurabilityDurable && c.OriginalDurability != sessionvo.DurabilityFailed) {
			return nil, nil, fmt.Errorf("%w: call_fact.evidence_completion", isessionstore.ErrInvalidEvidenceJSON)
		}
		if c.RejectionReason != "" {
			hash, err := hex.DecodeString(c.RejectedExpectationHash)
			if err != nil || len(hash) != 32 || c.Expectation != nil {
				return nil, nil, fmt.Errorf("%w: call_fact.rejected_expectation", isessionstore.ErrInvalidEvidenceJSON)
			}
		} else if !sessionvo.EvidenceExpectationShapeValid(c.Expectation) {
			return nil, nil, fmt.Errorf("%w: call_fact.evidence_expectation", isessionstore.ErrInvalidEvidenceJSON)
		}
	}
	return &value.PayloadEnvelope, value.EvidenceCompletion, nil
}
