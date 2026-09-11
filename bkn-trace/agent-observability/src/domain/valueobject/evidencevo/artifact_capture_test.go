// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencevo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestCaptureArtifactContentUsesStoredCanonicalHash(t *testing.T) {
	artifact := EvidenceArtifact{
		ArtifactID: "answer-1", SchemaVersion: ArtifactContractVersion,
		Content:     "flowchart LR\nPO[采购订单<br/>]",
		ContentHash: "sha256:d66060c35bbc3f5f22a0afc9131d6f6670ca44fcd2121b448ea3dbac65879917",
	}
	got := CaptureArtifactContent(artifact, 4096)
	want := `"flowchart LR\nPO[采购订单<br/>]"`
	if got.State != ArtifactContentCaptured || string(got.CanonicalJSON) != want || got.Reason != "" {
		t.Fatalf("canonical content was not captured: %+v", got)
	}
	if got.DeclaredHash != artifact.ContentHash || got.ArtifactID != artifact.ArtifactID {
		t.Fatalf("source identity changed: %+v", got)
	}
	// The canonical JSON string and the plain report text have different hashes.
	plainHash := sha256.Sum256([]byte(artifact.Content.(string)))
	if got.DeclaredHash == "sha256:"+hex.EncodeToString(plainHash[:]) {
		t.Fatal("plain document bytes were confused with artifact canonical content")
	}
}

func TestCaptureArtifactContentPreservesNumbersAndOwnsBytes(t *testing.T) {
	content := map[string]any{"large": json.Number("9007199254740993"), "decimal": json.Number("0.100000000000000001")}
	canonical := `{"decimal":0.100000000000000001,"large":9007199254740993}`
	artifact := captureTestArtifact(content, canonical)
	got := CaptureArtifactContent(artifact, len(canonical))
	if got.State != ArtifactContentCaptured || string(got.CanonicalJSON) != canonical {
		t.Fatalf("numeric representation changed: %+v", got)
	}
	content["large"] = json.Number("7")
	if string(got.CanonicalJSON) != canonical {
		t.Fatal("captured body aliases the source")
	}
	got.CanonicalJSON[0] = '['
	if content["large"] != json.Number("7") {
		t.Fatal("captured bytes changed the source")
	}
}

func TestCaptureArtifactContentRejectsUnverifiedContent(t *testing.T) {
	original := captureTestArtifact("answer", `"answer"`)
	cases := []struct {
		name   string
		change func(*EvidenceArtifact)
		reason string
	}{
		{"content changed", func(a *EvidenceArtifact) { a.Content = "different" }, "hash_mismatch"},
		{"missing hash", func(a *EvidenceArtifact) { a.ContentHash = "" }, "invalid_hash"},
		{"malformed hash", func(a *EvidenceArtifact) { a.ContentHash = "sha256:bad" }, "invalid_hash"},
		{"uppercase hash", func(a *EvidenceArtifact) { a.ContentHash = strings.ToUpper(a.ContentHash) }, "invalid_hash"},
		{"padded hash", func(a *EvidenceArtifact) { a.ContentHash = " " + a.ContentHash + " " }, "invalid_hash"},
		{"unknown encoding version", func(a *EvidenceArtifact) { a.SchemaVersion = "9.0.0" }, "unsupported_schema"},
		{"invalid identity", func(a *EvidenceArtifact) { a.ArtifactID = "../answer" }, "invalid_artifact_id"},
		{"content and pointer", func(a *EvidenceArtifact) { a.SnapshotRef = "snapshot:body-1" }, "ambiguous_content"},
		{"unencodable content", func(a *EvidenceArtifact) { a.Content = make(chan int) }, "invalid_content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			artifact := original
			tc.change(&artifact)
			got := CaptureArtifactContent(artifact, 4096)
			if got.State != ArtifactContentRejected || got.Reason != tc.reason || got.CanonicalJSON != nil {
				t.Fatalf("unverified content escaped: %+v", got)
			}
			if artifact.ContentHash != got.DeclaredHash {
				t.Fatal("declared source hash was replaced by a computed hash")
			}
		})
	}
}

func TestCaptureArtifactContentDoesNotResolvePointersOrInventMissingBody(t *testing.T) {
	artifact := captureTestArtifact(nil, `"not loaded"`)
	got := CaptureArtifactContent(artifact, 4096)
	if got.State != ArtifactContentMissing || got.CanonicalJSON != nil {
		t.Fatalf("missing body was invented: %+v", got)
	}
	artifact.SnapshotRef = "snapshot:body-1"
	got = CaptureArtifactContent(artifact, 4096)
	if got.State != ArtifactContentReferenceOnly || got.SnapshotRef != artifact.SnapshotRef || got.CanonicalJSON != nil {
		t.Fatalf("pointer was treated as verified body: %+v", got)
	}
	artifact.SnapshotRef = "https://example.invalid/report"
	got = CaptureArtifactContent(artifact, 4096)
	if got.State != ArtifactContentRejected || got.Reason != "invalid_reference" || got.CanonicalJSON != nil {
		t.Fatalf("unknown reference accepted: %+v", got)
	}
}

func TestCaptureArtifactContentBudgetUsesCanonicalUTF8Bytes(t *testing.T) {
	artifact := captureTestArtifact("物料", `"物料"`)
	for _, limit := range []int{0, -1, 7, 8} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			got := CaptureArtifactContent(artifact, limit)
			switch {
			case limit <= 0:
				if got.State != ArtifactContentRejected || got.Reason != "invalid_budget" {
					t.Fatalf("invalid budget: %+v", got)
				}
			case limit == 7:
				if got.State != ArtifactContentOverBudget {
					t.Fatalf("UTF8 bytes not counted: %+v", got)
				}
			case limit == 8:
				if got.State != ArtifactContentCaptured || string(got.CanonicalJSON) != `"物料"` {
					t.Fatalf("exact boundary: %+v", got)
				}
			}
			if got.State != ArtifactContentCaptured && got.CanonicalJSON != nil {
				t.Fatal("failed capture retained body")
			}
		})
	}
}

func TestCaptureArtifactContentKeepsEmptyValues(t *testing.T) {
	cases := []struct {
		content   any
		canonical string
	}{
		{"", `""`}, {false, `false`}, {json.Number("0"), `0`},
		{[]any{}, `[]`}, {map[string]any{}, `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.canonical, func(t *testing.T) {
			got := CaptureArtifactContent(captureTestArtifact(tc.content, tc.canonical), 4096)
			if got.State != ArtifactContentCaptured || string(got.CanonicalJSON) != tc.canonical {
				t.Fatalf("empty value treated as missing: %+v", got)
			}
		})
	}
}

func TestCaptureArtifactContentDetectsPriorDecodePrecisionLoss(t *testing.T) {
	artifact := captureTestArtifact(nil, `{"large":9007199254740993}`)
	// This reproduces the existing any/float64 read boundary without changing it.
	if err := json.Unmarshal([]byte(`{"large":9007199254740993}`), &artifact.Content); err != nil {
		t.Fatal(err)
	}
	got := CaptureArtifactContent(artifact, 4096)
	if got.State != ArtifactContentRejected || got.Reason != "hash_mismatch" || got.CanonicalJSON != nil {
		t.Fatalf("rounded content was accepted as original: %+v", got)
	}
	if !strings.HasPrefix(got.DeclaredHash, "sha256:") {
		t.Fatal("source hash lost")
	}
}

func captureTestArtifact(content any, canonical string) EvidenceArtifact {
	sum := sha256.Sum256([]byte(canonical))
	return EvidenceArtifact{
		ArtifactID: "artifact-test", SchemaVersion: ArtifactContractVersion,
		Content: content, ContentHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}
