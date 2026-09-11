// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

// This is an internal storage encoding, not a published API/schema. Metadata
// includes capture gaps and observed states. Bodies are base64 JSON byte strings
// so JSON serialization cannot normalize ledger or CallFact source bytes.
const capturePackageVersion = "internal-capture-v1"

type capturePackage struct {
	Version  string          `json:"version"`
	Manifest CaptureManifest `json:"manifest"`
	Bodies   [][]byte        `json:"bodies"`
}

// EncodeCapture serializes a trusted CaptureService result, without I/O or
// changes to the source. maxBytes bounds the returned package, not encoder peak
// memory or the preceding snapshot read. The caller owns atomic storage and the
// trusted association of interaction, package hash and deletion lifecycle.
func EncodeCapture(m CaptureManifest, maxBytes int) ([]byte, string, error) {
	if maxBytes <= 0 {
		return nil, "", errors.New("invalid capture package budget")
	}
	if m.Snapshot.Interaction.ID == "" {
		return nil, "", errors.New("capture interaction required")
	}
	// Clone only the containers whose byte fields are cleared below. All other
	// fields are read-only during encoding; no normalization of source metadata.
	m.Snapshot.CallFacts = slices.Clone(m.Snapshot.CallFacts)
	for i := range m.Snapshot.CallFacts {
		f := &m.Snapshot.CallFacts[i]
		if f.Output != nil {
			p := *f.Output
			f.Output = &p
		}
		if f.Error != nil {
			p := *f.Error
			f.Error = &p
		}
	}
	if m.Snapshot.Ledger != nil {
		m.Snapshot.Ledger = &sessionvo.EvidenceLedgerSnapshot{Events: slices.Clone(m.Snapshot.Ledger.Events)}
	}
	m.Artifacts = slices.Clone(m.Artifacts)
	p := capturePackage{Version: capturePackageVersion, Manifest: m, Bodies: [][]byte{}}
	for _, slot := range captureBodySlots(&p.Manifest) {
		p.Bodies = append(p.Bodies, []byte(*slot))
		*slot = nil
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, "", err
	}
	if len(raw) > maxBytes {
		return nil, "", errors.New("capture package exceeds budget")
	}
	return raw, capturePackageHash(raw), nil
}

// DecodeCapture reopens only the supplied fixed package. expectedHash must come
// from the caller's trusted package reference, not from this same untrusted body.
// A checksum verifies byte integrity, not historical authenticity or adoption.
// It does not fetch current records, parse raw ledger events or resolve artifacts.
func DecodeCapture(raw []byte, expectedHash string, maxBytes int) (CaptureManifest, error) {
	if maxBytes <= 0 || len(raw) > maxBytes {
		return CaptureManifest{}, errors.New("invalid capture package budget")
	}
	if expectedHash != capturePackageHash(raw) {
		return CaptureManifest{}, errors.New("capture package hash mismatch")
	}
	var p capturePackage
	if err := json.Unmarshal(raw, &p); err != nil {
		return CaptureManifest{}, err
	}
	if p.Version != capturePackageVersion {
		return CaptureManifest{}, errors.New("unsupported capture package version")
	}
	if p.Manifest.Snapshot.Interaction.ID == "" {
		return CaptureManifest{}, errors.New("capture interaction required")
	}
	slots := captureBodySlots(&p.Manifest)
	if len(slots) != len(p.Bodies) {
		return CaptureManifest{}, errors.New("capture body count mismatch")
	}
	for i, slot := range slots {
		if len(*slot) != 0 && string(*slot) != "null" {
			return CaptureManifest{}, errors.New("unexpected embedded capture body")
		}
		*slot = json.RawMessage(p.Bodies[i])
	}
	return p.Manifest, nil
}

// Slot order is part of the private encoding version. Keep this list in sync
// with every raw body in CaptureManifest; metadata is encoded as typed JSON.
func captureBodySlots(m *CaptureManifest) []*json.RawMessage {
	slots := []*json.RawMessage{}
	for i := range m.Snapshot.CallFacts {
		f := &m.Snapshot.CallFacts[i]
		slots = append(slots, &f.Input.Inline)
		if f.Output != nil {
			slots = append(slots, &f.Output.Inline)
		}
		if f.Error != nil {
			slots = append(slots, &f.Error.Inline)
		}
	}
	if m.Snapshot.Ledger != nil {
		for i := range m.Snapshot.Ledger.Events {
			slots = append(slots, &m.Snapshot.Ledger.Events[i].Envelope)
		}
	}
	for i := range m.Artifacts {
		slots = append(slots, &m.Artifacts[i].Content.CanonicalJSON)
	}
	return slots
}

func capturePackageHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
