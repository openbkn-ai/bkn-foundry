// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbpublication

import (
	"context"
	"errors"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type modelStorage struct {
	row                 map[string]any
	reads, writes       int
	failRead, uncertain bool
}

func (*modelStorage) Catalog(context.Context, string) (string, error) { return "catalog", nil }
func (*modelStorage) PublicationOwner(context.Context) (interfaces.AccountInfo, error) {
	return interfaces.AccountInfo{ID: "storage", Type: "app"}, nil
}
func (*modelStorage) Ensure(context.Context, interfaces.DraftDatasetSpec) error { return nil }
func (s *modelStorage) ReadPublishedModel(context.Context, interfaces.DraftDatasetSpec, string) (map[string]any, error) {
	s.reads++
	if s.failRead {
		return nil, errors.New("read unavailable")
	}
	if s.row == nil {
		return nil, kb.ErrPublishedModelMissing
	}
	return s.row, nil
}
func (s *modelStorage) Replace(_ context.Context, _ interfaces.DraftDatasetSpec, row map[string]any) error {
	s.writes++
	s.row = row
	if s.uncertain {
		return errors.New("write acknowledgement lost")
	}
	return nil
}
func TestPublishedSnapshotRecoversAcknowledgementWithoutOverwritingAnExistingRow(t *testing.T) {
	draft := frozenFixture(t)
	bundle, err := Freeze(draft, draft.Items.DatasetPlans[0])
	if err != nil {
		t.Fatal(err)
	}
	storage := &modelStorage{uncertain: true}
	owner, _ := storage.PublicationOwner(context.Background())
	spec, err := snapshotSpec("p", "release", "catalog", owner)
	if err != nil {
		t.Fatal(err)
	}
	record := &interfaces.KBPublication{Snapshot: spec, ModelFingerprint: bundle.ModelFingerprint, RuntimeDigest: bundle.RuntimeDigest, RuntimeRowID: bundle.RuntimeDigest}
	guard := func(context.Context) error { return nil }
	if err := persistSnapshot(context.Background(), storage, record, bundle, guard); err == nil {
		t.Fatal("lost acknowledgement was reported as success")
	}
	storage.uncertain = false
	if err := persistSnapshot(context.Background(), storage, record, bundle, guard); err != nil {
		t.Fatal("identical snapshot recovery failed", err)
	}
	if storage.writes != 1 {
		t.Fatal("recovery rewrote a previously stored snapshot")
	}
	storage.row["runtime_model"] = "tampered"
	if err := persistSnapshot(context.Background(), storage, record, bundle, guard); err == nil || storage.writes != 1 {
		t.Fatal("corrupt snapshot silently replaced")
	}
	storage.row = nil
	storage.failRead = true
	if err := persistSnapshot(context.Background(), storage, record, bundle, guard); err == nil || storage.writes != 1 {
		t.Fatal("failed read treated as absence")
	}
}
