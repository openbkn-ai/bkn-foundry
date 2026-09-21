package bkntrace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/bytedance/sonic"
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
// real function. ledgerSharedVector pins the restatement to the real function's output: the same
// vector and hash sit in ledgervo's canonical_hash_test.go, so a change on either side fails a test.
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

// ledgerSharedVector and ledgerSharedVectorHash also appear in bkn-trace's
// ledgervo/canonical_hash_test.go, where the hash is asserted against CanonicalPayloadHash itself.
// Keep the two copies byte for byte identical.
const (
	ledgerSharedVector     = `{"event_id":"evt-1711","payload":{"definition":{"zeta":"a<b>&c","alpha":9007199254740993,"mid":{"name":"n","code":"c"},"ratio":0.5}},"event_type":"ontology.schema.snapshot"}`
	ledgerSharedVectorHash = "42210712b1261e4939024f526fa34b419faf2058ab44afc75e8aae249a4ec4a6"
)

func TestCanonicalPayloadHashMatchesLedgerSharedVector(t *testing.T) {
	if got := canonicalPayloadHash([]byte(ledgerSharedVector)); got != ledgerSharedVectorHash {
		t.Fatalf("canonicalPayloadHash drifted from the ledger: got %s, want %s", got, ledgerSharedVectorHash)
	}
	if got := receiverCanonicalHash(t, []byte(ledgerSharedVector)); got != ledgerSharedVectorHash {
		t.Fatalf("receiverCanonicalHash no longer restates the ledger: got %s, want %s", got, ledgerSharedVectorHash)
	}
}

// Regression for #1711. The schema snapshot puts the tool's response struct into the envelope.
// Struct fields marshal in declaration order, while the receiver decodes the envelope into generic
// JSON and re-encodes it sorted, so a digest taken over the marshalled bytes never matched and every
// get_kn_detail / get_object_types / get_relation_types event was rejected. The fixture is built so
// that declaration order is not alphabetical, and it adds a nested struct, HTML characters, an
// integer above 2^53 and a float - each a way the two encodings can differ.
type snapshotDefinitionFixture struct {
	Zeta  string                   `json:"zeta"`
	Alpha int64                    `json:"alpha"`
	Mid   snapshotNestedDefinition `json:"mid"`
	Ratio float64                  `json:"ratio"`
}

type snapshotNestedDefinition struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

func TestEvidenceEnvelopeHashMatchesReceiverWhenEnvelopeCarriesStruct(t *testing.T) {
	event := Event{
		"event_id":   "evt-1711",
		"event_type": "ontology.schema.snapshot",
		"payload": map[string]any{
			"definition": snapshotDefinitionFixture{
				Zeta: "a<b>&c", Alpha: 9007199254740993, Ratio: 0.5,
				Mid: snapshotNestedDefinition{Name: "n", Code: "c"},
			},
		},
	}
	built, err := trace30EvidenceEvent(map[string]any{"bkn.conversation.id": "conv_1"}, event, nil)
	if err != nil {
		t.Fatalf("trace30EvidenceEvent failed: %v", err)
	}

	// What the receiver sees is the envelope inside the request body postBatch sends.
	body, err := sonic.ConfigStd.Marshal(built)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var wire struct {
		PayloadHash string          `json:"payload_hash"`
		Envelope    json.RawMessage `json:"envelope"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if want := receiverCanonicalHash(t, wire.Envelope); wire.PayloadHash != want {
		t.Fatalf("payload_hash would be rejected by agent-observability:\n  sent     = %s\n  receiver = %s",
			wire.PayloadHash, want)
	}
	if wire.PayloadHash != ledgerSharedVectorHash {
		t.Fatalf("an envelope equal to the shared vector hashed to %s, want %s", wire.PayloadHash, ledgerSharedVectorHash)
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
