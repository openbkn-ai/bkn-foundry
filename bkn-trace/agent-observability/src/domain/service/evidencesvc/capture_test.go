// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
)

type captureSnapshotReader struct {
	snapshot sessionvo.EvidenceSnapshot
	calls    int
}

func (r *captureSnapshotReader) ReadEvidenceSnapshot(ctx context.Context, _ string) (sessionvo.EvidenceSnapshot, bool, error) {
	r.calls++
	return r.snapshot, true, ctx.Err()
}

type captureArtifactReader struct {
	records map[string]evidencevo.EvidenceArtifact
	ids     []string
	budgets []int64
	hook    func(string) (iartifactstore.CaptureReadResult, error)
}

func (r *captureArtifactReader) ReadArtifactForCapture(_ context.Context, id string, _ evidencevo.QueryScope, budget int64) (iartifactstore.CaptureReadResult, error) {
	r.ids = append(r.ids, id)
	r.budgets = append(r.budgets, budget)
	if r.hook != nil {
		return r.hook(id)
	}
	a, found := r.records[id]
	return iartifactstore.CaptureReadResult{Artifact: a, Found: found, ReadBytes: 20}, nil
}
func captureFixture(id, text string) evidencevo.EvidenceArtifact {
	raw, _ := json.Marshal(map[string]any{"text": text, "quantity": json.Number("9223372036854775807")})
	sum := sha256.Sum256(raw)
	return evidencevo.EvidenceArtifact{ArtifactID: id, ArtifactType: evidencevo.ArtifactTypeResult, InteractionID: "int-a", RequestID: "req-a", SchemaVersion: evidencevo.ArtifactContractVersion, ContentHash: "sha256:" + hex.EncodeToString(sum[:]), Content: json.RawMessage(raw)}
}
func captureSnapshot(refs ...string) sessionvo.EvidenceSnapshot {
	return sessionvo.EvidenceSnapshot{Interaction: sessionvo.EvidenceInteraction{ID: "int-a", ConversationID: "conv-a", RowVersion: 7, ClosureManifest: &sessionvo.ClosureManifest{AnswerArtifactRef: "artifact:a"}}, Receipts: []sessionvo.Receipt{{ID: "rcpt-a", OperationID: "op-a", Attempt: 2, InteractionID: "int-a", ConversationID: "conv-a", RequestID: "req-a", ArtifactRefs: refs}}, Ledger: &sessionvo.EvidenceLedgerSnapshot{Events: []sessionvo.EvidenceLedgerRecord{}}}
}
func captureLimits() CaptureLimits {
	return CaptureLimits{MaxReads: 10, MaxResponseBytes: 100, MaxReadBytes: 1000, MaxContentBytes: 1000, MaxCapturedBytes: 10000}
}

func TestCaptureUsesFixedReferencesAndPreservesAllSources(t *testing.T) {
	snapshot := captureSnapshot("artifact:a", "artifact:b")
	event := ledgervo.Event{EventID: "evt-a", InteractionID: "int-a", ConversationID: "conv-a", RequestID: "req-a", OperationID: "op-a", Attempt: 2, ArtifactRefs: []string{"artifact:a"}}
	envelope, _ := json.Marshal(event)
	snapshot.Ledger.Events = append(snapshot.Ledger.Events, sessionvo.EvidenceLedgerRecord{EventID: "evt-a", Envelope: envelope})
	snapshot.CallFacts = []sessionvo.OperationCallFact{{OperationID: "op-a", Attempt: 2, InteractionID: "int-a", ConversationID: "conv-a", Input: sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`{ "wide":9223372036854775807 }`)}}}
	reader := &captureSnapshotReader{snapshot: snapshot}
	artifacts := &captureArtifactReader{records: map[string]evidencevo.EvidenceArtifact{"a": captureFixture("a", "answer"), "b": captureFixture("b", "source")}}
	got, found, err := NewCaptureService(reader, artifacts).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, captureLimits())
	if err != nil || !found {
		t.Fatalf("capture: %v %v", found, err)
	}
	if reader.calls != 1 || !reflect.DeepEqual(artifacts.ids, []string{"a", "b"}) {
		t.Fatalf("reads %d %v", reader.calls, artifacts.ids)
	}
	if len(got.Artifacts) != 2 || len(got.Artifacts[0].Sources) != 3 || got.Artifacts[0].Content.State != evidencevo.ArtifactContentCaptured {
		t.Fatalf("members: %+v", got.Artifacts)
	}
	if got.ReadBytes != 40 || got.ReadAttempts != 2 || got.CapturedBytes <= 0 {
		t.Fatalf("budgets: %+v", got)
	}
	if string(got.Snapshot.CallFacts[0].Input.Inline) != `{ "wide":9223372036854775807 }` {
		t.Fatal("source bytes normalized")
	}
	original := string(got.Artifacts[0].Content.CanonicalJSON)
	artifacts.records["a"] = captureFixture("a", "late replacement")
	if string(got.Artifacts[0].Content.CanonicalJSON) != original {
		t.Fatal("capture followed a later source")
	}
}

func TestCaptureScopeAndHashFailuresAreExplicit(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*evidencevo.EvidenceArtifact)
		reason string
	}{
		{"wrong_id", func(a *evidencevo.EvidenceArtifact) { a.ArtifactID = "other" }, "artifact_id_mismatch"},
		{"wrong_interaction", func(a *evidencevo.EvidenceArtifact) { a.InteractionID = "int-other" }, "interaction_mismatch"},
		{"unrecorded_operation", func(a *evidencevo.EvidenceArtifact) { a.OperationID = "op-absent" }, "operation_not_in_snapshot"},
		{"legacy_unknown_request", func(a *evidencevo.EvidenceArtifact) { a.InteractionID = ""; a.RequestID = "req-unknown" }, "unverified_source_scope"},
		{"tampered", func(a *evidencevo.EvidenceArtifact) { a.Content = map[string]any{"text": "changed"} }, "hash_mismatch"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			a := captureFixture("a", "answer")
			tt.mutate(&a)
			got, _, err := NewCaptureService(&captureSnapshotReader{snapshot: captureSnapshot("artifact:a")}, &captureArtifactReader{records: map[string]evidencevo.EvidenceArtifact{"a": a}}).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, captureLimits())
			if err != nil || got.Artifacts[0].Content.Reason != tt.reason || len(got.Artifacts[0].Content.CanonicalJSON) != 0 {
				t.Fatalf("rejection: %+v %v", got, err)
			}
		})
	}
}

func TestCaptureAcceptsLegacyOnlyWithRecordedRequestReference(t *testing.T) {
	a := captureFixture("a", "legacy")
	a.InteractionID = ""
	for _, explicit := range []bool{false, true} {
		snapshot := captureSnapshot()
		if explicit {
			snapshot.Receipts[0].ArtifactRefs = []string{"artifact:a"}
		}
		got, _, err := NewCaptureService(&captureSnapshotReader{snapshot: snapshot}, &captureArtifactReader{records: map[string]evidencevo.EvidenceArtifact{"a": a}}).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, captureLimits())
		if err != nil {
			t.Fatal(err)
		}
		if (got.Artifacts[0].Content.State == evidencevo.ArtifactContentCaptured) != explicit {
			t.Fatalf("legacy membership invented: %+v", got)
		}
	}
}

func TestCaptureBudgetsStopFurtherReadsAndKeepMissingDistinct(t *testing.T) {
	for _, mode := range []string{"reads", "bytes", "retained", "read_error", "missing"} {
		t.Run(mode, func(t *testing.T) {
			limits := captureLimits()
			switch mode {
			case "reads":
				limits.MaxReads = 1
			case "bytes":
				limits.MaxReadBytes = 21
			case "retained":
				limits.MaxCapturedBytes = 1
			}
			reader := &captureArtifactReader{records: map[string]evidencevo.EvidenceArtifact{"a": captureFixture("a", "a"), "b": captureFixture("b", "b")}}
			if mode == "read_error" {
				reader.hook = func(string) (iartifactstore.CaptureReadResult, error) {
					return iartifactstore.CaptureReadResult{ReadBytes: 20}, errors.New("store unavailable")
				}
			}
			if mode == "missing" {
				reader.records = nil
			}
			got, _, err := NewCaptureService(&captureSnapshotReader{snapshot: captureSnapshot("artifact:a", "artifact:b")}, reader).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, limits)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "reads" || mode == "bytes" {
				if len(reader.ids) != 1 || got.Artifacts[1].Content.State != evidencevo.ArtifactContentOverBudget {
					t.Fatalf("budget ignored: %+v %v", got, reader.ids)
				}
			}
			if mode == "retained" && got.Artifacts[0].Content.State != evidencevo.ArtifactContentOverBudget {
				t.Fatalf("retained budget: %+v", got)
			}
			if mode == "read_error" && got.Artifacts[0].Content.Reason != "read_error" {
				t.Fatal("error is not missing")
			}
			if mode == "missing" && got.Artifacts[0].Content.State != evidencevo.ArtifactContentMissing {
				t.Fatal("missing is not empty success")
			}
			if got.ReadBytes > limits.MaxReadBytes || got.CapturedBytes > limits.MaxCapturedBytes {
				t.Fatal("total budget exceeded")
			}
		})
	}
}

func TestCaptureCancellationRetainsEarlierContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &captureArtifactReader{hook: func(id string) (iartifactstore.CaptureReadResult, error) {
		if id == "b" {
			cancel()
			return iartifactstore.CaptureReadResult{ReadBytes: 3}, ctx.Err()
		}
		return iartifactstore.CaptureReadResult{Artifact: captureFixture(id, id), Found: true, ReadBytes: 20}, nil
	}}
	got, found, err := NewCaptureService(&captureSnapshotReader{snapshot: captureSnapshot("artifact:a", "artifact:b", "artifact:c")}, reader).Capture(ctx, "int-a", evidencevo.QueryScope{}, captureLimits())
	if !found || !errors.Is(err, context.Canceled) || len(reader.ids) != 2 || got.Artifacts[0].Content.State != evidencevo.ArtifactContentCaptured || got.Artifacts[2].Content.Reason != "canceled" {
		t.Fatalf("cancellation: %+v %v", got, err)
	}
}

func TestCaptureInvalidBudgetAndUnrecognizedReferenceDoNotRead(t *testing.T) {
	reader := &captureSnapshotReader{snapshot: captureSnapshot("https://example.invalid/private")}
	artifacts := &captureArtifactReader{}
	if _, _, err := NewCaptureService(reader, artifacts).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, CaptureLimits{}); err == nil || reader.calls != 0 {
		t.Fatal("invalid budget reached storage")
	}
	got, _, err := NewCaptureService(reader, artifacts).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, captureLimits())
	if err != nil || len(artifacts.ids) != 1 || got.Artifacts[1].Content.Reason != "unsupported_artifact_reference" {
		t.Fatalf("external reference followed: %+v %v", got, err)
	}
}

func TestCaptureReferrerIsNotAssumedToBeProducer(t *testing.T) {
	snapshot := captureSnapshot("artifact:a")
	snapshot.Operations = []sessionvo.Operation{{ID: "op-producer", ConversationID: "conv-a", InteractionID: "int-a", Attempt: 1}}
	a := captureFixture("a", "shared result")
	a.OperationID = "op-producer"
	a.RequestID = "req-producer"
	got, _, err := NewCaptureService(&captureSnapshotReader{snapshot: snapshot}, &captureArtifactReader{records: map[string]evidencevo.EvidenceArtifact{"a": a}}).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, captureLimits())
	if err != nil || got.Artifacts[0].Content.State != evidencevo.ArtifactContentCaptured || got.Artifacts[0].SourceOperationID != "op-producer" || got.Artifacts[0].Sources[1].OperationID != "op-a" || got.Artifacts[0].Sources[1].Attempt != 2 {
		t.Fatalf("producer conflated with referrer: %+v %v", got, err)
	}
}

func TestCaptureReferenceOnlyDoesNotFetchExternalSnapshots(t *testing.T) {
	a := captureFixture("a", "ref")
	a.Content = nil
	a.SnapshotRef = "snapshot:external-saved-body"
	reader := &captureArtifactReader{records: map[string]evidencevo.EvidenceArtifact{"a": a}}
	got, _, err := NewCaptureService(&captureSnapshotReader{snapshot: captureSnapshot()}, reader).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, captureLimits())
	if err != nil || len(reader.ids) != 1 || got.Artifacts[0].Content.State != evidencevo.ArtifactContentReferenceOnly || got.CapturedBytes != 0 {
		t.Fatalf("reference followed or falsely captured: %+v %v", got, err)
	}
}

func TestCaptureRecordedPayloadReferenceAndLedgerGaps(t *testing.T) {
	snapshot := captureSnapshot()
	snapshot.CallFacts = []sessionvo.OperationCallFact{{OperationID: "op-a", Attempt: 0, InteractionID: "int-a", ConversationID: "conv-a", RequestID: "req-a", Input: sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:b"}}}
	snapshot.Ledger = nil
	reader := &captureArtifactReader{records: map[string]evidencevo.EvidenceArtifact{"a": captureFixture("a", "a"), "b": captureFixture("b", "b")}}
	got, _, err := NewCaptureService(&captureSnapshotReader{snapshot: snapshot}, reader).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, captureLimits())
	if err != nil || len(got.Artifacts) != 2 || got.Artifacts[1].Sources[0].Attempt != 0 || !reflect.DeepEqual(got.PartialReasons, []string{"ledger_not_read"}) {
		t.Fatalf("unknown attempt or unread ledger repaired: %+v %v", got, err)
	}
	snapshot.Ledger = &sessionvo.EvidenceLedgerSnapshot{Events: []sessionvo.EvidenceLedgerRecord{{EventID: "evt-bad", Envelope: json.RawMessage(`{"event_id":"evt-bad","interaction_id":"int-other","artifact_refs":["artifact:other"]}`)}}}
	reader.ids = nil
	got, _, err = NewCaptureService(&captureSnapshotReader{snapshot: snapshot}, reader).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, captureLimits())
	if err != nil || len(reader.ids) != 2 || !reflect.DeepEqual(got.PartialReasons, []string{"ledger_scope_mismatch"}) {
		t.Fatalf("foreign ledger followed: %+v %v", got, err)
	}
}

func TestCaptureLedgerOnlyProducerDoesNotNeedSyntheticOperation(t *testing.T) {
	for _, mode := range []string{"matching", "foreign", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			snapshot := captureSnapshot()
			snapshot.Receipts = nil
			event := ledgervo.Event{EventID: "evt-only", InteractionID: "int-a", ConversationID: "conv-a", OperationID: "op-only", Attempt: 2, RequestID: "req-a", ArtifactRefs: []string{"artifact:a"}}
			if mode == "foreign" {
				event.InteractionID = "int-other"
			}
			raw, _ := json.Marshal(event)
			if mode == "malformed" {
				raw = json.RawMessage(`{"event_id":`)
			}
			snapshot.Ledger.Events = []sessionvo.EvidenceLedgerRecord{{EventID: "evt-only", Envelope: raw}}
			artifact := captureFixture("a", "recorded only in ledger")
			artifact.OperationID = "op-only"
			result, _, err := NewCaptureService(&captureSnapshotReader{snapshot: snapshot}, &captureArtifactReader{records: map[string]evidencevo.EvidenceArtifact{"a": artifact}}).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, captureLimits())
			if err != nil {
				t.Fatal(err)
			}
			if (result.Artifacts[0].Content.State == evidencevo.ArtifactContentCaptured) != (mode == "matching") {
				t.Fatalf("ledger-only source eligibility: %+v", result.Artifacts)
			}
			if len(result.Snapshot.Operations) != 0 || len(result.Snapshot.Receipts) != 0 {
				t.Fatal("missing execution record synthesized")
			}
			if mode == "matching" && result.Artifacts[0].Sources[1].Attempt != 2 {
				t.Fatal("recorded attempt lost")
			}
		})
	}
}

func TestCaptureCollectsExplicitInteractionQuestionReference(t *testing.T) {
	snapshot := captureSnapshot()
	event := ledgervo.Event{EventID: "question", InteractionID: "int-a", ConversationID: "conv-a", EventType: "agent.interaction.started", Envelope: json.RawMessage(`{"payload":{"question_artifact_ref":"artifact:question"}}`)}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Ledger.Events = []sessionvo.EvidenceLedgerRecord{{EventID: "question", Envelope: raw}}
	var manifest CaptureManifest
	refs := collectCaptureReferences(snapshot, &manifest, map[string]bool{})
	found := false
	for _, ref := range refs {
		if ref.Reference == "artifact:question" {
			found = true
		}
	}
	if !found {
		t.Fatal("recorded question reference omitted")
	}
}
