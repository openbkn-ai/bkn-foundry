// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
package main

import (
	"context"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"testing"
)

type artifactMemory struct {
	items  map[string]evidencevo.EvidenceArtifact
	writes int
}

func (m *artifactMemory) StoreArtifact(_ context.Context, a evidencevo.EvidenceArtifact) (bool, error) {
	m.items[a.ArtifactID] = a
	m.writes++
	return true, nil
}
func (m *artifactMemory) GetArtifact(_ context.Context, id string, _ evidencevo.QueryScope) (evidencevo.EvidenceArtifact, bool, error) {
	a, ok := m.items[id]
	return a, ok, nil
}
func TestArtifactImportPreflightRepeatAndConflict(t *testing.T) {
	var a evidencevo.EvidenceArtifact
	if err := json.Unmarshal([]byte(`{"artifact_id":"question-1","artifact_type":"question","bkn.request.id":"request-1","trace_id":"0123456789abcdef0123456789abcdef","interaction_id":"interaction-1","operation_id":"operation-1","content_type":"application/json","schema_version":"2.2.0","observed_at":"2026-09-01T00:00:00Z","content":{"text":"Inventory?"},"bkn.account.id":"user-1","bkn.account.type":"user"}`), &a); err != nil {
		t.Fatal(err)
	}
	store := &artifactMemory{items: map[string]evidencevo.EvidenceArtifact{}}
	n, err := importArtifacts(context.Background(), store, []evidencevo.EvidenceArtifact{a})
	if err != nil || n != 1 {
		t.Fatalf("first import: %d %v", n, err)
	}
	n, err = importArtifacts(context.Background(), store, []evidencevo.EvidenceArtifact{a})
	if err != nil || n != 0 || store.writes != 1 {
		t.Fatalf("repeat: %d %v", n, err)
	}
	changed := a
	changed.Content = map[string]any{"text": "Changed"}
	if _, err = importArtifacts(context.Background(), store, []evidencevo.EvidenceArtifact{changed}); err == nil || store.writes != 1 {
		t.Fatal("conflict modified target")
	}
	valid := a
	valid.ArtifactID = "question-2"
	invalid := a
	invalid.ArtifactID = "../invalid"
	if _, err = importArtifacts(context.Background(), store, []evidencevo.EvidenceArtifact{valid, invalid}); err == nil || store.writes != 1 {
		t.Fatal("invalid batch partially written")
	}
}
