package bkntrace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// Regression for #1098.
//
// agent-observability admits an evidence event only if it can recompute payload_hash from the
// envelope, and it does that with encoding/json (ledgervo.CanonicalPayloadHash). So whatever this
// package hashes has to be encoding/json's exact bytes. The default sonic config is not: it neither
// sorts map keys nor escapes HTML, and Go randomises map iteration order, so the sender produced a
// different envelope - and a different hash - on every single call. Every event was rejected, which
// took bkn_start_interaction down, which took every MCP tool that carries a bkn_context with it.
//
// A map with several keys is the whole point of these fixtures: with one key there is no order to
// get wrong, and the bug hides.
func canonicalEventFixture() Event {
	return Event{
		"event_id":        "evt_9f3c",
		"event_type":      "context.search_schema",
		"interaction_id":  "int_7c9d",
		"operation_id":    "op_1a2b",
		"attempt":         1,
		"observed_at":     "2026-08-21T09:00:00Z",
		"emitted_at":      "2026-08-21T09:00:01Z",
		"zebra_last_key":  "sorts after everything else",
		"alpha_first_key": "sorts before everything else",
		"html_payload":    `a<b&c>d`,
	}
}

// receiverCanonicalHash mirrors ledgervo.CanonicalPayloadHash. It lives in bkn-trace, a separate
// module this one cannot import, so the contract is restated here rather than asserted against the
// real function - if that canonicalisation ever changes, this test is the thing that has to be
// updated in step with it.
func receiverCanonicalHash(t *testing.T, envelope []byte) string {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(envelope, &decoded); err == nil {
		if canonical, err := json.Marshal(decoded); err == nil {
			envelope = canonical
		}
	}
	sum := sha256.Sum256(envelope)
	return hex.EncodeToString(sum[:])
}

func TestEvidenceEnvelopeHashMatchesReceiverCanonicalisation(t *testing.T) {
	event := canonicalEventFixture()

	built, err := trace30EvidenceEvent(map[string]any{
		"bkn.conversation.id": "conv_1",
		"bkn.request.id":      "req_1",
		"trace_id":            "trace_1",
	}, event, nil)
	if err != nil {
		t.Fatalf("trace30EvidenceEvent failed: %v", err)
	}

	envelope, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	want := receiverCanonicalHash(t, envelope)

	if built.PayloadHash != want {
		t.Errorf("payload_hash would be rejected by agent-observability:\n  sent     = %s\n  receiver = %s",
			built.PayloadHash, want)
	}
}

// The failure that made this so hard to spot from a single log line: it is not deterministic. Two
// calls on the same input disagreed, so the same event hashed differently on every retry.
func TestEvidenceEnvelopeHashIsStableAcrossCalls(t *testing.T) {
	traceBlock := map[string]any{"bkn.conversation.id": "conv_1", "trace_id": "trace_1"}

	first, err := trace30EvidenceEvent(traceBlock, canonicalEventFixture(), nil)
	if err != nil {
		t.Fatalf("trace30EvidenceEvent failed: %v", err)
	}
	for i := 0; i < 32; i++ {
		again, err := trace30EvidenceEvent(traceBlock, canonicalEventFixture(), nil)
		if err != nil {
			t.Fatalf("trace30EvidenceEvent failed on call %d: %v", i, err)
		}
		if again.PayloadHash != first.PayloadHash {
			t.Fatalf("payload_hash changed between calls (call %d): %s != %s",
				i, again.PayloadHash, first.PayloadHash)
		}
	}
}

// HashValue is exported and takes any, so a map reaches it the moment a caller passes one - and an
// identity that changes per call is worse than a wrong one, because nothing downstream can match on
// it. vega's copy of this function already used ConfigStd; this one had been left on the default.
func TestHashValueIsStableAndMatchesEncodingJSON(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"map with several keys", map[string]any{"zebra": 1, "alpha": 2, "mid": 3, "beta": 4, "yak": 5}},
		{"string carrying HTML", `a<b&c>d`},
		{"nested map", map[string]any{"outer": map[string]any{"z": 1, "a": 2}, "list": []any{"b", "a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			sum := sha256.Sum256(raw)
			want := "sha256:" + hex.EncodeToString(sum[:])

			for i := 0; i < 32; i++ {
				if got := HashValue(tc.value); got != want {
					t.Fatalf("HashValue diverged from encoding/json on call %d:\n  got  = %s\n  want = %s",
						i, got, want)
				}
			}
		})
	}
}
