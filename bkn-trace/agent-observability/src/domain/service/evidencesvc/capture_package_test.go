// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

func packageFixture() CaptureManifest {
	s := captureSnapshot("artifact:a")
	s.CallFacts = []sessionvo.OperationCallFact{{InteractionID: "int-a", OperationID: "op-a", Attempt: 2,
		Input:  sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(" { \"wide\":9223372036854775807, \"text\":\"<依据>\" } ")},
		Output: &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a"},
		Error:  &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`{ "message" : "失败" }`)},
	}}
	s.Ledger.Events = []sessionvo.EvidenceLedgerRecord{{EventID: "evt-a", Envelope: json.RawMessage(" {\n\"event_id\":\"evt-a\"\n} ")}}
	c := evidencevo.CaptureArtifactContent(captureFixture("a", "答案"), 10000)
	return CaptureManifest{Snapshot: s, Artifacts: []CapturedArtifact{{Reference: "artifact:a", Content: c, Sources: []ArtifactCaptureSource{{Pointer: "/receipts/0/artifact_refs/0", OperationID: "op-a", Attempt: 2}}}}, PartialReasons: []string{"partial_test"}, ReadAttempts: 1, ReadBytes: 200, CapturedBytes: len(c.CanonicalJSON)}
}

func TestCapturePackageFileRoundTripPreservesSourceBytes(t *testing.T) {
	original := packageFixture()
	inputBefore := bytes.Clone(original.Snapshot.CallFacts[0].Input.Inline)
	raw, id, err := EncodeCapture(original, 100000)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "capture.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeCapture(loaded, id, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, restored) {
		t.Fatal("manifest or original byte sequences changed in file round trip")
	}
	if !bytes.Equal(original.Snapshot.CallFacts[0].Input.Inline, inputBefore) {
		t.Fatal("encoding mutated source")
	}
	original.Snapshot.CallFacts[0].Input.Inline[1] = 'X'
	original.Snapshot.Interaction.RowVersion++
	original.Artifacts[0].Sources[0].Attempt = 99
	if reflect.DeepEqual(original, restored) {
		t.Fatal("restored package follows mutable source")
	}
	again, err := DecodeCapture(loaded, id, 100000)
	if err != nil {
		t.Fatal(err)
	}
	restored.Artifacts[0].Content.CanonicalJSON[0] = 'X'
	if again.Snapshot.Interaction.RowVersion != 7 || again.Artifacts[0].Sources[0].Attempt != 2 || again.Artifacts[0].Content.CanonicalJSON[0] != '{' {
		t.Fatal("decodes share mutable state")
	}
}

func TestCapturePackageIntegrityAndBudget(t *testing.T) {
	raw, id, err := EncodeCapture(packageFixture(), 100000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = EncodeCapture(packageFixture(), len(raw)-1); err == nil {
		t.Fatal("encode ignored budget")
	}
	if _, _, err = EncodeCapture(packageFixture(), len(raw)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		raw   []byte
		id    string
		limit int
	}{
		{"tamper", append(bytes.Clone(raw), ' '), id, 100000},
		{"wrong_hash", raw, "sha256:wrong", 100000},
		{"over_budget", raw, id, len(raw) - 1},
		{"invalid_budget", raw, id, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeCapture(tc.raw, tc.id, tc.limit); err == nil {
				t.Fatal("accepted invalid package")
			}
		})
	}
}

func TestCapturePackageRetainsMissingAndInterruptedStates(t *testing.T) {
	for _, ledgerRead := range []bool{false, true} {
		m := packageFixture()
		m.Artifacts = nil
		m.CapturedBytes = 0
		if !ledgerRead {
			m.Snapshot.Ledger = nil
		}
		for _, state := range []evidencevo.ArtifactContentState{evidencevo.ArtifactContentMissing, evidencevo.ArtifactContentCanceled, evidencevo.ArtifactContentRejected, evidencevo.ArtifactContentReferenceOnly, evidencevo.ArtifactContentOverBudget, evidencevo.ArtifactContentReadError} {
			m.Artifacts = append(m.Artifacts, CapturedArtifact{Reference: string(state), Content: evidencevo.ArtifactContentCapture{State: state, Reason: "recorded_gap"}})
		}
		raw, id, err := EncodeCapture(m, 100000)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeCapture(raw, id, 100000)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(m, got) {
			t.Fatal("gap or ledger read scope changed")
		}
	}
}

func TestCapturePackageRejectsUnsupportedOrAmbiguousLayout(t *testing.T) {
	raw, _, err := EncodeCapture(packageFixture(), 100000)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*capturePackage)
	}{
		{"future_version", func(p *capturePackage) { p.Version = "internal-capture-v2" }},
		{"missing_body", func(p *capturePackage) { p.Bodies = p.Bodies[:len(p.Bodies)-1] }},
		{"extra_body", func(p *capturePackage) { p.Bodies = append(p.Bodies, []byte(`{}`)) }},
		{"embedded_body", func(p *capturePackage) { p.Manifest.Snapshot.CallFacts[0].Input.Inline = json.RawMessage(`{}`) }},
		{"missing_scope", func(p *capturePackage) { p.Manifest.Snapshot.Interaction.ID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p capturePackage
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			tc.change(&p)
			changed, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeCapture(changed, capturePackageHash(changed), 100000); err == nil {
				t.Fatal("accepted unsupported package layout")
			}
		})
	}
}

func TestCapturePackagePreservesUnreadableLedgerAndEmptyPayloadBytes(t *testing.T) {
	m := packageFixture()
	// Capture retains unreadable ledger evidence with a gap. Saving it must not
	// parse/repair it or turn one unreadable row into failure of the entire package.
	m.Snapshot.Ledger.Events[0].Envelope = json.RawMessage("not JSON\xff")
	m.Snapshot.CallFacts[0].Input.Inline = json.RawMessage{}
	m.Snapshot.CallFacts[0].Error = nil
	m.PartialReasons = []string{"ledger_metadata_unreadable"}
	raw, id, err := EncodeCapture(m, 100000)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCapture(raw, id, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, got) {
		t.Fatal("unreadable evidence was normalized or empty became nil")
	}
	raw2, id2, err := EncodeCapture(got, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id || !bytes.Equal(raw, raw2) {
		t.Fatal("reopened package is unstable")
	}
}

func TestCapturePackageHashIncludesObservedScopeAndGaps(t *testing.T) {
	m := packageFixture()
	_, original, err := EncodeCapture(m, 100000)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*CaptureManifest){
		func(m *CaptureManifest) { m.Snapshot.Interaction.RowVersion++ },
		func(m *CaptureManifest) { m.PartialReasons = append(m.PartialReasons, "late_gap") },
		func(m *CaptureManifest) { m.Artifacts[0].Sources[0].Attempt++ },
		func(m *CaptureManifest) { m.Snapshot.Ledger = nil },
	} {
		changed := packageFixture()
		mutate(&changed)
		_, id, err := EncodeCapture(changed, 100000)
		if err != nil {
			t.Fatal(err)
		}
		if id == original {
			t.Fatal("different evidence scope reused package identity")
		}
	}
}
