// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
package main

import (
	"bytes"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"strings"
	"testing"
	"time"
)

func messageLedgerFixture() string {
	return `{"message_events":[{"event_id":"source-message-1","conversation_id":"conversation","interaction_id":"interaction","owner":{"application_principal_id":"agent","effective_subject_type":"user","effective_subject_id":"alice"},"producer_id":"offline-source","producer_stream_id":"source-thread","producer_epoch":1,"producer_sequence":1,"started_at":"2026-09-12T01:02:03.123456789Z","observed_at":"2026-09-12T01:02:03.123456789Z","emitted_at":"2026-09-12T01:02:03.123456789Z","ingested_at":"2026-09-12T01:02:03.123456Z","event_type":"agent.interaction.started","payload":{"question_artifact_ref":"artifact:source-question","content_hash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","intent_hash":"sha256:source-intent","mode":"chat","agent_id":"agent"}}]}`
}
func TestMessageLedgerStableNativeHashesAndSourceTimes(t *testing.T) {
	var first, second bytes.Buffer
	if err := prepareMessageLedger(strings.NewReader(messageLedgerFixture()), &first); err != nil {
		t.Fatal(err)
	}
	if err := prepareMessageLedger(strings.NewReader(messageLedgerFixture()), &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("unstable output")
	}
	var plan struct {
		Ledger []historicalLedgerRow `json:"ledger"`
	}
	if err := json.Unmarshal(first.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Ledger) != 1 {
		t.Fatal("missing source event")
	}
	row := plan.Ledger[0]
	var event ledgervo.Event
	if err := json.Unmarshal([]byte(row.Envelope), &event); err != nil {
		t.Fatal(err)
	}
	if event.SchemaVersion != "3.0.0" || event.CausalityStatus != "complete" || event.EventID != "source-message-1" || event.OperationID != "" || event.RequestID != "" || event.TraceID != "" || event.SpanID != "" || event.Attempt != 0 {
		t.Fatalf("fabricated event metadata: %+v", event)
	}
	expected, _ := time.Parse(time.RFC3339Nano, "2026-09-12T01:02:03.123456789Z")
	if !event.StartedAt.Equal(expected) || !event.ObservedAt.Equal(expected) || !event.EmittedAt.Equal(expected) || !time.Time(row.StartedAt).Equal(expected.Truncate(time.Microsecond)) || !time.Time(row.IngestedAt).Equal(expected.Truncate(time.Microsecond)) {
		t.Fatal("source timestamps replaced")
	}
	if event.PayloadHash != ledgervo.CanonicalPayloadHash(event.Envelope) || row.ImmutableRecordHash != ledgervo.ImmutableRecordHash(event) {
		t.Fatal("native hash mismatch")
	}
	var envelope struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(event.Envelope, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Payload) != 5 || envelope.Payload["question_artifact_ref"] != "artifact:source-question" {
		t.Fatal("source payload changed")
	}
	if envelope.Payload["content_hash"] != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatal("question hash lost")
	}
	if _, err := prepareHistoricalLedger(plan.Ledger); err != nil {
		t.Fatal(err)
	}
}
func TestMessageLedgerRejectsMissingOrInventedData(t *testing.T) {
	for _, change := range []struct{ old, new string }{{`"content_hash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`, ``}, {`"agent.interaction.started"`, `"model.completed"`}, {`"mode":"chat"`, `"mode":"task"`}, {`"artifact:source-question"`, `""`}, {`"ingested_at":"2026-09-12T01:02:03.123456Z",`, ``}, {`"payload":{`, `"trace_id":"invented","payload":{`}, {`"agent_id":"agent"`, `"agent_id":"agent","result":"invented"`}, {`"producer_sequence":1`, `"producer_sequence":0`}, {`"observed_at":"2026-09-12T01:02:03.123456789Z"`, `"observed_at":"2026-09-11T01:02:03Z"`}} {
		var output bytes.Buffer
		if err := prepareMessageLedger(strings.NewReader(strings.Replace(messageLedgerFixture(), change.old, change.new, 1)), &output); err == nil {
			t.Fatalf("accepted %s", change.old)
		}
	}
}
